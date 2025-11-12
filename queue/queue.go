// Package queue provides a comprehensive task queue and background job processing system
// with advanced scheduling capabilities.
//
// It supports in-memory, Redis, and RabbitMQ queues with features like:
//   - Priority-based job scheduling
//   - Worker pool management
//   - Retry mechanisms with exponential backoff
//   - Middleware support for logging, metrics, and recovery
//   - Batch processing capabilities
//   - Graceful shutdown handling
//   - Job progress tracking
//   - Comprehensive error handling
//   - RabbitMQ support with persistent message delivery
//   - Scheduled job execution
//   - Cron-based scheduling (using enhanced cron expressions with optional seconds support)
//   - Interval-based scheduling (using time.Duration)
//   - Task registration and management
//   - Error recovery and retries
//   - Context-aware execution
//
// Queue Manager Example:
//
//	package main
//
//	import (
//		"context"
//		"fmt"
//		"time"
//
//		"github.com/valentin-kaiser/go-core/queue"
//	)
//
//	func main() {
//		// Create a new queue manager
//		manager := queue.NewManager()
//
//		// Register a job handler
//		manager.RegisterHandler("email", func(ctx context.Context, job *queue.Job) error {
//			fmt.Printf("Processing email job: %s\n", job.ID)
//			return nil
//		})
//
//		// Start the manager
//		if err := manager.Start(context.Background()); err != nil {
//			panic(err)
//		}
//		defer manager.Stop()
//
//		// Enqueue a job
//		job := queue.NewJob("email").
//			WithPayload(map[string]interface{}{
//				"to":      "user@example.com",
//				"subject": "Welcome!",
//			}).
//			Build()
//
//		if err := manager.Enqueue(context.Background(), job); err != nil {
//			panic(err)
//		}
//
//		// Wait for processing
//		time.Sleep(time.Second)
//	}
//
// Task Scheduler Example:
//
//	package main
//
//	import (
//		"context"
//		"fmt"
//		"time"
//
//		"github.com/valentin-kaiser/go-core/queue"
//	)
//
//	func main() {
//		// Create a new task scheduler
//		scheduler := queue.NewTaskScheduler()
//
//		// Register a cron-based task (5 fields - traditional)
//		scheduler.RegisterCronTask("cleanup", "0 0 * * *", func(ctx context.Context) error {
//			fmt.Println("Running daily cleanup task")
//			return nil
//		})
//
//		// Register a cron-based task with seconds (6 fields)
//		scheduler.RegisterCronTask("frequent", "*/30 * * * * *", func(ctx context.Context) error {
//			fmt.Println("Running every 30 seconds")
//			return nil
//		})
//
//		// Register a predefined expression
//		scheduler.RegisterCronTask("hourly", "@hourly", func(ctx context.Context) error {
//			fmt.Println("Running every hour")
//			return nil
//		})
//
//		// Register a task with named months and days
//		scheduler.RegisterCronTask("weekday", "0 0 9 * * MON-FRI", func(ctx context.Context) error {
//			fmt.Println("Running on weekdays at 9 AM")
//			return nil
//		})
//
//		// Register an interval-based task
//		scheduler.RegisterIntervalTask("health-check", time.Minute*5, func(ctx context.Context) error {
//			fmt.Println("Running health check")
//			return nil
//		})
//
//		// Start the scheduler
//		if err := scheduler.Start(context.Background()); err != nil {
//			panic(err)
//		}
//		defer scheduler.Stop()
//
//		// Keep the program running
//		select {}
//	}
package queue

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
)

// Queue defines the interface for job queues
type Queue interface {
	Enqueue(ctx context.Context, job *Job) error
	Dequeue(ctx context.Context, timeout time.Duration) (*Job, error)
	Schedule(ctx context.Context, job *Job) error
	UpdateJob(ctx context.Context, job *Job) error
	GetJob(ctx context.Context, id string) (*Job, error)
	GetJobs(ctx context.Context, status Status, limit int) ([]*Job, error)
	GetStats(ctx context.Context) (*Stats, error)
	DeleteJob(ctx context.Context, id string) error
	Close() error
}

// Stats represents queue statistics
type Stats struct {
	JobsProcessed int64 `json:"jobs_processed"`
	JobsFailed    int64 `json:"jobs_failed"`
	JobsRetried   int64 `json:"jobs_retried"`
	QueueSize     int64 `json:"queue_size"`
	WorkersActive int64 `json:"workers_active"`
	WorkersBusy   int64 `json:"workers_busy"`
	TotalJobs     int64 `json:"total_jobs"`
	Pending       int64 `json:"pending"`
	Running       int64 `json:"running"`
	Completed     int64 `json:"completed"`
	Failed        int64 `json:"failed"`
	Retrying      int64 `json:"retrying"`
	Scheduled     int64 `json:"scheduled"`
	DeadLetter    int64 `json:"dead_letter"`
}

