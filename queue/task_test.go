package queue_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/queue"
)

func TestTaskScheduler_RegisterIntervalTask(t *testing.T) {
	scheduler := queue.NewTaskScheduler()

	var executed atomic.Int32
	taskFunc := func(_ context.Context) error {
		executed.Add(1)
		return nil
	}

	err := scheduler.RegisterIntervalTask("test-task", time.Second, taskFunc)
	if err != nil {
		t.Fatalf("failed to register interval task: %v", err)
	}

	task, err := scheduler.GetTask("test-task")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if task.Name != "test-task" {
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
	err = scheduler.RegisterIntervalTask("test-task", time.Second, taskFunc)
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
	err := scheduler.RegisterCronTask("test-cron", "*/5 * * * *", taskFunc)
	if err != nil {
		t.Fatalf("failed to register cron task: %v", err)
	}

	task, err := scheduler.GetTask("test-cron")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if task.Type != queue.TaskTypeCron {
		t.Errorf("expected task type cron, got %v", task.Type)
	}

	if task.CronSpec != "*/5 * * * *" {
		t.Errorf("expected cron spec '*/5 * * * *', got '%s'", task.CronSpec)
	}

	// Test invalid cron expression
	err = scheduler.RegisterCronTask("invalid-cron", "invalid", taskFunc)
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}

	// Test empty name
	err = scheduler.RegisterCronTask("", "*/5 * * * *", taskFunc)
	if err == nil {
		t.Error("expected error for empty task name")
	}

	// Test nil function
	err = scheduler.RegisterCronTask("nil-func", "*/5 * * * *", nil)
	if err == nil {
		t.Error("expected error for nil function")
	}
}

