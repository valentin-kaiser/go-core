package queue_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/internal/await"
	"github.com/valentin-kaiser/go-core/v2/queue"
)

const (
	queueWaitTimeout     = 5 * time.Second
	managerScheduleDelay = 500 * time.Millisecond
)

func TestNewJob(t *testing.T) {
	builder := queue.NewJob("test-job")
	job := builder.Build()
	if job.Type != "test-job" {
		t.Errorf("Expected job type 'test-job', got '%s'", job.Type)
	}

	if job.Priority != queue.PriorityNormal {
		t.Errorf("Expected priority Normal, got %v", job.Priority)
	}

	if job.MaxAttempts != 3 {
		t.Errorf("Expected max attempts 3, got %d", job.MaxAttempts)
	}
}

func TestJobBuilder(t *testing.T) {
	payload := map[string]any{
		"email": "test@example.com",
		"name":  "Test User",
	}

	job := queue.NewJob("email").
		WithID("test-id").
		WithPayload(payload).
		WithPriority(queue.PriorityHigh).
		WithMaxAttempts(5).
		WithDelay(time.Hour).
		WithMetadata("source", "test").
		Build()

	if job.ID != "test-id" {
		t.Errorf("Expected job ID 'test-id', got '%s'", job.ID)
	}

	if job.Type != "email" {
		t.Errorf("Expected job type 'email', got '%s'", job.Type)
	}

	if job.Priority != queue.PriorityHigh {
		t.Errorf("Expected priority High, got %v", job.Priority)
	}

	if job.MaxAttempts != 5 {
		t.Errorf("Expected max attempts 5, got %d", job.MaxAttempts)
	}

	if job.Status != queue.StatusScheduled {
		t.Errorf("Expected status Scheduled, got %v", job.Status)
	}

	// Unmarshal payload to check values
	var payloadMap map[string]any
	err := json.Unmarshal(job.Payload, &payloadMap)
	if err != nil {
		t.Fatalf("Failed to unmarshal payload: %v", err)
	}

	if payloadMap["email"] != "test@example.com" {
		t.Errorf("Expected payload email 'test@example.com', got '%v'", payloadMap["email"])
	}

	if job.Metadata["source"] != "test" {
		t.Errorf("Expected metadata source 'test', got '%s'", job.Metadata["source"])
	}
}

func TestJobBuilderWithPayloadJSON(t *testing.T) {
	jsonData := []byte(`{"email": "test@example.com", "count": 42}`)

	job := queue.NewJob("email").
		WithJSONPayload(jsonData).
		Build()

	// Unmarshal payload to check values
	var payloadMap map[string]any
	err := json.Unmarshal(job.Payload, &payloadMap)
	if err != nil {
		t.Fatalf("Failed to unmarshal payload: %v", err)
	}

	if payloadMap["email"] != "test@example.com" {
		t.Errorf("Expected payload email 'test@example.com', got '%v'", payloadMap["email"])
	}

	if payloadMap["count"] != float64(42) {
		t.Errorf("Expected payload count 42, got '%v'", payloadMap["count"])
	}
}

func TestJobStatus(t *testing.T) {
	job := &queue.Job{
		Status:      queue.StatusPending,
		MaxAttempts: 3,
	}

	if job.IsScheduled() {
		t.Error("Job should not be scheduled")
	}

	if job.IsExpired() {
		t.Error("Job should not be expired")
	}

	// Test scheduled job
	job.ScheduleAt = time.Now().Add(time.Hour)
	if !job.IsScheduled() {
		t.Error("Job should be scheduled")
	}

	// Test expired job
	job.Attempts = 5
	job.MaxAttempts = 3
	if !job.IsExpired() {
		t.Error("Job should be expired")
	}
}

func TestPriorityString(t *testing.T) {
	tests := []struct {
		priority queue.Priority
		expected string
	}{
		{priority: queue.PriorityLow, expected: "low"},
		{priority: queue.PriorityNormal, expected: "normal"},
		{priority: queue.PriorityHigh, expected: "high"},
		{priority: queue.PriorityCritical, expected: "critical"},
		{priority: queue.Priority(99), expected: "unknown"},
	}

	for _, test := range tests {
		if test.priority.String() != test.expected {
			t.Errorf("Expected priority string '%s', got '%s'", test.expected, test.priority.String())
		}
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		status   queue.Status
		expected string
	}{
		{status: queue.StatusPending, expected: "pending"},
		{status: queue.StatusRunning, expected: "running"},
		{status: queue.StatusCompleted, expected: "completed"},
		{status: queue.StatusFailed, expected: "failed"},
		{status: queue.StatusRetrying, expected: "retrying"},
		{status: queue.StatusScheduled, expected: "scheduled"},
		{status: queue.StatusDeadLetter, expected: "dead_letter"},
		{status: queue.Status(99), expected: "unknown"},
	}

	for _, test := range tests {
		if test.status.String() != test.expected {
			t.Errorf("Expected status string '%s', got '%s'", test.expected, test.status.String())
		}
	}
}