// Manager manages the job queue and workers
type Manager struct {
	queue            Queue
	handlers         map[string]JobHandler
	workerCount      int
	maxRetries       int
	retryDelay       time.Duration
	retryBackoff     float64
	scheduleInterval time.Duration
	shutdownChan     chan struct{}
	workerWg         sync.WaitGroup
	handlerMutex     sync.RWMutex
	running          int32
	stats            *Stats
	statsMutex       sync.RWMutex
	scheduleTicker   *time.Ticker
	progressChan     chan progressUpdate
}

// NewManager creates a new queue manager with default settings
func NewManager() *Manager {
	return &Manager{
		queue:            NewMemoryQueue(),
		handlers:         make(map[string]JobHandler),
		workerCount:      1,
		maxRetries:       3,
		retryDelay:       time.Second * 2,
		retryBackoff:     2.0,
		scheduleInterval: time.Second * 10,
		shutdownChan:     make(chan struct{}),
		stats:            &Stats{},
		progressChan:     make(chan progressUpdate, 100),
	}
}

type progressUpdate struct {
	jobID    string
	progress float64
}

// RetryableError represents an error that should trigger a retry
type RetryableError struct {
	Err error
}

// NewRetryableError wraps an error to indicate that a job should be retried.
// When a job handler returns a RetryableError, the job manager will automatically
// retry the job according to the configured retry policy, instead of marking it as failed.
//
// Example usage:
//
//	func myJobHandler(job *queue.Job) error {
//		if err := doSomething(); err != nil {
//			if isTemporaryError(err) {
//				return queue.NewRetryableError(err) // Will retry
//			}
//			return err // Will fail permanently
//		}
//		return nil
//	}
func NewRetryableError(err error) *RetryableError {
	return &RetryableError{Err: err}
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

// JobProgress represents job progress information
type JobProgress struct {
	JobID    string  `json:"job_id"`
	Progress float64 `json:"progress"`
	Message  string  `json:"message,omitempty"`
}

// WithRabbitMQ sets the queue to use RabbitMQ with the given configuration
func (m *Manager) WithRabbitMQ(config RabbitMQConfig) *Manager {
	queue, err := NewRabbitMQ(config)
	if err == nil {
		m.queue = queue
	}
	return m
}

// WithRabbitMQFromURL sets the queue to use RabbitMQ with the given URL
func (m *Manager) WithRabbitMQFromURL(url string) *Manager {
	if queue, err := NewRabbitMQFromURL(url); err == nil {
		m.queue = queue
	}
	return m
}

// WithQueue sets the queue implementation
func (m *Manager) WithQueue(queue Queue) *Manager {
	m.queue = queue
	return m
}

// WithWorkers sets the number of workers
func (m *Manager) WithWorkers(workers int) *Manager {
	if workers > 0 {
		m.workerCount = workers
	}
	return m
}

// WithRetryAttempts sets the maximum number of retry attempts
func (m *Manager) WithRetryAttempts(attempts int) *Manager {
	if attempts >= 0 {
		m.maxRetries = attempts
	}
	return m
}

// WithRetryDelay sets the retry delay
func (m *Manager) WithRetryDelay(delay time.Duration) *Manager {
	if delay > 0 {
		m.retryDelay = delay
	}
	return m
}

// WithRetryBackoff sets the retry backoff multiplier
func (m *Manager) WithRetryBackoff(backoff float64) *Manager {
	if backoff > 0 {
		m.retryBackoff = backoff
	}
	return m
}

// WithScheduleInterval sets the interval for checking scheduled jobs
func (m *Manager) WithScheduleInterval(interval time.Duration) *Manager {
	if interval > 0 {
		m.scheduleInterval = interval
	}
	return m
}

// RegisterHandler registers a job handler for a specific job type
func (m *Manager) RegisterHandler(jobType string, handler JobHandler) {
	m.handlerMutex.Lock()
	defer m.handlerMutex.Unlock()
	m.handlers[jobType] = handler
}

// Start starts the queue manager
func (m *Manager) Start(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&m.running, 0, 1) {
		return apperror.NewError("manager is already running")
	}

	logger.Debug().
		Field("workers", m.workerCount).
		Field("max_retries", m.maxRetries).
		Field("retry_delay", m.retryDelay.Milliseconds()).
		Msg("starting queue manager")

	go m.progressReporter()

	m.scheduleTicker = time.NewTicker(m.scheduleInterval)
	go m.scheduleProcessor(ctx)

	for i := 0; i < m.workerCount; i++ {
		m.workerWg.Add(1)
		go m.worker(ctx, i)
	}

	return nil
}

