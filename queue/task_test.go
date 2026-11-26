package queue_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/internal/await"
	"github.com/valentin-kaiser/go-core/v2/queue"
)

const (
	// taskWaitTimeout bounds every wait for a scheduler run, so a missed run fails instead of hanging.
	taskWaitTimeout   = 5 * time.Second
	taskContextLimit  = 5 * time.Second
	taskCheckFast     = 50 * time.Millisecond
	taskCheckStandard = 100 * time.Millisecond
	taskRetryDelay    = 30 * time.Millisecond
	taskSchedulerWait = 10 * time.Second

	everyFiveMinutesSpec = "*/5 * * * *"
	everySecondSpec      = "* * * * * *"
)

func TestTaskScheduler_RegisterIntervalTask(t *testing.T) {
	const taskName = "test-task"

	scheduler := queue.NewTaskScheduler()

	var executed atomic.Int32
	taskFunc := func(_ context.Context) error {
		executed.Add(1)
		return nil
	}

	err := scheduler.RegisterIntervalTask(taskName, time.Second, taskFunc)
	if err != nil {
		t.Fatalf("failed to register interval task: %v", err)
	}

	task, err := scheduler.Task(taskName)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if task.Name != taskName {
		t.Errorf("expected task name 'test-task', got '%s'", task.Name)
	}

	if task.Type != queue.TaskTypeInterval {
		t.Errorf("expected task type interval, got %v", task.Type)
	}

	if task.Interval != time.Second {
		t.Errorf("expected interval 1s, got %v", task.Interval)
	}

	if !task.Enabled {
		t.Error("expected task to be enabled")
	}

	// Test duplicate registration
	err = scheduler.RegisterIntervalTask(taskName, time.Second, taskFunc)
	if err == nil {
		t.Error("expected error when registering duplicate task")
	}
}

func TestTaskScheduler_RegisterCronTask(t *testing.T) {
	scheduler := queue.NewTaskScheduler()

	var executed atomic.Int32
	taskFunc := func(_ context.Context) error {
		executed.Add(1)
		return nil
	}

	// Test valid cron expression
	err := scheduler.RegisterCronTask("test-cron", everyFiveMinutesSpec, taskFunc)
	if err != nil {
		t.Fatalf("failed to register cron task: %v", err)
	}

	task, err := scheduler.Task("test-cron")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if task.Type != queue.TaskTypeCron {
		t.Errorf("expected task type cron, got %v", task.Type)
	}

	if task.CronSpec != everyFiveMinutesSpec {
		t.Errorf("expected cron spec '*/5 * * * *', got '%s'", task.CronSpec)
	}

	// Test invalid cron expression
	err = scheduler.RegisterCronTask("invalid-cron", "invalid", taskFunc)
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}

	// Test empty name
	err = scheduler.RegisterCronTask("", everyFiveMinutesSpec, taskFunc)
	if err == nil {
		t.Error("expected error for empty task name")
	}

	// Test nil function
	err = scheduler.RegisterCronTask("nil-func", everyFiveMinutesSpec, nil)
	if err == nil {
		t.Error("expected error for nil function")
	}
}

func TestTaskScheduler_StartStop(t *testing.T) {
	scheduler := queue.NewTaskScheduler()

	ctx, cancel := context.WithTimeout(t.Context(), taskContextLimit)
	defer cancel()

	// Test start
	err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	if !scheduler.IsRunning() {
		t.Error("expected scheduler to be running")
	}

	// Test double start
	err = scheduler.Start(ctx)
	if err == nil {
		t.Error("expected error when starting already running scheduler")
	}

	// Test stop
	scheduler.Stop()

	if scheduler.IsRunning() {
		t.Error("expected scheduler to be stopped")
	}

	// Test double stop (should not panic)
	scheduler.Stop()
}