func TestMemoryQueue(t *testing.T) {
	ctx := t.Context()
	q := queue.NewMemoryQueue()

	// Test enqueue
	job := queue.NewJob("test").WithID("test-1").Build()
	err := q.Enqueue(ctx, job)
	if err != nil {
		t.Fatalf("Failed to enqueue job: %v", err)
	}

	// Test dequeue
	dequeuedJob, err := q.Dequeue(ctx, time.Second)
	if err != nil {
		t.Fatalf("Failed to dequeue job: %v", err)
	}

	if dequeuedJob.ID != job.ID {
		t.Errorf("Expected dequeued job ID '%s', got '%s'", job.ID, dequeuedJob.ID)
	}

	// Test get job
	retrievedJob, err := q.Job(ctx, job.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if retrievedJob.ID != job.ID {
		t.Errorf("Expected retrieved job ID '%s', got '%s'", job.ID, retrievedJob.ID)
	}

	// Test update job
	job.Status = queue.StatusCompleted
	err = q.UpdateJob(ctx, job)
	if err != nil {
		t.Fatalf("Failed to update job: %v", err)
	}

	// Test get jobs by status
	jobs, err := q.Jobs(ctx, queue.StatusCompleted, 10)
	if err != nil {
		t.Fatalf("Failed to get jobs by status: %v", err)
	}

	if len(jobs) != 1 {
		t.Errorf("Expected 1 completed job, got %d", len(jobs))
	}

	// Test stats
	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats.TotalJobs != 1 {
		t.Errorf("Expected 1 total job, got %d", stats.TotalJobs)
	}

	if stats.Completed != 1 {
		t.Errorf("Expected 1 completed job, got %d", stats.Completed)
	}

	// Test delete job
	err = q.DeleteJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("Failed to delete job: %v", err)
	}

	// Test close
	err = q.Close()
	if err != nil {
		t.Fatalf("Failed to close queue: %v", err)
	}
}

func TestMemoryQueueScheduled(t *testing.T) {
	ctx := t.Context()
	q := queue.NewMemoryQueue()

	// Test scheduled job
	job := queue.NewJob("test").
		WithID("test-scheduled").
		WithScheduleAt(time.Now().Add(time.Hour)).
		Build()

	err := q.Schedule(ctx, job)
	if err != nil {
		t.Fatalf("Failed to schedule job: %v", err)
	}

	// Should not be dequeued immediately
	dequeuedJob, err := q.Dequeue(ctx, time.Millisecond*100)
	if err != context.DeadlineExceeded {
		t.Errorf("Expected timeout error, got %v", err)
	}

	if dequeuedJob != nil {
		t.Error("Should not dequeue scheduled job")
	}

	// Test get scheduled jobs
	jobs, err := q.Jobs(ctx, queue.StatusScheduled, 10)
	if err != nil {
		t.Fatalf("Failed to get scheduled jobs: %v", err)
	}

	if len(jobs) != 1 {
		t.Errorf("Expected 1 scheduled job, got %d", len(jobs))
	}

	err = q.Close()
	if err != nil {
		t.Fatalf("Failed to close queue: %v", err)
	}
}

func TestMemoryQueuePriority(t *testing.T) {
	ctx := t.Context()
	q := queue.NewMemoryQueue()

	// Enqueue jobs with different priorities
	jobs := []*queue.Job{
		queue.NewJob("low").WithID("low-1").WithPriority(queue.PriorityLow).Build(),
		queue.NewJob("high").WithID("high-1").WithPriority(queue.PriorityHigh).Build(),
		queue.NewJob("normal").WithID("normal-1").WithPriority(queue.PriorityNormal).Build(),
		queue.NewJob("critical").WithID("critical-1").WithPriority(queue.PriorityCritical).Build(),
	}

	for _, job := range jobs {
		err := q.Enqueue(ctx, job)
		if err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}
	}

	// Dequeue jobs - should come out in priority order
	expectedOrder := []string{"critical-1", "high-1", "normal-1", "low-1"}
	for _, expectedID := range expectedOrder {
		job, err := q.Dequeue(ctx, time.Second)
		if err != nil {
			t.Fatalf("Failed to dequeue job: %v", err)
		}

		if job.ID != expectedID {
			t.Errorf("Expected job ID '%s', got '%s'", expectedID, job.ID)
		}
	}

	err := q.Close()
	if err != nil {
		t.Fatalf("Failed to close queue: %v", err)
	}
}

