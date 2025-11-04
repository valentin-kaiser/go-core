package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/logging"
)

var logger = logging.GetPackageLogger("queue")

// TaskFunc represents a task function that can be executed
type TaskFunc func(ctx context.Context) error

// TaskType represents the type of task scheduling
type TaskType int

const (
	// TaskTypeCron represents cron-based task scheduling
	TaskTypeCron TaskType = iota
	// TaskTypeInterval represents interval-based task scheduling
	TaskTypeInterval
)

// String returns the string representation of the task type
func (t TaskType) String() string {
	switch t {
	case TaskTypeCron:
		return "cron"
	case TaskTypeInterval:
		return "interval"
	default:
		return "unknown"
	}
}

// Task represents a scheduled task
type Task struct {
	ID                  string        `json:"id"`
	Name                string        `json:"name"`
	Type                TaskType      `json:"type"`
	CronSpec            string        `json:"cron_spec,omitempty"`
	Interval            time.Duration `json:"interval,omitempty"`
	Function            TaskFunc      `json:"-"`
	NextRun             time.Time     `json:"next_run"`
	LastRun             time.Time     `json:"last_run"`
	RunCount            int64         `json:"run_count"`
	ErrorCount          int64         `json:"error_count"`
	ConsecutiveFailures int64         `json:"consecutive_failures"`
	LastError           string        `json:"last_error,omitempty"`
	IsRunning           bool          `json:"is_running"`
	Quiet               bool          `json:"log_on_first_failure_only"`
	AllowConcurrent     bool          `json:"allow_concurrent"`
	MaxRetries          int           `json:"max_retries"`
	RetryDelay          time.Duration `json:"retry_delay"`
	Timeout             time.Duration `json:"timeout"`
	Enabled             bool          `json:"enabled"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
	mutex               sync.RWMutex  `json:"-"`
}

// TaskScheduler manages background tasks
type TaskScheduler struct {
	tasks          map[string]*Task
	tasksMutex     sync.RWMutex
	running        int32
	shutdownChan   chan struct{}
	workerWg       sync.WaitGroup
	checkInterval  time.Duration
	defaultTimeout time.Duration
	retryDelay     time.Duration
	cancel         context.CancelFunc
}

// NewTaskScheduler creates a new task scheduler with default settings
func NewTaskScheduler() *TaskScheduler {
	return &TaskScheduler{
		tasks:          make(map[string]*Task),
		shutdownChan:   make(chan struct{}),
		checkInterval:  time.Second * 10,
		defaultTimeout: time.Minute * 5,
		retryDelay:     time.Second * 5,
	}
}

// WithCheckInterval sets the interval for checking scheduled tasks
func (s *TaskScheduler) WithCheckInterval(interval time.Duration) *TaskScheduler {
	if interval > 0 {
		s.checkInterval = interval
	}
	return s
}

// WithDefaultTimeout sets the default timeout for task execution
func (s *TaskScheduler) WithDefaultTimeout(timeout time.Duration) *TaskScheduler {
	if timeout > 0 {
		s.defaultTimeout = timeout
	}
	return s
}

// WithRetryDelay sets the delay between retries
func (s *TaskScheduler) WithRetryDelay(delay time.Duration) *TaskScheduler {
	if delay > 0 {
		s.retryDelay = delay
	}
	return s
}

// RegisterCronTask registers a new cron-based task
func (s *TaskScheduler) RegisterCronTask(name, cronSpec string, fn TaskFunc) error {
	return s.RegisterCronTaskWithOptions(name, cronSpec, fn, TaskOptions{})
}

// RegisterIntervalTask registers a new interval-based task
func (s *TaskScheduler) RegisterIntervalTask(name string, interval time.Duration, fn TaskFunc) error {
	return s.RegisterIntervalTaskWithOptions(name, interval, fn, TaskOptions{
		Immediately: true,
	})
}

// TaskOptions provides configuration options for tasks
type TaskOptions struct {
	// MaxRetries specifies the maximum number of retries for a task default is 0 (no retries)
	MaxRetries int
	// RetryDelay specifies the delay between retries (default is 5 seconds)
	RetryDelay time.Duration
	// Timeout specifies the maximum duration for task execution (default is 5 minutes)
	Timeout time.Duration
	// Concurrent specifies whether the task can run concurrently (default is false)
	Concurrent bool
	// Immediately specifies whether the task should run immediately upon registration (default is false)
	Immediately bool
	// Quiet specifies whether to log only the first failure in a series of consecutive failures (default is false)
	Quiet bool
}

// RegisterCronTaskWithOptions registers a new cron-based task with options
func (s *TaskScheduler) RegisterCronTaskWithOptions(name, cronSpec string, fn TaskFunc, options TaskOptions) error {
	if name == "" {
		return apperror.NewError("task name cannot be empty")
	}
	if cronSpec == "" {
		return apperror.NewError("cron specification cannot be empty")
	}
	if fn == nil {
		return apperror.NewError("task function cannot be nil")
	}

	if err := s.ValidateCronSpec(cronSpec); err != nil {
		return apperror.NewError(fmt.Sprintf("invalid cron specification: %v", err))
	}

	s.tasksMutex.Lock()
	defer s.tasksMutex.Unlock()
	if _, exists := s.tasks[name]; exists {
		return apperror.NewError(fmt.Sprintf("task with name '%s' already exists", name))
	}

	retryDelay := s.retryDelay
	if options.RetryDelay > 0 {
		retryDelay = options.RetryDelay
	}

	timeout := s.defaultTimeout
	if options.Timeout > 0 {
		timeout = options.Timeout
	}
	task := &Task{
		ID:              generateTaskID(),
		Name:            name,
		Type:            TaskTypeCron,
		CronSpec:        cronSpec,
		Function:        fn,
		AllowConcurrent: options.Concurrent,
		MaxRetries:      options.MaxRetries,
		RetryDelay:      retryDelay,
		Timeout:         timeout,
		Quiet:           options.Quiet,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		Enabled:         true,
	}

	nextRun, err := s.calculateNextCronRun(cronSpec, time.Now())
	if err != nil {
		return apperror.NewError(fmt.Sprintf("failed to calculate next run time: %v", err))
	}

	task.NextRun = nextRun
	if options.Immediately {
		task.NextRun = time.Now()
	}
	s.tasks[name] = task

	logger.Debug().Fields(
		logging.F("task_name", name),
		logging.F("cron_spec", cronSpec),
		logging.F("next_run", task.NextRun),
	).Msg("cron task registered")

	return nil
}

// RegisterIntervalTaskWithOptions registers a new interval-based task with options
func (s *TaskScheduler) RegisterIntervalTaskWithOptions(name string, interval time.Duration, fn TaskFunc, options TaskOptions) error {
	if name == "" {
		return apperror.NewError("task name cannot be empty")
	}
	if interval <= 0 {
		return apperror.NewError("interval must be positive")
	}
	if fn == nil {
		return apperror.NewError("task function cannot be nil")
	}

	s.tasksMutex.Lock()
	defer s.tasksMutex.Unlock()

	if _, exists := s.tasks[name]; exists {
		return apperror.NewError(fmt.Sprintf("task with name '%s' already exists", name))
	}

	retryDelay := s.retryDelay
	if options.RetryDelay > 0 {
		retryDelay = options.RetryDelay
	}

	timeout := s.defaultTimeout
	if options.Timeout > 0 {
		timeout = options.Timeout
	}

	task := &Task{
		ID:              generateTaskID(),
		Name:            name,
		Type:            TaskTypeInterval,
		Interval:        interval,
		Function:        fn,
		AllowConcurrent: options.Concurrent,
		MaxRetries:      options.MaxRetries,
		RetryDelay:      retryDelay,
		Timeout:         timeout,
		Quiet:           options.Quiet,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		Enabled:         true,
	}

	task.NextRun = time.Now().Add(interval)
	if options.Immediately {
		task.NextRun = time.Now()
	}

	s.tasks[name] = task

	logger.Debug().Fields(
		logging.F("task_name", name),
		logging.F("interval", interval),
		logging.F("next_run", task.NextRun),
	).Msg("interval task registered")

	return nil
}

// RegisterOrRescheduleCronTask registers a new cron-based task or reschedules an existing one
func (s *TaskScheduler) RegisterOrRescheduleCronTask(name, cronSpec string, fn TaskFunc) error {
	return s.RegisterOrRescheduleCronTaskWithOptions(name, cronSpec, fn, TaskOptions{})
}

// RegisterOrRescheduleIntervalTask registers a new interval-based task or reschedules an existing one
func (s *TaskScheduler) RegisterOrRescheduleIntervalTask(name string, interval time.Duration, fn TaskFunc) error {
	return s.RegisterOrRescheduleIntervalTaskWithOptions(name, interval, fn, TaskOptions{})
}

// RegisterOrRescheduleCronTaskWithOptions registers a new cron-based task or reschedules an existing one with options
func (s *TaskScheduler) RegisterOrRescheduleCronTaskWithOptions(name, cronSpec string, fn TaskFunc, options TaskOptions) error {
	if name == "" {
		return apperror.NewError("task name cannot be empty")
	}
	if cronSpec == "" {
		return apperror.NewError("cron specification cannot be empty")
	}
	if fn == nil {
		return apperror.NewError("task function cannot be nil")
	}

	if err := s.ValidateCronSpec(cronSpec); err != nil {
		return apperror.NewError(fmt.Sprintf("invalid cron specification: %v", err))
	}

	s.tasksMutex.Lock()
	defer s.tasksMutex.Unlock()

	existingTask, exists := s.tasks[name]
	if exists {
		// Task exists, reschedule it
		nextRun, err := s.calculateNextCronRun(cronSpec, time.Now())
		if err != nil {
			return apperror.NewError(fmt.Sprintf("failed to calculate next run time: %v", err))
		}

		existingTask.mutex.Lock()
		existingTask.Type = TaskTypeCron
		existingTask.CronSpec = cronSpec
		existingTask.Interval = 0
		existingTask.Function = fn
		existingTask.NextRun = nextRun
		existingTask.UpdatedAt = time.Now()

		// Update options if provided
		if options.MaxRetries >= 0 { // Allow explicit 0 to disable retries
			existingTask.MaxRetries = options.MaxRetries
		}
		if options.RetryDelay > 0 {
			existingTask.RetryDelay = options.RetryDelay
		}
		if options.Timeout > 0 {
			existingTask.Timeout = options.Timeout
		}
		existingTask.AllowConcurrent = options.Concurrent
		existingTask.Quiet = options.Quiet
		nextRunForLog := existingTask.NextRun
		existingTask.mutex.Unlock()

		logger.Trace().
			Field("task_name", name).
			Field("cron_spec", cronSpec).
			Field("next_run", nextRunForLog).
			Msg("existing cron task rescheduled")

		return nil
	}

	retryDelay := s.retryDelay
	if options.RetryDelay > 0 {
		retryDelay = options.RetryDelay
	}

	timeout := s.defaultTimeout
	if options.Timeout > 0 {
		timeout = options.Timeout
	}

	task := &Task{
		ID:              generateTaskID(),
		Name:            name,
		Type:            TaskTypeCron,
		CronSpec:        cronSpec,
		Function:        fn,
		AllowConcurrent: options.Concurrent,
		MaxRetries:      options.MaxRetries,
		RetryDelay:      retryDelay,
		Timeout:         timeout,
		Quiet:           options.Quiet,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		Enabled:         true,
	}

	nextRun, err := s.calculateNextCronRun(cronSpec, time.Now())
	if err != nil {
		return apperror.NewError(fmt.Sprintf("failed to calculate next run time: %v", err))
	}

	task.NextRun = nextRun
	if options.Immediately {
		task.NextRun = time.Now()
	}
	s.tasks[name] = task

	logger.Debug().
		Field("task_name", name).
		Field("cron_spec", cronSpec).
		Field("next_run", task.NextRun).
		Msg("new cron task registered")

	return nil
}

// RegisterOrRescheduleIntervalTaskWithOptions registers a new interval-based task or reschedules an existing one with options
func (s *TaskScheduler) RegisterOrRescheduleIntervalTaskWithOptions(name string, interval time.Duration, fn TaskFunc, options TaskOptions) error {
	if name == "" {
		return apperror.NewError("task name cannot be empty")
	}
	if interval <= 0 {
		return apperror.NewError("interval must be positive")
	}
	if fn == nil {
		return apperror.NewError("task function cannot be nil")
	}

	s.tasksMutex.Lock()
	defer s.tasksMutex.Unlock()

	existingTask, exists := s.tasks[name]
	if exists {
		existingTask.mutex.Lock()
		existingTask.Type = TaskTypeInterval
		existingTask.CronSpec = "" // Clear cron spec for interval tasks
		existingTask.Interval = interval
		existingTask.Function = fn
		existingTask.NextRun = time.Now().Add(interval)
		existingTask.UpdatedAt = time.Now()

		if options.MaxRetries >= 0 { // Allow explicit 0 to disable retries
			existingTask.MaxRetries = options.MaxRetries
		}
		if options.RetryDelay > 0 {
			existingTask.RetryDelay = options.RetryDelay
		}
		if options.Timeout > 0 {
			existingTask.Timeout = options.Timeout
		}
		existingTask.AllowConcurrent = options.Concurrent
		existingTask.Quiet = options.Quiet
		nextRunForLog := existingTask.NextRun
		existingTask.mutex.Unlock()

		logger.Trace().
			Field("task_name", name).
			Field("interval", interval).
			Field("next_run", nextRunForLog).
			Msg("existing interval task rescheduled")

		return nil
	}

	retryDelay := s.retryDelay
	if options.RetryDelay > 0 {
		retryDelay = options.RetryDelay
	}

	timeout := s.defaultTimeout
	if options.Timeout > 0 {
		timeout = options.Timeout
	}

	task := &Task{
		ID:              generateTaskID(),
		Name:            name,
		Type:            TaskTypeInterval,
		Interval:        interval,
		Function:        fn,
		AllowConcurrent: options.Concurrent,
		MaxRetries:      options.MaxRetries,
		RetryDelay:      retryDelay,
		Timeout:         timeout,
		Quiet:           options.Quiet,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
		Enabled:         true,
	}

	task.NextRun = time.Now().Add(interval)
	if options.Immediately {
		task.NextRun = time.Now()
	}

	s.tasks[name] = task

	logger.Debug().
		Field("task_name", name).
		Field("interval", interval).
		Field("next_run", task.NextRun).
		Msg("new interval task registered")

	return nil
}

// Start starts the task scheduler
func (s *TaskScheduler) Start(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		return apperror.NewError("task scheduler is already running")
	}

	ctx, s.cancel = context.WithCancel(ctx)

	s.workerWg.Add(1)
	go s.schedulerLoop(ctx)

	logger.Debug().Msg("task scheduler started")
	return nil
}

// Stop stops the task scheduler
func (s *TaskScheduler) Stop() {
	if !atomic.CompareAndSwapInt32(&s.running, 1, 0) {
		return
	}

	logger.Debug().Msg("stopping task scheduler...")
	if s.cancel != nil {
		s.cancel()
	}

	close(s.shutdownChan)
	s.workerWg.Wait()

	logger.Debug().Msg("task scheduler stopped")
}

// schedulerLoop is the main scheduler loop
func (s *TaskScheduler) schedulerLoop(ctx context.Context) {
	defer s.workerWg.Done()

	ticker := time.NewTicker(s.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.shutdownChan:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkAndRunTasks(ctx)
		}
	}
}

// checkAndRunTasks checks for tasks that need to be executed and runs them
func (s *TaskScheduler) checkAndRunTasks(ctx context.Context) {
	s.tasksMutex.RLock()
	var tasksToRun []*Task
	now := time.Now()

	for _, task := range s.tasks {
		task.mutex.RLock()
		enabled := task.Enabled
		nextRun := task.NextRun
		isRunning := task.IsRunning
		allowConcurrent := task.AllowConcurrent
		task.mutex.RUnlock()

		// Run task if it's enabled, scheduled to run, and either not running or concurrent execution is allowed
		if enabled && now.After(nextRun) && (!isRunning || allowConcurrent) {
			tasksToRun = append(tasksToRun, task)
		}
	}
	s.tasksMutex.RUnlock()

	for _, task := range tasksToRun {
		s.workerWg.Add(1)
		go s.runTask(ctx, task)
	}
}

// runTask executes a single task
func (s *TaskScheduler) runTask(ctx context.Context, task *Task) {
	defer s.workerWg.Done()

	// For concurrent tasks, update next run time immediately so next instance can be scheduled
	// For non-concurrent tasks, set running state to prevent overlapping executions
	if task.AllowConcurrent {
		err := s.updateNextRun(task)
		if err != nil {
			logger.Error().
				Err(err).
				Field("task_name", task.Name).
				Msg("failed to update next run time before execution")
		}
	}

	if !task.AllowConcurrent {
		task.mutex.Lock()
		task.IsRunning = true
		task.UpdatedAt = time.Now()
		task.mutex.Unlock()
	}

	if task.AllowConcurrent {
		task.mutex.Lock()
		task.UpdatedAt = time.Now()
		task.mutex.Unlock()
	}

	taskCtx, cancel := context.WithTimeout(ctx, task.Timeout)
	defer cancel()

	var lastError error
	for attempt := 0; attempt <= task.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		logger.Trace().
			Field("task_name", task.Name).
			Field("attempt", attempt+1).
			Field("max_retries", task.MaxRetries+1).
			Msg("executing task")

		err := task.Function(taskCtx)

		if err == nil {
			task.mutex.Lock()
			// Only mark as not running for non-concurrent tasks
			if !task.AllowConcurrent {
				task.IsRunning = false
			}
			task.LastRun = time.Now()
			task.RunCount++
			task.ConsecutiveFailures = 0 // Reset consecutive failures on success
			task.LastError = ""
			task.UpdatedAt = time.Now()

			// For non-concurrent tasks, update next run time after completion
			// For concurrent tasks, this was already done at the start
			var nextRunTime time.Time
			var runCount int64
			if !task.AllowConcurrent {
				task.mutex.Unlock()
				err = s.updateNextRun(task)
				if err != nil {
					logger.Error().
						Err(err).
						Field("task_name", task.Name).
						Msg("failed to update next run time")
				}
				// Read the values for logging after update
				task.mutex.RLock()
				nextRunTime = task.NextRun
				runCount = task.RunCount
				task.mutex.RUnlock()
			}

			if task.AllowConcurrent {
				nextRunTime = task.NextRun
				runCount = task.RunCount
				task.mutex.Unlock()
			}

			logger.Trace().
				Field("task_name", task.Name).
				Field("run_count", runCount).
				Field("next_run", nextRunTime).
				Msg("task executed successfully")
			return
		}

		lastError = err
		if !task.Quiet || task.ConsecutiveFailures == 0 && task.MaxRetries > 0 {
			logger.Warn().
				Err(err).
				Field("task_name", task.Name).
				Field("attempt", attempt+1).
				Msg("task execution failed")
		}

		if attempt < task.MaxRetries {
			select {
			case <-taskCtx.Done():
				return
			case <-time.After(task.RetryDelay):
			}
		}
	}

	// Handle failure case after all retries exhausted
	task.mutex.Lock()

	// For non-concurrent tasks, update next run time after completion
	// For concurrent tasks, this was already done at the start
	if !task.AllowConcurrent {
		task.IsRunning = false
	}
	task.ErrorCount++
	task.LastRun = time.Now()
	task.ConsecutiveFailures++ // Increment consecutive failures
	task.LastError = lastError.Error()
	task.UpdatedAt = time.Now()

	// Capture values for logging before updating next run
	errorCount := task.ErrorCount
	consecutiveFailures := task.ConsecutiveFailures
	var nextRunTime time.Time

	if !task.AllowConcurrent {
		task.mutex.Unlock()
		err := s.updateNextRun(task)
		if err != nil {
			logger.Error().
				Err(err).
				Field("task_name", task.Name).
				Msg("failed to update next run time after retries")
		}
		// Read next run time for logging
		task.mutex.RLock()
		nextRunTime = task.NextRun
		task.mutex.RUnlock()
	}

	if task.AllowConcurrent {
		nextRunTime = task.NextRun
		task.mutex.Unlock()
	}

	if !task.Quiet || consecutiveFailures == 1 {
		logger.Error().
			Err(lastError).
			Field("task_name", task.Name).
			Field("error_count", errorCount).
			Field("next_run", nextRunTime).
			Msg("task execution failed")
	}
}

func (s *TaskScheduler) updateNextRun(task *Task) error {
	task.mutex.Lock()
	defer task.mutex.Unlock()

	switch task.Type {
	case TaskTypeCron:
		nextRun, err := s.calculateNextCronRun(task.CronSpec, time.Now())
		if err != nil {
			return fmt.Errorf("failed to calculate next run time: %w", err)
		}
		task.NextRun = nextRun
	case TaskTypeInterval:
		task.NextRun = time.Now().Add(task.Interval)
	}
	return nil
}

// GetTask returns a task by name
func (s *TaskScheduler) GetTask(name string) (*Task, error) {
	s.tasksMutex.RLock()
	task, exists := s.tasks[name]
	s.tasksMutex.RUnlock()

	if !exists {
		return nil, apperror.NewError(fmt.Sprintf("task '%s' not found", name))
	}

	task.mutex.RLock()
	defer task.mutex.RUnlock()
	return &Task{
		ID:                  task.ID,
		Name:                task.Name,
		Type:                task.Type,
		CronSpec:            task.CronSpec,
		Interval:            task.Interval,
		Function:            task.Function,
		NextRun:             task.NextRun,
		LastRun:             task.LastRun,
		RunCount:            task.RunCount,
		ErrorCount:          task.ErrorCount,
		ConsecutiveFailures: task.ConsecutiveFailures,
		LastError:           task.LastError,
		IsRunning:           task.IsRunning,
		Quiet:               task.Quiet,
		AllowConcurrent:     task.AllowConcurrent,
		MaxRetries:          task.MaxRetries,
		RetryDelay:          task.RetryDelay,
		Timeout:             task.Timeout,
		Enabled:             task.Enabled,
		CreatedAt:           task.CreatedAt,
		UpdatedAt:           task.UpdatedAt,
		// Note: mutex is intentionally not copied
	}, nil
}

// GetTasks returns all registered tasks
func (s *TaskScheduler) GetTasks() map[string]*Task {
	s.tasksMutex.RLock()
	defer s.tasksMutex.RUnlock()

	tasks := make(map[string]*Task, len(s.tasks))
	for name, task := range s.tasks {
		// Create a safe copy of each task with proper locking
		task.mutex.RLock()
		taskCopy := Task{
			ID:                  task.ID,
			Name:                task.Name,
			Type:                task.Type,
			CronSpec:            task.CronSpec,
			Interval:            task.Interval,
			Function:            task.Function,
			NextRun:             task.NextRun,
			LastRun:             task.LastRun,
			RunCount:            task.RunCount,
			ErrorCount:          task.ErrorCount,
			ConsecutiveFailures: task.ConsecutiveFailures,
			LastError:           task.LastError,
			IsRunning:           task.IsRunning,
			Quiet:               task.Quiet,
			AllowConcurrent:     task.AllowConcurrent,
			MaxRetries:          task.MaxRetries,
			RetryDelay:          task.RetryDelay,
			Timeout:             task.Timeout,
			Enabled:             task.Enabled,
			CreatedAt:           task.CreatedAt,
			UpdatedAt:           task.UpdatedAt,
			// Note: mutex is intentionally not copied
		}
		task.mutex.RUnlock()

		tasks[name] = &taskCopy
	}

	return tasks
}

// EnableTask enables a task
func (s *TaskScheduler) EnableTask(name string) error {
	s.tasksMutex.Lock()
	defer s.tasksMutex.Unlock()

	task, exists := s.tasks[name]
	if !exists {
		return apperror.NewError(fmt.Sprintf("task '%s' not found", name))
	}

	task.Enabled = true
	task.UpdatedAt = time.Now()

	logger.Debug().
		Field("task_name", name).
		Msg("task enabled")

	return nil
}

// DisableTask disables a task
func (s *TaskScheduler) DisableTask(name string) error {
	s.tasksMutex.Lock()
	defer s.tasksMutex.Unlock()

	task, exists := s.tasks[name]
	if !exists {
		return apperror.NewError(fmt.Sprintf("task '%s' not found", name))
	}

	task.Enabled = false
	task.UpdatedAt = time.Now()

	logger.Debug().
		Field("task_name", name).
		Msg("task disabled")

	return nil
}

// RemoveTask removes a task from the scheduler
func (s *TaskScheduler) RemoveTask(name string) error {
	s.tasksMutex.Lock()
	defer s.tasksMutex.Unlock()

	task, exists := s.tasks[name]
	if !exists {
		return apperror.NewError(fmt.Sprintf("task '%s' not found", name))
	}

	if task.IsRunning {
		return apperror.NewError(fmt.Sprintf("cannot remove running task '%s'", name))
	}

	delete(s.tasks, name)

	logger.Debug().
		Field("task_name", name).
		Msg("task removed")

	return nil
}

// RescheduleTaskWithCron reschedules an existing task with a new cron specification
func (s *TaskScheduler) RescheduleTaskWithCron(name, cronSpec string) error {
	if name == "" {
		return apperror.NewError("task name cannot be empty")
	}
	if cronSpec == "" {
		return apperror.NewError("cron specification cannot be empty")
	}

	if err := s.ValidateCronSpec(cronSpec); err != nil {
		return apperror.NewError(fmt.Sprintf("invalid cron specification: %v", err))
	}

	s.tasksMutex.Lock()
	defer s.tasksMutex.Unlock()

	task, exists := s.tasks[name]
	if !exists {
		return apperror.NewError(fmt.Sprintf("task '%s' not found", name))
	}

	nextRun, err := s.calculateNextCronRun(cronSpec, time.Now())
	if err != nil {
		return apperror.NewError(fmt.Sprintf("failed to calculate next run time: %v", err))
	}

	task.Type = TaskTypeCron
	task.CronSpec = cronSpec
	task.Interval = 0 // Clear interval for cron tasks
	task.NextRun = nextRun
	task.UpdatedAt = time.Now()

	logger.Trace().
		Field("task_name", name).
		Field("cron_spec", cronSpec).
		Field("next_run", nextRun).
		Msg("task rescheduled with cron specification")

	return nil
}

// RescheduleTaskWithInterval reschedules an existing task with a new interval
func (s *TaskScheduler) RescheduleTaskWithInterval(name string, interval time.Duration) error {
	if name == "" {
		return apperror.NewError("task name cannot be empty")
	}
	if interval <= 0 {
		return apperror.NewError("interval must be positive")
	}

	s.tasksMutex.Lock()
	defer s.tasksMutex.Unlock()

	task, exists := s.tasks[name]
	if !exists {
		return apperror.NewError(fmt.Sprintf("task '%s' not found", name))
	}

	task.Type = TaskTypeInterval
	task.CronSpec = "" // Clear cron spec for interval tasks
	task.Interval = interval
	task.NextRun = time.Now().Add(interval)
	task.UpdatedAt = time.Now()

	logger.Trace().
		Field("task_name", name).
		Field("interval", interval).
		Field("next_run", task.NextRun).
		Msg("task rescheduled with interval")

	return nil
}

// IsRunning returns true if the scheduler is running
func (s *TaskScheduler) IsRunning() bool {
	return atomic.LoadInt32(&s.running) == 1
}

// generateTaskID generates a unique task ID
func generateTaskID() string {
	return uuid.New().String()
}
