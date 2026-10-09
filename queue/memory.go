package queue

import (
	"container/heap"
	"context"
	"sync"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

// MemoryQueue implements an in-memory job queue with priority support
type MemoryQueue struct {
	jobs          map[string]*Job
	pendingJobs   *jobHeap
	scheduledJobs map[string]*Job
	mutex         sync.RWMutex
	notifyC       chan struct{}
	closed        bool
	finished      finishedJobs
}

// NewMemoryQueue creates and initializes a new in-memory job queue with priority support.
//
// Thread-safety: The queue is thread-safe, utilizing a sync.RWMutex for concurrent access
// and a channel for notifications. This ensures safe operations in multi-threaded environments.
//
// Intended use cases: The queue is suitable for managing jobs in memory, particularly in
// scenarios requiring priority-based scheduling and thread-safe operations. It is ideal for
// applications where job persistence is not required, and all jobs can be managed in memory.
func NewMemoryQueue() *MemoryQueue {
	return &MemoryQueue{
		jobs:          make(map[string]*Job),
		pendingJobs:   &jobHeap{},
		scheduledJobs: make(map[string]*Job),
		notifyC:       make(chan struct{}, 1),
		finished:      newFinishedJobs(defaultFinishedRetention),
	}
}

// WithFinishedRetention sets how many completed, failed and dead-lettered jobs the queue keeps
// for GetJob and GetJobs (default 10000). Older ones are dropped from the index; GetStats
// still counts them. Zero or less keeps every job, which grows without bound.
func (mq *MemoryQueue) WithFinishedRetention(n int) *MemoryQueue {
	mq.mutex.Lock()
	defer mq.mutex.Unlock()
	mq.finished = newFinishedJobs(n)
	return mq
}

// Enqueue adds a job to the queue
func (mq *MemoryQueue) Enqueue(_ context.Context, job *Job) error {
	mq.mutex.Lock()
	defer mq.mutex.Unlock()

	if mq.closed {
		return apperror.NewError("queue is closed")
	}

	mq.jobs[job.ID] = job
	if !job.IsScheduled() {
		heap.Push(mq.pendingJobs, job)
		mq.notify()
		return nil
	}

	mq.scheduledJobs[job.ID] = job
	return nil
}

// Dequeue removes and returns the next job from the queue
func (mq *MemoryQueue) Dequeue(ctx context.Context, timeout time.Duration) (*Job, error) {
	mq.mutex.Lock()
	if mq.closed {
		mq.mutex.Unlock()
		return nil, apperror.NewError("queue is closed")
	}

	if mq.pendingJobs.Len() > 0 {
		jobInterface := heap.Pop(mq.pendingJobs)
		job, ok := jobInterface.(*Job)
		if !ok {
			mq.mutex.Unlock()
			return nil, apperror.NewError("invalid job type in queue")
		}
		mq.mutex.Unlock()
		return job, nil
	}
	mq.mutex.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, context.DeadlineExceeded
		case <-mq.notifyC:
			mq.mutex.Lock()
			if mq.closed {
				mq.mutex.Unlock()
				return nil, apperror.NewError("queue is closed")
			}

			if mq.pendingJobs.Len() > 0 {
				jobInterface := heap.Pop(mq.pendingJobs)
				job, ok := jobInterface.(*Job)
				if !ok {
					mq.mutex.Unlock()
					return nil, apperror.NewError("invalid job type in queue")
				}
				mq.mutex.Unlock()
				return job, nil
			}
			mq.mutex.Unlock()
		}
	}
}

// Schedule adds a scheduled job to the queue
func (mq *MemoryQueue) Schedule(_ context.Context, job *Job) error {
	mq.mutex.Lock()
	defer mq.mutex.Unlock()

	if mq.closed {
		return apperror.NewError("queue is closed")
	}

	mq.jobs[job.ID] = job
	mq.scheduledJobs[job.ID] = job
	return nil
}