func TestTaskScheduler_IntervalTaskExecution(t *testing.T) {
	const (
		interval       = 300 * time.Millisecond
		minInterval    = 200 * time.Millisecond
		maxInterval    = 500 * time.Millisecond
		wantExecutions = 3
	)

	scheduler := queue.NewTaskScheduler().WithCheckInterval(taskCheckStandard)

	var executed atomic.Int32
	var executionTimes []time.Time
	var mu sync.Mutex

	taskFunc := func(_ context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		executed.Add(1)
		executionTimes = append(executionTimes, time.Now())
		return nil
	}

	err := scheduler.RegisterIntervalTask("test-interval", interval, taskFunc)
	if err != nil {
		t.Fatalf("failed to register interval task: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), taskContextLimit)
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	await.Until(t, taskWaitTimeout, func() bool { return executed.Load() >= wantExecutions }, "the interval task to run three times")
	scheduler.Stop()

	execCount := executed.Load()
	if execCount < 2 {
		t.Errorf("expected at least 2 executions, got %d", execCount)
	}

	// Check that task was executed at reasonable intervals
	mu.Lock()
	defer mu.Unlock()

	for i := 1; i < len(executionTimes); i++ {
		gap := executionTimes[i].Sub(executionTimes[i-1])
		if gap < minInterval || gap > maxInterval {
			t.Errorf("unexpected interval between executions: %v", gap)
		}
	}
}

func TestTaskScheduler_TaskRetry(t *testing.T) {
	const taskName = "retry-task"

	scheduler := queue.NewTaskScheduler().
		WithCheckInterval(taskCheckFast).
		WithRetryDelay(taskCheckFast)

	var attempts atomic.Int32
	var successCount atomic.Int32
	var lastError error

	taskFunc := func(_ context.Context) error {
		count := attempts.Add(1)
		if count < 3 {
			lastError = fmt.Errorf("attempt %d failed", count)
			return lastError
		}
		successCount.Add(1)
		return nil
	}

	err := scheduler.RegisterIntervalTaskWithOptions(taskName, 10*time.Second, taskFunc, queue.TaskOptions{
		MaxRetries:  3,
		RetryDelay:  taskRetryDelay,
		Immediately: true, // Run immediately for this test
	})
	if err != nil {
		t.Fatalf("failed to register task: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), taskContextLimit)
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait for task to be executed and retried
	await.Until(t, taskWaitTimeout, func() bool {
		task, err := scheduler.Task(taskName)
		return err == nil && task.RunCount >= 1
	}, "the task to succeed after its retries")
	scheduler.Stop()

	// The task should execute and succeed after 3 attempts
	// It should only succeed once in the time window since interval is 10 seconds
	attemptCount := attempts.Load()
	if attemptCount < 3 {
		t.Errorf("expected at least 3 attempts, got %d", attemptCount)
	}

	successCountValue := successCount.Load()
	if successCountValue < 1 {
		t.Errorf("expected at least 1 success, got %d", successCountValue)
	}

	// Check task stats
	task, err := scheduler.Task(taskName)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if task.RunCount < 1 {
		t.Errorf("expected run count at least 1, got %d", task.RunCount)
	}

	if task.ErrorCount != 0 {
		t.Errorf("expected error count 0, got %d", task.ErrorCount)
	}

	if task.LastError != "" {
		t.Errorf("expected no last error, got '%s'", task.LastError)
	}
}

func TestTaskScheduler_TaskFailure(t *testing.T) {
	const taskName = "failing-task"

	scheduler := queue.NewTaskScheduler().
		WithCheckInterval(taskCheckFast).
		WithRetryDelay(taskRetryDelay)

	var attempts atomic.Int32
	expectedError := errors.New("task always fails")

	taskFunc := func(_ context.Context) error {
		attempts.Add(1)
		return expectedError
	}

	err := scheduler.RegisterIntervalTaskWithOptions(taskName, 5*time.Second, taskFunc, queue.TaskOptions{
		MaxRetries:  2,
		RetryDelay:  taskRetryDelay,
		Immediately: true, // Run immediately for this test
	})
	if err != nil {
		t.Fatalf("failed to register task: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), taskContextLimit)
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait for task to fail completely
	await.Until(t, taskWaitTimeout, func() bool {
		task, err := scheduler.Task(taskName)
		return err == nil && task.ErrorCount >= 1
	}, "the task to fail after its retries")
	scheduler.Stop()

	attemptCount := attempts.Load()
	if attemptCount < 3 { // At least 1 initial + 2 retries
		t.Errorf("expected at least 3 attempts, got %d", attemptCount)
	}

	// Check task stats
	task, err := scheduler.Task(taskName)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if task.RunCount != 0 {
		t.Errorf("expected run count 0, got %d", task.RunCount)
	}

	if task.ErrorCount < 1 {
		t.Errorf("expected error count at least 1, got %d", task.ErrorCount)
	}

	if task.LastError != expectedError.Error() {
		t.Errorf("expected last error '%s', got '%s'", expectedError.Error(), task.LastError)
	}
}

func TestTaskScheduler_EnableDisableTask(t *testing.T) {
	const (
		taskName    = "toggle-task"
		missingTask = "non-existent"
	)

	scheduler := queue.NewTaskScheduler()

	taskFunc := func(_ context.Context) error {
		return nil
	}

	err := scheduler.RegisterIntervalTask(taskName, time.Second, taskFunc)
	if err != nil {
		t.Fatalf("failed to register task: %v", err)
	}

	// Test disable
	err = scheduler.DisableTask(taskName)
	if err != nil {
		t.Fatalf("failed to disable task: %v", err)
	}

	task, err := scheduler.Task(taskName)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if task.Enabled {
		t.Error("expected task to be disabled")
	}

	// Test enable
	err = scheduler.EnableTask(taskName)
	if err != nil {
		t.Fatalf("failed to enable task: %v", err)
	}

	task, err = scheduler.Task(taskName)
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if !task.Enabled {
		t.Error("expected task to be enabled")
	}

	// Test non-existent task
	err = scheduler.EnableTask(missingTask)
	if err == nil {
		t.Error("expected error when enabling non-existent task")
	}

	err = scheduler.DisableTask(missingTask)
	if err == nil {
		t.Error("expected error when disabling non-existent task")
	}
}

func TestTaskScheduler_RemoveTask(t *testing.T) {
	const (
		taskName    = "remove-task"
		missingTask = "non-existent"
	)

	scheduler := queue.NewTaskScheduler()

	taskFunc := func(_ context.Context) error {
		return nil
	}

	err := scheduler.RegisterIntervalTask(taskName, time.Second, taskFunc)
	if err != nil {
		t.Fatalf("failed to register task: %v", err)
	}

	// Test removal
	err = scheduler.RemoveTask(taskName)
	if err != nil {
		t.Fatalf("failed to remove task: %v", err)
	}

	// Check that task is gone
	_, err = scheduler.Task(taskName)
	if err == nil {
		t.Error("expected error when getting removed task")
	}

	// Test removing non-existent task
	err = scheduler.RemoveTask(missingTask)
	if err == nil {
		t.Error("expected error when removing non-existent task")
	}
}

func TestTaskScheduler_GetTasks(t *testing.T) {
	scheduler := queue.NewTaskScheduler()

	taskFunc := func(_ context.Context) error {
		return nil
	}

	// Register multiple tasks
	err := scheduler.RegisterIntervalTask("task1", time.Second, taskFunc)
	if err != nil {
		t.Fatalf("failed to register task1: %v", err)
	}

	err = scheduler.RegisterIntervalTask("task2", time.Second*2, taskFunc)
	if err != nil {
		t.Fatalf("failed to register task2: %v", err)
	}

	err = scheduler.RegisterCronTask("task3", "0 0 * * *", taskFunc)
	if err != nil {
		t.Fatalf("failed to register task3: %v", err)
	}

	tasks := scheduler.Tasks()

	if len(tasks) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(tasks))
	}

	expectedTasks := []string{"task1", "task2", "task3"}
	for _, name := range expectedTasks {
		if _, exists := tasks[name]; !exists {
			t.Errorf("expected task '%s' to exist", name)
		}
	}

	// Check that returned tasks are copies (not references)
	originalTask := tasks["task1"]
	originalTask.Enabled = false

	task, err := scheduler.Task("task1")
	if err != nil {
		t.Fatalf("failed to get task1: %v", err)
	}

	if !task.Enabled {
		t.Error("modifying returned task affected original task")
	}
}

func TestTaskScheduler_ConcurrentExecution(t *testing.T) {
	const taskInterval = 150 * time.Millisecond

	scheduler := queue.NewTaskScheduler().WithCheckInterval(taskCheckStandard)

	var executionCount atomic.Int32
	var concurrentCount atomic.Int32
	var maxConcurrent atomic.Int32
	release := make(chan struct{})

	taskFunc := func(ctx context.Context) error {
		// Track concurrent executions
		current := concurrentCount.Add(1)
		for {
			existing := maxConcurrent.Load()
			if current <= existing || maxConcurrent.CompareAndSwap(existing, current) {
				break
			}
		}

		// Simulate work: every run stays active until the test has seen the overlap
		select {
		case <-release:
		case <-ctx.Done():
		}

		executionCount.Add(1)
		concurrentCount.Add(-1)
		return nil
	}

	// Test non-concurrent task (default behavior)
	err := scheduler.RegisterIntervalTask("non-concurrent", taskInterval, taskFunc)
	if err != nil {
		t.Fatalf("failed to register non-concurrent task: %v", err)
	}

	// Reset counters
	executionCount.Store(0)
	concurrentCount.Store(0)
	maxConcurrent.Store(0)

	// Test concurrent task
	err = scheduler.RegisterIntervalTaskWithOptions("concurrent", taskInterval, taskFunc, queue.TaskOptions{
		Concurrent: true,
	})
	if err != nil {
		t.Fatalf("failed to register concurrent task: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait for the executions to overlap, then let them finish
	await.Until(t, taskWaitTimeout, func() bool { return maxConcurrent.Load() >= 2 }, "two overlapping executions")
	close(release)
	await.Until(t, taskWaitTimeout, func() bool { return executionCount.Load() >= 2 }, "two finished executions")
	cancel()
	scheduler.Stop()

	// Verify the concurrent task had overlapping executions
	finalExecutionCount := executionCount.Load()
	finalMaxConcurrent := maxConcurrent.Load()

	t.Logf("Total executions: %d, Max concurrent: %d", finalExecutionCount, finalMaxConcurrent)

	if finalExecutionCount < 2 {
		t.Errorf("expected at least 2 executions, got %d", finalExecutionCount)
	}

	if finalMaxConcurrent < 2 {
		t.Errorf("expected at least 2 concurrent executions, got %d", finalMaxConcurrent)
	}

	// Verify task properties
	task, err := scheduler.Task("concurrent")
	if err != nil {
		t.Fatalf("failed to get concurrent task: %v", err)
	}

	if !task.AllowConcurrent {
		t.Error("expected AllowConcurrent to be true")
	}

	task, err = scheduler.Task("non-concurrent")
	if err != nil {
		t.Fatalf("failed to get non-concurrent task: %v", err)
	}

	if task.AllowConcurrent {
		t.Error("expected AllowConcurrent to be false")
	}
}

func TestTaskScheduler_ConcurrentCronExecution(t *testing.T) {
	scheduler := queue.NewTaskScheduler().WithCheckInterval(taskCheckFast)

	var executionCount atomic.Int32
	var concurrentCount atomic.Int32
	var maxConcurrent atomic.Int32
	release := make(chan struct{})

	taskFunc := func(ctx context.Context) error {
		// Track concurrent executions
		current := concurrentCount.Add(1)
		for {
			existing := maxConcurrent.Load()
			if current <= existing || maxConcurrent.CompareAndSwap(existing, current) {
				break
			}
		}

		// Simulate work: every run stays active until the test has seen the overlap
		select {
		case <-release:
		case <-ctx.Done():
		}

		executionCount.Add(1)
		concurrentCount.Add(-1)
		return nil
	}

	// Test concurrent cron task that runs every second
	err := scheduler.RegisterCronTaskWithOptions("concurrent-cron", everySecondSpec, taskFunc, queue.TaskOptions{
		Concurrent: true,
	})
	if err != nil {
		t.Fatalf("failed to register concurrent cron task: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// The cron fires on whole seconds, so two overlapping runs need a few seconds of real time
	await.Until(t, taskSchedulerWait, func() bool { return maxConcurrent.Load() >= 2 }, "two overlapping cron executions")
	close(release)
	await.Until(t, taskWaitTimeout, func() bool { return executionCount.Load() >= 2 }, "two finished cron executions")
	cancel()
	scheduler.Stop()

	// Verify the concurrent task had overlapping executions
	finalExecutionCount := executionCount.Load()
	finalMaxConcurrent := maxConcurrent.Load()

	t.Logf("Total cron executions: %d, Max concurrent: %d", finalExecutionCount, finalMaxConcurrent)

	if finalExecutionCount < 2 {
		t.Errorf("expected at least 2 executions, got %d", finalExecutionCount)
	}

	if finalMaxConcurrent < 2 {
		t.Errorf("expected at least 2 concurrent executions, got %d", finalMaxConcurrent)
	}

	// Verify task properties
	task, err := scheduler.Task("concurrent-cron")
	if err != nil {
		t.Fatalf("failed to get concurrent cron task: %v", err)
	}

	if !task.AllowConcurrent {
		t.Error("expected AllowConcurrent to be true")
	}

	if task.Type != queue.TaskTypeCron {
		t.Errorf("expected task type cron, got %v", task.Type)
	}
}

// registerImmediateTasks registers two cron and two interval tasks that differ in Immediately.
func registerImmediateTasks(t *testing.T, scheduler *queue.TaskScheduler, cronCount, intervalCount *atomic.Int32) {
	t.Helper()

	const intervalTaskPeriod = 2 * time.Second

	cronTaskFunc := func(_ context.Context) error {
		cronCount.Add(1)
		return nil
	}

	intervalTaskFunc := func(_ context.Context) error {
		intervalCount.Add(1)
		return nil
	}

	// Test cron task with RunImmediately = true
	err := scheduler.RegisterCronTaskWithOptions("cron-immediate", "*/30 * * * * *", cronTaskFunc, queue.TaskOptions{
		Immediately: true,
	})
	if err != nil {
		t.Fatalf("failed to register immediate cron task: %v", err)
	}

	// Test cron task with RunImmediately = false (default)
	err = scheduler.RegisterCronTaskWithOptions("cron-scheduled", "0 0 * * * *", cronTaskFunc, queue.TaskOptions{
		Immediately: false,
	})
	if err != nil {
		t.Fatalf("failed to register scheduled cron task: %v", err)
	}

	// Test interval task with RunImmediately = false
	err = scheduler.RegisterIntervalTaskWithOptions("interval-scheduled", intervalTaskPeriod, intervalTaskFunc, queue.TaskOptions{
		Immediately: false,
	})
	if err != nil {
		t.Fatalf("failed to register scheduled interval task: %v", err)
	}

	// Test interval task with default behavior (should run immediately for backward compatibility)
	err = scheduler.RegisterIntervalTask("interval-immediate", intervalTaskPeriod, intervalTaskFunc)
	if err != nil {
		t.Fatalf("failed to register immediate interval task: %v", err)
	}
}

func TestTaskScheduler_RunImmediatelyNextRun(t *testing.T) {
	const (
		immediateWindow         = 100 * time.Millisecond
		cronScheduledMinDelay   = 10 * time.Second
		intervalScheduledMinGap = 1500 * time.Millisecond
	)

	scheduler := queue.NewTaskScheduler().WithCheckInterval(taskCheckFast)

	var cronExecutionCount atomic.Int32
	var intervalExecutionCount atomic.Int32
	registerImmediateTasks(t, scheduler, &cronExecutionCount, &intervalExecutionCount)

	// Verify next run times before starting
	cronImmediate, err := scheduler.Task("cron-immediate")
	if err != nil {
		t.Fatalf("failed to get cron immediate task: %v", err)
	}

	cronScheduled, err := scheduler.Task("cron-scheduled")
	if err != nil {
		t.Fatalf("failed to get cron scheduled task: %v", err)
	}

	intervalScheduled, err := scheduler.Task("interval-scheduled")
	if err != nil {
		t.Fatalf("failed to get interval scheduled task: %v", err)
	}

	intervalImmediate, err := scheduler.Task("interval-immediate")
	if err != nil {
		t.Fatalf("failed to get interval immediate task: %v", err)
	}

	now := time.Now()

	// Immediate tasks should be scheduled to run now or very soon
	if cronImmediate.NextRun.After(now.Add(immediateWindow)) {
		t.Errorf("cron immediate task should run immediately, but next run is %v", cronImmediate.NextRun)
	}

	if intervalImmediate.NextRun.After(now.Add(immediateWindow)) {
		t.Errorf("interval immediate task should run immediately, but next run is %v", intervalImmediate.NextRun)
	}

	// Scheduled tasks should be scheduled for later
	if cronScheduled.NextRun.Before(now.Add(cronScheduledMinDelay)) {
		t.Errorf("cron scheduled task should not run immediately, but next run is %v", cronScheduled.NextRun)
	}

	if intervalScheduled.NextRun.Before(now.Add(intervalScheduledMinGap)) {
		t.Errorf("interval scheduled task should not run immediately, but next run is %v", intervalScheduled.NextRun)
	}
}

func TestTaskScheduler_RunImmediately(t *testing.T) {
	scheduler := queue.NewTaskScheduler().WithCheckInterval(taskCheckFast)

	var cronExecutionCount atomic.Int32
	var intervalExecutionCount atomic.Int32
	registerImmediateTasks(t, scheduler, &cronExecutionCount, &intervalExecutionCount)

	// Start the scheduler
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// The immediate tasks should have executed by now
	await.Until(t, taskWaitTimeout, func() bool {
		return cronExecutionCount.Load() > 0 && intervalExecutionCount.Load() > 0
	}, "the immediate cron and interval tasks to run")

	cronCount := cronExecutionCount.Load()
	intervalCount := intervalExecutionCount.Load()

	if cronCount == 0 {
		t.Error("cron immediate task should have executed by now")
	}

	if intervalCount == 0 {
		t.Error("interval immediate task should have executed by now")
	}

	cancel()
	scheduler.Stop()

	t.Logf("Cron executions: %d, Interval executions: %d", cronCount, intervalCount)
}