func TestManager(t *testing.T) {
	ctx := t.Context()
	manager := queue.NewScheduler().
		WithWorkers(2).
		WithRetryAttempts(2).
		WithRetryDelay(time.Millisecond * 100)

	// Register a test handler
	var handlerCalled bool
	var mu sync.Mutex
	manager.RegisterHandler("test", func(_ context.Context, _ *queue.Job) error {
		mu.Lock()
		handlerCalled = true
		mu.Unlock()
		return nil
	})

	// Start manager
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Enqueue a job
	job := queue.NewJob("test").WithID("test-manager").Build()
	err = manager.Enqueue(ctx, job)
	if err != nil {
		t.Fatalf("Failed to enqueue job: %v", err)
	}

	// Wait for job to be processed
	await.Until(t, queueWaitTimeout, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return handlerCalled
	}, "the handler to be called")

	// Stop manager: it waits for the worker to finish the job, which makes the job state safe to read
	err = manager.Stop(t.Context())
	if err != nil {
		t.Fatalf("Failed to stop manager: %v", err)
	}

	mu.Lock()
	called := handlerCalled
	mu.Unlock()

	if !called {
		t.Error("Handler should have been called")
	}

	// Check job status
	processedJob, err := manager.Job(ctx, job.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if processedJob.Status != queue.StatusCompleted {
		t.Errorf("Expected job status Completed, got %v", processedJob.Status)
	}

	if manager.IsRunning() {
		t.Error("Manager should not be running after stop")
	}
}

func TestManagerRetry(t *testing.T) {
	ctx := t.Context()
	manager := queue.NewScheduler().
		WithWorkers(1).
		WithRetryAttempts(2).
		WithRetryDelay(time.Millisecond * 50)

	// Register a handler that fails initially
	var attempts int
	var mu sync.Mutex
	manager.RegisterHandler("retry-test", func(_ context.Context, _ *queue.Job) error {
		mu.Lock()
		attempts++
		currentAttempts := attempts
		mu.Unlock()

		if currentAttempts < 2 {
			return queue.NewRetryableError(errors.New("temporary failure"))
		}
		return nil
	})

	// Start manager
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Enqueue a job
	job := queue.NewJob("retry-test").WithID("retry-job").Build()
	err = manager.Enqueue(ctx, job)
	if err != nil {
		t.Fatalf("Failed to enqueue job: %v", err)
	}

	// Wait for job to be processed with retries
	await.Until(t, queueWaitTimeout, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts >= 2
	}, "the retried job to run twice")

	// Stop manager: it waits for the worker to finish the job, which makes the job state safe to read
	err = manager.Stop(t.Context())
	if err != nil {
		t.Fatalf("Failed to stop manager: %v", err)
	}

	mu.Lock()
	finalAttempts := attempts
	mu.Unlock()

	if finalAttempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", finalAttempts)
	}

	// Check job status
	processedJob, err := manager.Job(ctx, job.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if processedJob.Status != queue.StatusCompleted {
		t.Errorf("Expected job status Completed, got %v", processedJob.Status)
	}
}

func TestManagerScheduled(t *testing.T) {
	ctx := t.Context()
	manager := queue.NewScheduler().
		WithWorkers(1).
		WithScheduleInterval(time.Millisecond * 50)

	// Register a test handler
	var handlerCalled bool
	var mu sync.Mutex
	manager.RegisterHandler("scheduled-test", func(_ context.Context, _ *queue.Job) error {
		mu.Lock()
		handlerCalled = true
		mu.Unlock()
		return nil
	})

	// Start manager
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Schedule a job for near future
	job := queue.NewJob("scheduled-test").
		WithID("scheduled-job").
		WithScheduleAt(time.Now().Add(managerScheduleDelay)).
		Build()

	err = manager.Schedule(ctx, job)
	if err != nil {
		t.Fatalf("Failed to schedule job: %v", err)
	}

	mu.Lock()
	called := handlerCalled
	mu.Unlock()

	if called {
		t.Error("Handler should not have been called yet")
	}

	// Wait for scheduled time
	await.Until(t, queueWaitTimeout, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return handlerCalled
	}, "the scheduled job to be processed")

	mu.Lock()
	called = handlerCalled
	mu.Unlock()

	if !called {
		t.Error("Handler should have been called after scheduled time")
	}

	// Stop manager
	err = manager.Stop(t.Context())
	if err != nil {
		t.Fatalf("Failed to stop manager: %v", err)
	}
}

