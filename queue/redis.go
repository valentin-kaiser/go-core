package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/valentin-kaiser/go-core/apperror"
)

// RedisQueue implements a Redis-backed job queue
type RedisQueue struct {
	client    redis.Cmdable
	keyPrefix string
	closed    bool
}

// NewRedisQueue creates a new Redis-backed queue.
//
// Parameters:
// - client: A Redis client implementing the redis.Cmdable interface, used to interact with the Redis database.
// - keyPrefix: A string used as a prefix for all Redis keys created by the queue. If an empty string is provided, the default prefix "queue" is used.
//
// Purpose:
// This function initializes a RedisQueue instance, which provides methods for enqueueing, dequeueing, and scheduling jobs in a Redis-backed job queue.
func NewRedisQueue(client redis.Cmdable, keyPrefix string) *RedisQueue {
	if keyPrefix == "" {
		keyPrefix = "queue"
	}
	return &RedisQueue{
		client:    client,
		keyPrefix: keyPrefix,
	}
}

// Enqueue adds a job to the queue
func (rq *RedisQueue) Enqueue(ctx context.Context, job *Job) error {
	if rq.closed {
		return apperror.NewError("queue is closed")
	}

	jobData, err := json.Marshal(job)
	if err != nil {
		return apperror.Wrap(err)
	}

	pipe := rq.client.Pipeline()
	pipe.HSet(ctx, rq.jobKey(job.ID), "data", jobData)

	if job.IsScheduled() {
		pipe.ZAdd(ctx, rq.scheduledKey(), redis.Z{
			Score:  float64(job.ScheduleAt.Unix()),
			Member: job.ID,
		})
	}

	if !job.IsScheduled() {
		pipe.ZAdd(ctx, rq.pendingKey(), redis.Z{
			Score:  float64(job.Priority),
			Member: job.ID,
		})
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return apperror.Wrap(err)
	}

	return nil
}

// Dequeue removes and returns the next job from the queue
func (rq *RedisQueue) Dequeue(ctx context.Context, timeout time.Duration) (*Job, error) {
	if rq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	result, err := rq.client.BZPopMax(ctx, timeout, rq.pendingKey()).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, context.DeadlineExceeded
		}
		return nil, apperror.Wrap(err)
	}

	memberStr, ok := result.Member.(string)
	if !ok {
		return nil, apperror.NewErrorf("invalid member type in redis result: expected string, got %T", result.Member)
	}
	jobData, err := rq.client.HGet(ctx, rq.jobKey(memberStr), "data").Result()
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	var job Job
	err = json.Unmarshal([]byte(jobData), &job)
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	return &job, nil
}

// Schedule adds a scheduled job to the queue
func (rq *RedisQueue) Schedule(ctx context.Context, job *Job) error {
	if rq.closed {
		return apperror.NewError("queue is closed")
	}

	jobData, err := json.Marshal(job)
	if err != nil {
		return apperror.Wrap(err)
	}

	pipe := rq.client.Pipeline()

	pipe.HSet(ctx, rq.jobKey(job.ID), "data", jobData)
	pipe.ZAdd(ctx, rq.scheduledKey(), redis.Z{
		Score:  float64(job.ScheduleAt.Unix()),
		Member: job.ID,
	})

	_, err = pipe.Exec(ctx)
	if err != nil {
		return apperror.Wrap(err)
	}

	return nil
}

// GetJob retrieves a job by ID
func (rq *RedisQueue) GetJob(ctx context.Context, id string) (*Job, error) {
	if rq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	jobData, err := rq.client.HGet(ctx, rq.jobKey(id), "data").Result()
	if err != nil {
		if err == redis.Nil {
			return nil, apperror.NewError("job not found")
		}
		return nil, apperror.Wrap(err)
	}

	var job Job
	err = json.Unmarshal([]byte(jobData), &job)
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	return &job, nil
}

// UpdateJob updates an existing job
func (rq *RedisQueue) UpdateJob(ctx context.Context, job *Job) error {
	if rq.closed {
		return apperror.NewError("queue is closed")
	}

	jobData, err := json.Marshal(job)
	if err != nil {
		return apperror.Wrap(err)
	}

	pipe := rq.client.Pipeline()
	pipe.HSet(ctx, rq.jobKey(job.ID), "data", jobData)
	pipe.ZRem(ctx, rq.pendingKey(), job.ID)
	pipe.ZRem(ctx, rq.scheduledKey(), job.ID)

	switch job.Status {
	case StatusPending:
		pipe.ZAdd(ctx, rq.pendingKey(), redis.Z{
			Score:  float64(job.Priority),
			Member: job.ID,
		})
	case StatusScheduled, StatusRetrying:
		pipe.ZAdd(ctx, rq.scheduledKey(), redis.Z{
			Score:  float64(job.ScheduleAt.Unix()),
			Member: job.ID,
		})
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return apperror.Wrap(err)
	}

	return nil
}

// DeleteJob removes a job from the queue
func (rq *RedisQueue) DeleteJob(ctx context.Context, id string) error {
	if rq.closed {
		return apperror.NewError("queue is closed")
	}

	pipe := rq.client.Pipeline()
	pipe.Del(ctx, rq.jobKey(id))
	pipe.ZRem(ctx, rq.pendingKey(), id)
	pipe.ZRem(ctx, rq.scheduledKey(), id)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return apperror.Wrap(err)
	}

	return nil
}