// GetJob retrieves a job by ID
func (mq *MemoryQueue) GetJob(_ context.Context, id string) (*Job, error) {
	mq.mutex.RLock()
	defer mq.mutex.RUnlock()

	if mq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	job, exists := mq.jobs[id]
	if !exists {
		return nil, apperror.NewError("job not found")
	}

	return job, nil
}

// UpdateJob updates an existing job
func (mq *MemoryQueue) UpdateJob(_ context.Context, job *Job) error {
	mq.mutex.Lock()
	defer mq.mutex.Unlock()

	if mq.closed {
		return apperror.NewError("queue is closed")
	}

	mq.jobs[job.ID] = job

	switch job.Status {
	case StatusPending:
		delete(mq.scheduledJobs, job.ID)
		heap.Push(mq.pendingJobs, job)
		mq.notify()
	case StatusScheduled, StatusRetrying:
		mq.scheduledJobs[job.ID] = job
	case StatusCompleted, StatusFailed, StatusDeadLetter:
		delete(mq.scheduledJobs, job.ID)
		mq.finished.record(mq.jobs, job)
	}

	return nil
}

// DeleteJob removes a job from the queue
func (mq *MemoryQueue) DeleteJob(_ context.Context, id string) error {
	mq.mutex.Lock()
	defer mq.mutex.Unlock()

	if mq.closed {
		return apperror.NewError("queue is closed")
	}

	delete(mq.jobs, id)
	delete(mq.scheduledJobs, id)
	return nil
}

// GetJobs retrieves jobs by status
func (mq *MemoryQueue) GetJobs(_ context.Context, status Status, limit int) ([]*Job, error) {
	mq.mutex.RLock()
	defer mq.mutex.RUnlock()

	if mq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	var jobs []*Job
	count := 0

	for _, job := range mq.jobs {
		if job.Status == status {
			jobs = append(jobs, job)
			count++
			if limit > 0 && count >= limit {
				break
			}
		}
	}

	return jobs, nil
}

// GetStats returns queue statistics
func (mq *MemoryQueue) GetStats(_ context.Context) (*Stats, error) {
	mq.mutex.RLock()
	defer mq.mutex.RUnlock()

	if mq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	stats := &Stats{}

	for _, job := range mq.jobs {
		stats.TotalJobs++
		switch job.Status {
		case StatusPending:
			stats.Pending++
			stats.QueueSize++
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
		case StatusScheduled:
			stats.Scheduled++
			stats.QueueSize++
		case StatusDeadLetter:
			stats.DeadLetter++
		}
	}

	mq.finished.addTo(stats)

	return stats, nil
}

// Close closes the queue
func (mq *MemoryQueue) Close() error {
	mq.mutex.Lock()
	defer mq.mutex.Unlock()

	if mq.closed {
		return nil
	}

	mq.closed = true
	close(mq.notifyC)

	return nil
}

// notify sends a notification to waiting dequeuers
func (mq *MemoryQueue) notify() {
	select {
	case mq.notifyC <- struct{}{}:
	default:
		// Channel is full, notification already pending
	}
}

// jobHeap implements a priority queue for jobs
type jobHeap []*Job

func (h *jobHeap) Len() int { return len(*h) }

func (h *jobHeap) Less(i, j int) bool {
	// Higher priority values come first
	if (*h)[i].Priority != (*h)[j].Priority {
		return (*h)[i].Priority > (*h)[j].Priority
	}
	// If priorities are equal, earlier created jobs come first
	return (*h)[i].CreatedAt.Before((*h)[j].CreatedAt)
}

func (h *jobHeap) Swap(i, j int) { (*h)[i], (*h)[j] = (*h)[j], (*h)[i] }

func (h *jobHeap) Push(x interface{}) {
	job, ok := x.(*Job)
	if !ok {
		panic("expected *Job type")
	}
	*h = append(*h, job)
}

func (h *jobHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[0 : n-1]
	return item
}