func TestTaskScheduler_StartStop(t *testing.T) {
	scheduler := queue.NewTaskScheduler()

	ctx, cancel := context.WithTimeout(t.Context(), time.Second*5)
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
	scheduler := queue.NewTaskScheduler().WithCheckInterval(time.Millisecond * 100)

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

	err := scheduler.RegisterIntervalTask("test-interval", time.Millisecond*300, taskFunc)
	if err != nil {
		t.Fatalf("failed to register interval task: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second*2)
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait for a few executions
	time.Sleep(time.Second)
	scheduler.Stop()

	execCount := executed.Load()
	if execCount < 2 {
		t.Errorf("expected at least 2 executions, got %d", execCount)
	}

	// Check that task was executed at reasonable intervals
	mu.Lock()
	defer mu.Unlock()

	if len(executionTimes) < 2 {
		t.Skip("not enough executions to test intervals")
	}

	for i := 1; i < len(executionTimes); i++ {
		interval := executionTimes[i].Sub(executionTimes[i-1])
		if interval < time.Millisecond*200 || interval > time.Millisecond*500 {
			t.Errorf("unexpected interval between executions: %v", interval)
		}
	}
}

func TestTaskScheduler_TaskRetry(t *testing.T) {
	scheduler := queue.NewTaskScheduler().
		WithCheckInterval(time.Millisecond * 50).
		WithRetryDelay(time.Millisecond * 50)

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

	err := scheduler.RegisterIntervalTaskWithOptions("retry-task", time.Second*10, taskFunc, queue.TaskOptions{
		MaxRetries:  3,
		RetryDelay:  time.Millisecond * 30,
		Immediately: true, // Run immediately for this test
	})
	if err != nil {
		t.Fatalf("failed to register task: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second*2)
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait for task to be executed and retried
	time.Sleep(time.Millisecond * 300)
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
	task, err := scheduler.GetTask("retry-task")
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
	scheduler := queue.NewTaskScheduler().
		WithCheckInterval(time.Millisecond * 50).
		WithRetryDelay(time.Millisecond * 30)

	var attempts atomic.Int32
	expectedError := errors.New("task always fails")

	taskFunc := func(_ context.Context) error {
		attempts.Add(1)
		return expectedError
	}

	err := scheduler.RegisterIntervalTaskWithOptions("failing-task", time.Second*5, taskFunc, queue.TaskOptions{
		MaxRetries:  2,
		RetryDelay:  time.Millisecond * 30,
		Immediately: true, // Run immediately for this test
	})
	if err != nil {
		t.Fatalf("failed to register task: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second*2)
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait for task to fail completely
	time.Sleep(time.Millisecond * 200)
	scheduler.Stop()

	attemptCount := attempts.Load()
	if attemptCount < 3 { // At least 1 initial + 2 retries
		t.Errorf("expected at least 3 attempts, got %d", attemptCount)
	}

	// Check task stats
	task, err := scheduler.GetTask("failing-task")
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
	scheduler := queue.NewTaskScheduler()

	taskFunc := func(_ context.Context) error {
		return nil
	}

	err := scheduler.RegisterIntervalTask("toggle-task", time.Second, taskFunc)
	if err != nil {
		t.Fatalf("failed to register task: %v", err)
	}

	// Test disable
	err = scheduler.DisableTask("toggle-task")
	if err != nil {
		t.Fatalf("failed to disable task: %v", err)
	}

	task, err := scheduler.GetTask("toggle-task")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if task.Enabled {
		t.Error("expected task to be disabled")
	}

	// Test enable
	err = scheduler.EnableTask("toggle-task")
	if err != nil {
		t.Fatalf("failed to enable task: %v", err)
	}

	task, err = scheduler.GetTask("toggle-task")
	if err != nil {
		t.Fatalf("failed to get task: %v", err)
	}

	if !task.Enabled {
		t.Error("expected task to be enabled")
	}

	// Test non-existent task
	err = scheduler.EnableTask("non-existent")
	if err == nil {
		t.Error("expected error when enabling non-existent task")
	}

	err = scheduler.DisableTask("non-existent")
	if err == nil {
		t.Error("expected error when disabling non-existent task")
	}
}

func TestTaskScheduler_RemoveTask(t *testing.T) {
	scheduler := queue.NewTaskScheduler()

	taskFunc := func(_ context.Context) error {
		return nil
	}

	err := scheduler.RegisterIntervalTask("remove-task", time.Second, taskFunc)
	if err != nil {
		t.Fatalf("failed to register task: %v", err)
	}

	// Test removal
	err = scheduler.RemoveTask("remove-task")
	if err != nil {
		t.Fatalf("failed to remove task: %v", err)
	}

	// Check that task is gone
	_, err = scheduler.GetTask("remove-task")
	if err == nil {
		t.Error("expected error when getting removed task")
	}

	// Test removing non-existent task
	err = scheduler.RemoveTask("non-existent")
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

	tasks := scheduler.GetTasks()

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

	task, err := scheduler.GetTask("task1")
	if err != nil {
		t.Fatalf("failed to get task1: %v", err)
	}

	if !task.Enabled {
		t.Error("modifying returned task affected original task")
	}
}

func TestTaskScheduler_ConcurrentExecution(t *testing.T) {
	scheduler := queue.NewTaskScheduler().WithCheckInterval(100 * time.Millisecond)

	var executionCount atomic.Int32
	var concurrentCount atomic.Int32
	var maxConcurrent atomic.Int32

	taskFunc := func(_ context.Context) error {
		// Track concurrent executions
		current := concurrentCount.Add(1)
		for {
			existing := maxConcurrent.Load()
			if current <= existing || maxConcurrent.CompareAndSwap(existing, current) {
				break
			}
		}

		// Simulate work
		time.Sleep(300 * time.Millisecond)

		executionCount.Add(1)
		concurrentCount.Add(-1)
		return nil
	}

	// Test non-concurrent task (default behavior)
	err := scheduler.RegisterIntervalTask("non-concurrent", 150*time.Millisecond, taskFunc)
	if err != nil {
		t.Fatalf("failed to register non-concurrent task: %v", err)
	}

	// Reset counters
	executionCount.Store(0)
	concurrentCount.Store(0)
	maxConcurrent.Store(0)

	// Test concurrent task
	err = scheduler.RegisterIntervalTaskWithOptions("concurrent", 150*time.Millisecond, taskFunc, queue.TaskOptions{
		Concurrent: true,
	})
	if err != nil {
		t.Fatalf("failed to register concurrent task: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait for some executions
	time.Sleep(1 * time.Second)
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
	task, err := scheduler.GetTask("concurrent")
	if err != nil {
		t.Fatalf("failed to get concurrent task: %v", err)
	}

	if !task.AllowConcurrent {
		t.Error("expected AllowConcurrent to be true")
	}

	task, err = scheduler.GetTask("non-concurrent")
	if err != nil {
		t.Fatalf("failed to get non-concurrent task: %v", err)
	}

	if task.AllowConcurrent {
		t.Error("expected AllowConcurrent to be false")
	}
}

func TestTaskScheduler_ConcurrentCronExecution(t *testing.T) {
	scheduler := queue.NewTaskScheduler().WithCheckInterval(50 * time.Millisecond)

	var executionCount atomic.Int32
	var concurrentCount atomic.Int32
	var maxConcurrent atomic.Int32

	taskFunc := func(_ context.Context) error {
		// Track concurrent executions
		current := concurrentCount.Add(1)
		for {
			existing := maxConcurrent.Load()
			if current <= existing || maxConcurrent.CompareAndSwap(existing, current) {
				break
			}
		}

		// Simulate work - longer than the cron interval to force overlap
		time.Sleep(1500 * time.Millisecond)

		executionCount.Add(1)
		concurrentCount.Add(-1)
		return nil
	}

	// Test concurrent cron task that runs every second
	err := scheduler.RegisterCronTaskWithOptions("concurrent-cron", "* * * * * *", taskFunc, queue.TaskOptions{
		Concurrent: true,
	})
	if err != nil {
		t.Fatalf("failed to register concurrent cron task: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait for some executions
	time.Sleep(3500 * time.Millisecond)
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
	task, err := scheduler.GetTask("concurrent-cron")
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

func TestTaskScheduler_RunImmediately(t *testing.T) {
	scheduler := queue.NewTaskScheduler().WithCheckInterval(50 * time.Millisecond)

	var cronExecutionCount atomic.Int32
	var intervalExecutionCount atomic.Int32

	// Simple task function that increments a counter
	cronTaskFunc := func(_ context.Context) error {
		cronExecutionCount.Add(1)
		return nil
	}

	intervalTaskFunc := func(_ context.Context) error {
		intervalExecutionCount.Add(1)
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
	err = scheduler.RegisterIntervalTaskWithOptions("interval-scheduled", 2*time.Second, intervalTaskFunc, queue.TaskOptions{
		Immediately: false,
	})
	if err != nil {
		t.Fatalf("failed to register scheduled interval task: %v", err)
	}

	// Test interval task with default behavior (should run immediately for backward compatibility)
	err = scheduler.RegisterIntervalTask("interval-immediate", 2*time.Second, intervalTaskFunc)
	if err != nil {
		t.Fatalf("failed to register immediate interval task: %v", err)
	}

	// Verify next run times before starting
	cronImmediate, err := scheduler.GetTask("cron-immediate")
	if err != nil {
		t.Fatalf("failed to get cron immediate task: %v", err)
	}

	cronScheduled, err := scheduler.GetTask("cron-scheduled")
	if err != nil {
		t.Fatalf("failed to get cron scheduled task: %v", err)
	}

	intervalScheduled, err := scheduler.GetTask("interval-scheduled")
	if err != nil {
		t.Fatalf("failed to get interval scheduled task: %v", err)
	}

	intervalImmediate, err := scheduler.GetTask("interval-immediate")
	if err != nil {
		t.Fatalf("failed to get interval immediate task: %v", err)
	}

	now := time.Now()

	// Immediate tasks should be scheduled to run now or very soon
	if cronImmediate.NextRun.After(now.Add(100 * time.Millisecond)) {
		t.Errorf("cron immediate task should run immediately, but next run is %v", cronImmediate.NextRun)
	}

	if intervalImmediate.NextRun.After(now.Add(100 * time.Millisecond)) {
		t.Errorf("interval immediate task should run immediately, but next run is %v", intervalImmediate.NextRun)
	}

	// Scheduled tasks should be scheduled for later
	if cronScheduled.NextRun.Before(now.Add(10 * time.Second)) {
		t.Errorf("cron scheduled task should not run immediately, but next run is %v", cronScheduled.NextRun)
	}

	if intervalScheduled.NextRun.Before(now.Add(1500 * time.Millisecond)) {
		t.Errorf("interval scheduled task should not run immediately, but next run is %v", intervalScheduled.NextRun)
	}

	// Start the scheduler
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start scheduler: %v", err)
	}

	// Wait a short time to see which tasks execute immediately
	time.Sleep(500 * time.Millisecond)

	// The immediate tasks should have executed by now
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