// GetJobs retrieves jobs by status
func (rq *RedisQueue) GetJobs(ctx context.Context, status Status, limit int) ([]*Job, error) {
	if rq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	var jobs []*Job
	var jobIDs []string
	var err error

	switch status {
	case StatusPending:
		jobIDs, err = rq.client.ZRevRange(ctx, rq.pendingKey(), 0, int64(limit-1)).Result()
	case StatusScheduled, StatusRetrying:
		jobIDs, err = rq.client.ZRange(ctx, rq.scheduledKey(), 0, int64(limit-1)).Result()
	default:
		// For other statuses, we need to scan through all jobs
		return rq.scanJobsByStatus(ctx, status, limit)
	}

	if err != nil {
		return nil, apperror.Wrap(err)
	}

	for _, jobID := range jobIDs {
		job, err := rq.GetJob(ctx, jobID)
		if err != nil {
			continue // Skip jobs that can't be retrieved
		}

		if job.Status == status {
			jobs = append(jobs, job)
		}
	}

	return jobs, nil
}

// scanJobsByStatus scans through all jobs to find ones with a specific status
func (rq *RedisQueue) scanJobsByStatus(ctx context.Context, status Status, limit int) ([]*Job, error) {
	var jobs []*Job
	var cursor uint64
	count := 0

	for {
		keys, nextCursor, err := rq.client.Scan(ctx, cursor, rq.keyPrefix+":job:*", 100).Result()
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		for _, key := range keys {
			if count >= limit {
				break
			}

			jobData, err := rq.client.HGet(ctx, key, "data").Result()
			if err != nil {
				continue
			}

			var job Job
			unmarshalErr := json.Unmarshal([]byte(jobData), &job)
			if unmarshalErr != nil {
				continue
			}

			if job.Status == status {
				jobs = append(jobs, &job)
				count++
			}
		}

		cursor = nextCursor
		if cursor == 0 || count >= limit {
			break
		}
	}

	return jobs, nil
}

// GetStats returns queue statistics
func (rq *RedisQueue) GetStats(ctx context.Context) (*Stats, error) {
	if rq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	stats := &Stats{}

	pendingCount, err := rq.client.ZCard(ctx, rq.pendingKey()).Result()
	if err != nil {
		return nil, apperror.Wrap(err)
	}
	stats.Pending = pendingCount
	stats.QueueSize += pendingCount

	scheduledCount, err := rq.client.ZCard(ctx, rq.scheduledKey()).Result()
	if err != nil {
		return nil, apperror.Wrap(err)
	}
	stats.Scheduled = scheduledCount
	stats.QueueSize += scheduledCount

	var cursor uint64
	for {
		keys, nextCursor, err := rq.client.Scan(ctx, cursor, rq.keyPrefix+":job:*", 100).Result()
		if err != nil {
			return nil, apperror.Wrap(err)
		}

		for _, key := range keys {
			jobData, err := rq.client.HGet(ctx, key, "data").Result()
			if err != nil {
				continue
			}

			var job Job
			unmarshalErr := json.Unmarshal([]byte(jobData), &job)
			if unmarshalErr != nil {
				continue
			}

			stats.TotalJobs++
			switch job.Status {
			case StatusRunning:
				stats.Running++
				stats.WorkersBusy++
			case StatusCompleted:
				stats.Completed++
				stats.JobsProcessed++
			case StatusFailed:
				stats.Failed++
				stats.JobsFailed++
			case StatusRetrying:
				stats.Retrying++
				stats.JobsRetried++
			case StatusDeadLetter:
				stats.DeadLetter++
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	return stats, nil
}

// Close closes the queue
func (rq *RedisQueue) Close() error {
	rq.closed = true
	return nil
}

// Key generation helpers
func (rq *RedisQueue) jobKey(id string) string {
	return fmt.Sprintf("%s:job:%s", rq.keyPrefix, id)
}

func (rq *RedisQueue) pendingKey() string {
	return rq.keyPrefix + ":pending"
}

func (rq *RedisQueue) scheduledKey() string {
	return rq.keyPrefix + ":scheduled"
}

// MoveScheduledToPending moves scheduled jobs that are ready to be processed
func (rq *RedisQueue) MoveScheduledToPending(ctx context.Context) error {
	if rq.closed {
		return apperror.NewError("queue is closed")
	}

	now := time.Now().Unix()

	jobIDs, err := rq.client.ZRangeByScore(ctx, rq.scheduledKey(), &redis.ZRangeBy{
		Min: "-inf",
		Max: strconv.FormatInt(now, 10),
	}).Result()

	if err != nil {
		return apperror.Wrap(err)
	}

	for _, jobID := range jobIDs {
		job, err := rq.GetJob(ctx, jobID)
		if err != nil {
			continue
		}

		job.Status = StatusPending
		job.ScheduleAt = time.Time{}

		updateErr := rq.UpdateJob(ctx, job)
		if updateErr != nil {
			continue
		}
	}

	return nil
}