// Stop stops the queue manager gracefully
func (m *Manager) Stop() error {
	if !atomic.CompareAndSwapInt32(&m.running, 1, 0) {
		return apperror.NewError("manager is not running")
	}

	logger.Info().Msg("stopping queue manager")
	if m.scheduleTicker != nil {
		m.scheduleTicker.Stop()
	}

	close(m.shutdownChan)

	done := make(chan struct{})
	go func() {
		m.workerWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info().Msg("queue manager stopped")
	case <-time.After(time.Second * 5):
		logger.Warn().Msg("timeout waiting for workers to stop")
	}

	return nil
}

// Enqueue adds a job to the queue
func (m *Manager) Enqueue(ctx context.Context, job *Job) error {
	if atomic.LoadInt32(&m.running) == 0 {
		return apperror.NewError("manager is not running")
	}

	if job.MaxAttempts == 0 {
		job.MaxAttempts = m.maxRetries
	}

	logger.Debug().
		Field("job_id", job.ID).
		Field("job_type", job.Type).
		Field("priority", job.Priority.String()).
		Msg("job enqueued")

	return m.queue.Enqueue(ctx, job)
}

// Schedule adds a scheduled job to the queue
func (m *Manager) Schedule(ctx context.Context, job *Job) error {
	if atomic.LoadInt32(&m.running) == 0 {
		return apperror.NewError("manager is not running")
	}

	if job.MaxAttempts == 0 {
		job.MaxAttempts = m.maxRetries
	}

	logger.Debug().
		Field("job_id", job.ID).
		Field("job_type", job.Type).
		Field("schedule_at", job.ScheduleAt).
		Msg("job scheduled")

	return m.queue.Schedule(ctx, job)
}

// GetJob retrieves a job by ID
func (m *Manager) GetJob(ctx context.Context, id string) (*Job, error) {
	return m.queue.GetJob(ctx, id)
}

// GetJobs retrieves jobs by status
func (m *Manager) GetJobs(ctx context.Context, status Status, limit int) ([]*Job, error) {
	return m.queue.GetJobs(ctx, status, limit)
}

// GetStats returns current queue statistics
func (m *Manager) GetStats() *Stats {
	m.statsMutex.RLock()
	defer m.statsMutex.RUnlock()

	ctx := context.Background()
	if queueStats, err := m.queue.GetStats(ctx); err == nil {
		queueStats.WorkersActive = atomic.LoadInt64(&m.stats.WorkersActive)
		queueStats.WorkersBusy = atomic.LoadInt64(&m.stats.WorkersBusy)
		return queueStats
	}

	// Fallback to manager stats only
	return &Stats{
		JobsProcessed: atomic.LoadInt64(&m.stats.JobsProcessed),
		JobsFailed:    atomic.LoadInt64(&m.stats.JobsFailed),
		JobsRetried:   atomic.LoadInt64(&m.stats.JobsRetried),
		QueueSize:     atomic.LoadInt64(&m.stats.QueueSize),
		WorkersActive: atomic.LoadInt64(&m.stats.WorkersActive),
		WorkersBusy:   atomic.LoadInt64(&m.stats.WorkersBusy),
	}
}

// IsRunning returns true if the manager is currently running
func (m *Manager) IsRunning() bool {
	return atomic.LoadInt32(&m.running) == 1
}

// worker processes jobs from the queue
func (m *Manager) worker(ctx context.Context, workerID int) {
	defer m.workerWg.Done()

	logger.Trace().Field("worker_id", workerID).Msg("worker started")

	atomic.AddInt64(&m.stats.WorkersActive, 1)
	defer atomic.AddInt64(&m.stats.WorkersActive, -1)

	for {
		select {
		case <-ctx.Done():
			logger.Trace().Field("worker_id", workerID).Msg("worker stopped due to context cancellation")
			return
		case <-m.shutdownChan:
			logger.Trace().Field("worker_id", workerID).Msg("worker stopped due to shutdown signal")
			return
		default:
			job, err := m.queue.Dequeue(ctx, time.Second*5)
			if err != nil {
				continue
			}

			if job != nil {
				atomic.AddInt64(&m.stats.WorkersBusy, 1)
				m.processJob(ctx, job, workerID)
				atomic.AddInt64(&m.stats.WorkersBusy, -1)
			}
		}
	}
}