func TestBatchManager(t *testing.T) {
	ctx := t.Context()
	manager := queue.NewScheduler().WithWorkers(2)

	// Register a test handler
	processedJobs := make(map[string]bool)
	var processedMutex sync.Mutex
	manager.RegisterHandler("batch-test", func(_ context.Context, job *queue.Job) error {
		processedMutex.Lock()
		processedJobs[job.ID] = true
		processedMutex.Unlock()
		return nil
	})

	// Start manager
	err := manager.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start manager: %v", err)
	}

	// Create batch manager
	batchManager := queue.NewBatcher(manager)

	// Create batch of jobs
	jobs := []*queue.Job{
		queue.NewJob("batch-test").WithID("batch-job-1").Build(),
		queue.NewJob("batch-test").WithID("batch-job-2").Build(),
		queue.NewJob("batch-test").WithID("batch-job-3").Build(),
	}

	batch, err := batchManager.CreateBatch(ctx, "test-batch", jobs)
	if err != nil {
		t.Fatalf("Failed to create batch: %v", err)
	}

	if batch.Total != 3 {
		t.Errorf("Expected batch total 3, got %d", batch.Total)
	}

	if batch.Pending != 3 {
		t.Errorf("Expected batch pending 3, got %d", batch.Pending)
	}

	// Wait for jobs to be processed
	await.Until(t, queueWaitTimeout, func() bool {
		processedMutex.Lock()
		defer processedMutex.Unlock()
		return len(processedJobs) == len(jobs)
	}, "all batch jobs to be processed")

	// Stop manager: it waits for the workers to finish the jobs, which makes the job states safe to read
	err = manager.Stop(t.Context())
	if err != nil {
		t.Fatalf("Failed to stop manager: %v", err)
	}

	// Update batch status
	err = batchManager.UpdateBatchStatus(ctx, batch.ID)
	if err != nil {
		t.Fatalf("Failed to update batch status: %v", err)
	}

	// Check that all jobs were processed
	processedMutex.Lock()
	processedCount := len(processedJobs)
	processedMutex.Unlock()
	if processedCount != 3 {
		t.Errorf("Expected 3 processed jobs, got %d", processedCount)
	}

	// Get updated batch
	updatedBatch, err := batchManager.Batch(batch.ID)
	if err != nil {
		t.Fatalf("Failed to get batch: %v", err)
	}

	if updatedBatch.Status != queue.StatusCompleted {
		t.Errorf("Expected batch status Completed, got %v", updatedBatch.Status)
	}
}

func TestMiddleware(t *testing.T) {
	ctx := t.Context()
	job := queue.NewJob("test").WithID("middleware-test").Build()

	// Test logging middleware
	called := false
	handler := queue.LoggingMiddleware(func(_ context.Context, _ *queue.Job) error {
		called = true
		return nil
	})

	err := handler(ctx, job)
	if err != nil {
		t.Errorf("Handler should not return error: %v", err)
	}

	if !called {
		t.Error("Handler should have been called")
	}

	// Test timeout middleware
	timeoutHandler := queue.TimeoutMiddleware(time.Millisecond * 100)(func(ctx context.Context, _ *queue.Job) error {
		<-ctx.Done()
		return ctx.Err()
	})

	err = timeoutHandler(ctx, job)
	if err == nil {
		t.Error("Handler should have timed out")
	}

	// Test recovery middleware
	recoveryHandler := queue.RecoveryMiddleware(func(_ context.Context, _ *queue.Job) error {
		panic("test panic")
	})

	err = recoveryHandler(ctx, job)
	if err == nil {
		t.Error("Handler should return error after panic")
	}

	// Test middleware chain
	chainHandler := queue.MiddlewareChain(
		func(_ context.Context, _ *queue.Job) error {
			return nil
		},
		queue.LoggingMiddleware,
		queue.MetricsMiddleware,
		queue.RecoveryMiddleware,
	)

	err = chainHandler(ctx, job)
	if err != nil {
		t.Errorf("Chain handler should not return error: %v", err)
	}
}