// processJob processes a single job
func (m *Manager) processJob(ctx context.Context, job *Job, workerID int) {
	logger.Debug().
		Field("job_id", job.ID).
		Field("job_type", job.Type).
		Field("worker_id", workerID).
		Msg("processing job")

	job.Status = StatusRunning
	job.Attempts++
	job.UpdatedAt = time.Now()
	if err := m.queue.UpdateJob(ctx, job); err != nil {
		logger.Error().Err(err).Field("job_id", job.ID).Msg("failed to update job status to running")
	}

	m.handlerMutex.RLock()
	handler, exists := m.handlers[job.Type]
	m.handlerMutex.RUnlock()

	if !exists {
		job.Status = StatusFailed
		job.Error = "no handler registered for job type: " + job.Type
		job.UpdatedAt = time.Now()
		if err := m.queue.UpdateJob(ctx, job); err != nil {
			logger.Error().Err(err).Field("job_id", job.ID).Msg("failed to update job status to failed")
		}
		atomic.AddInt64(&m.stats.JobsFailed, 1)
		logger.Error().Field("job_id", job.ID).Field("job_type", job.Type).Msg("no handler found")
		return
	}

	err := handler(ctx, job)
	if err != nil {
		if retryErr, ok := err.(*RetryableError); ok && job.Attempts < job.MaxAttempts {
			job.Status = StatusRetrying
			job.Error = retryErr.Error()
			job.RetryAt = time.Now().Add(m.calculateRetryDelay(job.Attempts))
			job.UpdatedAt = time.Now()
			if err := m.queue.UpdateJob(ctx, job); err != nil {
				logger.Error().Err(err).Field("job_id", job.ID).Msg("failed to update job status to retrying")
			}
			atomic.AddInt64(&m.stats.JobsRetried, 1)

			logger.Debug().
				Field("job_id", job.ID).
				Field("job_type", job.Type).
				Field("retry_delay", m.calculateRetryDelay(job.Attempts).Milliseconds()).
				Msg("job scheduled for retry")

			go func() {
				time.Sleep(m.calculateRetryDelay(job.Attempts))
				job.Status = StatusPending
				job.RetryAt = time.Time{}
				if err := m.queue.Enqueue(ctx, job); err != nil {
					logger.Error().Err(err).Field("job_id", job.ID).Msg("failed to re-enqueue job for retry")
				}
			}()
			return
		}

		job.Status = StatusFailed
		job.Error = err.Error()
		job.UpdatedAt = time.Now()
		if err := m.queue.UpdateJob(ctx, job); err != nil {
			logger.Error().Err(err).Field("job_id", job.ID).Msg("failed to update job status to failed")
		}
		atomic.AddInt64(&m.stats.JobsFailed, 1)

		logger.Error().
			Err(err).
			Field("job_id", job.ID).
			Field("job_type", job.Type).
			Field("attempt", job.Attempts).
			Msg("job failed")
		return
	}

	job.Status = StatusCompleted
	job.CompletedAt = time.Now()
	job.Progress = 1.0
	job.UpdatedAt = time.Now()
	if err := m.queue.UpdateJob(ctx, job); err != nil {
		logger.Error().Err(err).Field("job_id", job.ID).Msg("failed to update job status to completed")
	}
	atomic.AddInt64(&m.stats.JobsProcessed, 1)

	logger.Debug().
		Field("job_id", job.ID).
		Field("job_type", job.Type).
		Msg("job completed successfully")
}

// calculateRetryDelay calculates the retry delay with exponential backoff
func (m *Manager) calculateRetryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return m.retryDelay
	}

	delay := float64(m.retryDelay.Nanoseconds())
	multiplier := math.Pow(m.retryBackoff, float64(attempt-1))
	return time.Duration(delay * multiplier)
}

// scheduleProcessor processes scheduled jobs
func (m *Manager) scheduleProcessor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.shutdownChan:
			return
		case <-m.scheduleTicker.C:
			jobs, err := m.queue.GetJobs(ctx, StatusScheduled, 100)
			if err != nil {
				logger.Error().Err(err).Msg("failed to get scheduled jobs")
				continue
			}

			now := time.Now()
			for _, job := range jobs {
				if job.ScheduleAt.Before(now) || job.ScheduleAt.Equal(now) {
					job.Status = StatusPending
					job.ScheduleAt = time.Time{}
					if err := m.queue.Enqueue(ctx, job); err != nil {
						logger.Error().Err(err).Field("job_id", job.ID).Msg("failed to enqueue scheduled job")
					}
				}
			}
		}
	}
}

// progressReporter handles job progress updates
func (m *Manager) progressReporter() {
	for update := range m.progressChan {
		logger.Debug().
			Field("job_id", update.jobID).
			Field("progress", update.progress).
			Msg("job progress updated")
	}
}