func TestJobContext(t *testing.T) {
	ctx := t.Context()
	job := queue.NewJob("test").
		WithPayload(map[string]any{
			"string_value": "test",
			"int_value":    42,
			"bool_value":   true,
		}).
		WithMetadata("source", "test").
		Build()

	jobCtx := queue.NewJobContext(ctx, job)

	// Test payload getters
	strValue, ok := jobCtx.PayloadString("string_value")
	if !ok || strValue != "test" {
		t.Errorf("Expected string value 'test', got '%s'", strValue)
	}

	intValue, ok := jobCtx.PayloadInt("int_value")
	if !ok || intValue != 42 {
		t.Errorf("Expected int value 42, got %d", intValue)
	}

	boolValue, ok := jobCtx.PayloadBool("bool_value")
	if !ok || !boolValue {
		t.Errorf("Expected bool value true, got %v", boolValue)
	}

	// Test metadata getter
	source, ok := jobCtx.Metadata("source")
	if !ok || source != "test" {
		t.Errorf("Expected metadata source 'test', got '%s'", source)
	}

	// Test progress reporting
	jobCtx.ReportProgress(0.5)
	// Progress reporting is async, so we can't easily test the result
}

func TestRetryableError(t *testing.T) {
	err := queue.NewRetryableError(errors.New("test error"))

	if err.Error() != "test error" {
		t.Errorf("Expected error message 'test error', got '%s'", err.Error())
	}

	if !queue.IsRetryable(err) {
		t.Error("Error should be retryable")
	}

	regularErr := errors.New("regular error")
	if queue.IsRetryable(regularErr) {
		t.Error("Regular error should not be retryable")
	}
}

func BenchmarkMemoryQueueEnqueue(b *testing.B) {
	ctx := b.Context()
	q := queue.NewMemoryQueue()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job := queue.NewJob("benchmark").WithID(fmt.Sprintf("job-%d", i)).Build()
		err := q.Enqueue(ctx, job)
		if err != nil {
			b.Fatalf("failed to enqueue job: %v", err)
		}
	}
}

func BenchmarkMemoryQueueDequeue(b *testing.B) {
	ctx := b.Context()
	q := queue.NewMemoryQueue()

	// Pre-populate queue
	for i := 0; i < b.N; i++ {
		job := queue.NewJob("benchmark").WithID(fmt.Sprintf("job-%d", i)).Build()
		err := q.Enqueue(ctx, job)
		if err != nil {
			b.Fatalf("failed to enqueue job: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := q.Dequeue(ctx, time.Second)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkJobBuilder(b *testing.B) {
	payload := map[string]any{
		"email": "test@example.com",
		"name":  "Test User",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		queue.NewJob("email").
			WithPayload(payload).
			WithPriority(queue.PriorityHigh).
			WithMaxAttempts(3).
			WithMetadata("source", "test").
			Build()
	}
}

func TestJobScheduling(t *testing.T) {
	scheduleTime := time.Now().Add(time.Hour)

	job := queue.NewJob("scheduled").
		WithScheduleAt(scheduleTime).
		Build()

	if job.ScheduleAt.IsZero() {
		t.Error("Expected ScheduleAt to be set")
	}

	if !job.ScheduleAt.Equal(scheduleTime) {
		t.Errorf("Expected ScheduleAt to be %v, got %v", scheduleTime, job.ScheduleAt)
	}
}

func TestJobMetadataAndBasics(t *testing.T) {
	job := queue.NewJob("test").
		WithMetadata("key1", "value1").
		WithMetadata("key2", "value2").
		Build()

	if job.Metadata["key1"] != "value1" {
		t.Errorf("Expected metadata key1 to be 'value1', got '%s'", job.Metadata["key1"])
	}

	if job.Metadata["key2"] != "value2" {
		t.Errorf("Expected metadata key2 to be 'value2', got '%s'", job.Metadata["key2"])
	}
}

func TestManagerBasics(t *testing.T) {
	manager := queue.NewScheduler()
	if manager == nil {
		t.Error("Expected manager to be created")
	}

	stats := manager.Stats()
	if stats == nil {
		t.Error("Expected stats to be available")
	}
}
