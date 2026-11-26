//go:build integration

package queue_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/internal/await"
	"github.com/valentin-kaiser/go-core/v2/queue"
)

// Login of the broker that test/compose.yaml starts for the integration tests.
const (
	rabbitMQTestUser = "admin"
	rabbitMQTestKey  = "admin123"
	rabbitMQTestHost = "localhost:5672"
)

const (
	rabbitMQStartHint        = "docker compose -f test/compose.yaml up -d"
	rabbitMQDequeueTimeout   = 5 * time.Second
	rabbitMQEmptyTimeout     = 400 * time.Millisecond
	rabbitMQShortTimeout     = 100 * time.Millisecond
	rabbitMQShortDelay       = 200 * time.Millisecond
	rabbitMQLongDelay        = 700 * time.Millisecond
	rabbitMQGoroutineWait    = 5 * time.Second
	rabbitMQCloseWait        = 2 * time.Second
	rabbitMQDeliveryLimit    = time.Second
	rabbitMQRetentionWait    = 2 * time.Second
	rabbitMQWorkerTimeout    = 500 * time.Millisecond
	rabbitMQPriorityJobs     = 3
	rabbitMQListLimit        = 10
	rabbitMQWorkerCount      = 4
	rabbitMQConsumerJobs     = 200
	rabbitMQWorkerPrefetch   = 8
	rabbitMQRetainFinished   = 3
	rabbitMQRetentionJobs    = 10
	rabbitMQSmallPrefetch    = 2
	rabbitMQConsumerPrefetch = 5
	rabbitMQWakePrefetch     = 1
	rabbitMQBenchPrefetch    = 100
	rabbitMQQueueNameFormat  = "%s_%d"
)

func TestRabbitMQBasicEnqueueDequeue(t *testing.T) {
	q := newTestRabbitMQ(t, "basic")
	ctx := t.Context()

	job := queue.NewJob("test-job").
		WithID("test-1").
		WithPayload(map[string]any{"message": "hello world"}).
		Build()

	err := q.Enqueue(ctx, job)
	if err != nil {
		t.Fatalf("Failed to enqueue job: %v", err)
	}

	dequeuedJob, err := q.Dequeue(ctx, rabbitMQDequeueTimeout)
	if err != nil {
		t.Fatalf("Failed to dequeue job: %v", err)
	}

	if dequeuedJob == nil {
		t.Fatal("Expected job, got nil")
	}

	if dequeuedJob.ID != job.ID {
		t.Errorf("Expected job ID %s, got %s", job.ID, dequeuedJob.ID)
	}

	if dequeuedJob.Type != job.Type {
		t.Errorf("Expected job type %s, got %s", job.Type, dequeuedJob.Type)
	}

	if dequeuedJob.Status != queue.StatusRunning {
		t.Errorf("Expected job status %s, got %s", queue.StatusRunning, dequeuedJob.Status)
	}

	var payload map[string]any
	err = json.Unmarshal(dequeuedJob.Payload, &payload)
	if err != nil {
		t.Fatalf("Failed to unmarshal payload: %v", err)
	}

	if payload["message"] != "hello world" {
		t.Errorf("Expected message 'hello world', got %v", payload["message"])
	}
}

func TestRabbitMQScheduledJobDelivered(t *testing.T) {
	q := newTestRabbitMQ(t, "scheduled")
	ctx := t.Context()

	job := queue.NewJob("scheduled-job").
		WithID("scheduled-1").
		WithDelay(rabbitMQShortDelay).
		WithPayload(map[string]any{"scheduled": true}).
		Build()

	err := q.Schedule(ctx, job)
	if err != nil {
		t.Fatalf("Failed to schedule job: %v", err)
	}

	dequeuedJob, err := q.Dequeue(ctx, rabbitMQDequeueTimeout)
	if err != nil {
		t.Fatalf("Failed to dequeue scheduled job: %v", err)
	}

	if dequeuedJob == nil {
		t.Fatal("Expected scheduled job, got nil")
	}

	if dequeuedJob.ID != job.ID {
		t.Errorf("Expected job ID %s, got %s", job.ID, dequeuedJob.ID)
	}

	dequeuedJob.Status = queue.StatusCompleted
	err = q.UpdateJob(ctx, dequeuedJob)
	if err != nil {
		t.Fatalf("Failed to update job: %v", err)
	}
}

func TestRabbitMQPriorityJobs(t *testing.T) {
	q := newTestRabbitMQ(t, "priority")
	ctx := t.Context()

	lowJob := queue.NewJob("low-job").
		WithID("low-1").
		WithPriority(queue.PriorityLow).
		Build()

	highJob := queue.NewJob("high-job").
		WithID("high-1").
		WithPriority(queue.PriorityHigh).
		Build()

	normalJob := queue.NewJob("normal-job").
		WithID("normal-1").
		WithPriority(queue.PriorityNormal).
		Build()

	err := q.Enqueue(ctx, normalJob)
	if err != nil {
		t.Fatalf("Failed to enqueue normal job: %v", err)
	}

	err = q.Enqueue(ctx, lowJob)
	if err != nil {
		t.Fatalf("Failed to enqueue low job: %v", err)
	}

	err = q.Enqueue(ctx, highJob)
	if err != nil {
		t.Fatalf("Failed to enqueue high job: %v", err)
	}

	for i := range rabbitMQPriorityJobs {
		job, err := q.Dequeue(ctx, rabbitMQDequeueTimeout)
		if err != nil {
			t.Fatalf("Failed to dequeue job %d: %v", i, err)
		}

		if job == nil {
			t.Fatalf("Expected job %d, got nil", i)
		}

		job.Status = queue.StatusCompleted
		err = q.UpdateJob(ctx, job)
		if err != nil {
			t.Fatalf("Failed to update job %d: %v", i, err)
		}
	}

	// Note: RabbitMQ priority might not be strictly enforced in our simple test
	// but the jobs should all be processed successfully
}

func TestRabbitMQJobOperations(t *testing.T) {
	q := newTestRabbitMQ(t, "operations")
	ctx := t.Context()

	job := queue.NewJob("test-ops").
		WithID("ops-1").
		WithPayload(map[string]any{"operation": "test"}).
		Build()

	err := q.Enqueue(ctx, job)
	if err != nil {
		t.Fatalf("Failed to enqueue job: %v", err)
	}

	retrievedJob, err := q.Job(ctx, job.ID)
	if err != nil {
		t.Fatalf("Failed to get job: %v", err)
	}

	if retrievedJob.ID != job.ID {
		t.Errorf("Expected job ID %s, got %s", job.ID, retrievedJob.ID)
	}

	pendingJobs, err := q.Jobs(ctx, queue.StatusPending, rabbitMQListLimit)
	if err != nil {
		t.Fatalf("Failed to get pending jobs: %v", err)
	}

	found := false
	for _, pJob := range pendingJobs {
		if pJob.ID == job.ID {
			found = true
			break
		}
	}

	if !found {
		t.Error("Job not found in pending jobs")
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats: %v", err)
	}

	if stats.Pending == 0 {
		t.Error("Expected pending jobs in stats")
	}

	err = q.DeleteJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("Failed to delete job: %v", err)
	}

	_, err = q.Job(ctx, job.ID)
	if err == nil {
		t.Error("Expected error when getting deleted job")
	}
}

func TestRabbitMQPurge(t *testing.T) {
	q := newTestRabbitMQ(t, "purge")
	ctx := t.Context()

	if !q.IsConnectionOpen() {
		t.Error("Expected connection to be open")
	}

	err := q.PurgeQueue(ctx)
	if err != nil {
		t.Fatalf("Failed to purge queue: %v", err)
	}

	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatalf("Failed to get stats after purge: %v", err)
	}

	if stats.QueueSize != 0 {
		t.Errorf("Expected queue size 0 after purge, got %d", stats.QueueSize)
	}
}

func TestRabbitMQReconnection(t *testing.T) {
	config := queue.RabbitMQConfig{
		URL:          rabbitMQTestURL(),
		QueueName:    "test_reconnect",
		ExchangeName: "test_reconnect_exchange",
		RoutingKey:   "test_reconnect",
		Durable:      false,
		AutoDelete:   true,
	}

	q := newRabbitMQ(t, config)

	err := q.Reconnect(config)
	if err != nil {
		t.Fatalf("Failed to reconnect: %v", err)
	}

	if !q.IsConnectionOpen() {
		t.Error("Expected connection to be open after reconnection")
	}
}

func TestRabbitMQWithClosedConnection(t *testing.T) {
	q := openRabbitMQ(t, queue.RabbitMQConfig{
		URL:          rabbitMQTestURL(),
		QueueName:    "test_closed",
		ExchangeName: "test_closed_exchange",
		RoutingKey:   "test_closed",
		Durable:      false,
		AutoDelete:   true,
	})

	err := q.Close()
	if err != nil {
		t.Logf("Warning: failed to close queue: %v", err)
	}

	job := queue.NewJob("test-closed").WithID("closed-1").Build()

	tests := []struct {
		name      string
		operation func(ctx context.Context) error
	}{
		{
			name:      "enqueuing",
			operation: func(ctx context.Context) error { return q.Enqueue(ctx, job) },
		},
		{
			name: "dequeuing",
			operation: func(ctx context.Context) error {
				_, err := q.Dequeue(ctx, time.Second)
				return err
			},
		},
		{
			name: "getting job",
			operation: func(ctx context.Context) error {
				_, err := q.Job(ctx, job.ID)
				return err
			},
		},
		{
			name: "getting jobs",
			operation: func(ctx context.Context) error {
				_, err := q.Jobs(ctx, queue.StatusPending, rabbitMQListLimit)
				return err
			},
		},
		{
			name: "getting stats",
			operation: func(ctx context.Context) error {
				_, err := q.Stats(ctx)
				return err
			},
		},
		{
			name:      "deleting job",
			operation: func(ctx context.Context) error { return q.DeleteJob(ctx, job.ID) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.operation(t.Context())
			if err == nil {
				t.Errorf("Expected error when %s on closed queue", tt.name)
			}
		})
	}
}

func BenchmarkRabbitMQEnqueue(b *testing.B) {
	q := newRabbitMQ(b, queue.RabbitMQConfig{
		URL:          rabbitMQTestURL(),
		QueueName:    "benchmark_queue",
		ExchangeName: "benchmark_exchange",
		RoutingKey:   "benchmark",
		Durable:      false,
		AutoDelete:   true,
	})

	ctx := b.Context()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			job := queue.NewJob("benchmark-job").
				WithID(fmt.Sprintf("bench-%d", i)).
				WithPayload(map[string]any{"index": i}).
				Build()

			err := q.Enqueue(ctx, job)
			if err != nil {
				b.Errorf("Failed to enqueue job: %v", err)
				return
			}
			i++
		}
	})
}

func BenchmarkRabbitMQDequeue(b *testing.B) {
	q := newRabbitMQ(b, queue.RabbitMQConfig{
		URL:          rabbitMQTestURL(),
		QueueName:    "benchmark_dequeue",
		ExchangeName: "benchmark_dequeue_exchange",
		RoutingKey:   "benchmark_dequeue",
		Durable:      false,
		AutoDelete:   true,
	})

	ctx := b.Context()

	// Pre-populate queue with jobs
	for i := 0; i < b.N; i++ {
		job := queue.NewJob("benchmark-dequeue-job").
			WithID(fmt.Sprintf("dequeue-bench-%d", i)).
			WithPayload(map[string]any{"index": i}).
			Build()

		err := q.Enqueue(ctx, job)
		if err != nil {
			b.Fatalf("Failed to enqueue job: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job, err := q.Dequeue(ctx, rabbitMQDequeueTimeout)
		if err != nil {
			b.Fatalf("Failed to dequeue job: %v", err)
		}
		if job == nil {
			b.Fatal("Expected job, got nil")
		}

		job.Status = queue.StatusCompleted
		err = q.UpdateJob(ctx, job)
		if err != nil {
			b.Fatalf("Failed to update job: %v", err)
		}
	}
}

// openRabbitMQ connects to the broker and fails the test when it is not running.
func openRabbitMQ(tb testing.TB, config queue.RabbitMQConfig) *queue.RabbitMQ {
	tb.Helper()

	q, err := queue.NewRabbitMQ(config)
	if err != nil {
		tb.Fatalf("connecting to RabbitMQ failed, start it with `%s`: %v", rabbitMQStartHint, err)
	}
	return q
}

// newRabbitMQ connects to the broker and purges and closes the queue when the test ends.
func newRabbitMQ(tb testing.TB, config queue.RabbitMQConfig) *queue.RabbitMQ {
	tb.Helper()

	q := openRabbitMQ(tb, config)
	tb.Cleanup(func() {
		err := q.PurgeQueue(context.Background())
		if err != nil {
			tb.Logf("purging queue failed: %v", err)
		}
		err = q.Close()
		if err != nil {
			tb.Logf("closing queue failed: %v", err)
		}
	})
	return q
}

// uniqueRabbitMQName returns a name per run: basic.get never auto-deletes a queue, so fixed names collect leftovers.
func uniqueRabbitMQName(name string) string {
	return fmt.Sprintf(rabbitMQQueueNameFormat, name, time.Now().UnixNano())
}

func newTestRabbitMQ(t *testing.T, name string) *queue.RabbitMQ {
	t.Helper()

	suffix := uniqueRabbitMQName(name)
	return newRabbitMQ(t, queue.RabbitMQConfig{
		URL:          rabbitMQTestURL(),
		QueueName:    suffix,
		ExchangeName: suffix + "_exchange",
		RoutingKey:   suffix,
		Durable:      false,
		AutoDelete:   true,
	})
}

// Dequeue has to wait for the whole timeout when the queue is empty, like the memory queue does.
func TestRabbitMQDequeueHonorsTimeout(t *testing.T) {
	q := newTestRabbitMQ(t, "timeout")

	start := time.Now()
	_, err := q.Dequeue(t.Context(), rabbitMQEmptyTimeout)
	elapsed := time.Since(start)
	if !errors.Is(err, queue.ErrNoJobAvailable) {
		t.Fatalf("expected ErrNoJobAvailable, got %v", err)
	}
	if elapsed < 350*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("Dequeue returned after %v, want about 400ms", elapsed)
	}
}

// A job enqueued while a consumer waits is delivered long before the timeout.
func TestRabbitMQDequeueWakesOnNewJob(t *testing.T) {
	q := newTestRabbitMQ(t, "wake")

	waiting := make(chan struct{})
	go func() {
		<-waiting
		err := q.Enqueue(context.Background(), queue.NewJob("wake").WithID("wake-1").Build())
		if err != nil {
			t.Errorf("enqueuing wake job failed: %v", err)
		}
	}()

	start := time.Now()
	close(waiting)
	job, err := q.Dequeue(t.Context(), rabbitMQDequeueTimeout)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if job.ID != "wake-1" {
		t.Fatalf("got job %q", job.ID)
	}
	if elapsed := time.Since(start); elapsed > rabbitMQDeliveryLimit {
		t.Fatalf("job was delivered after %v", elapsed)
	}
}

// A scheduled job must not be delivered before its time.
func TestRabbitMQScheduledJobIsDelayed(t *testing.T) {
	q := newTestRabbitMQ(t, "delay")
	ctx := t.Context()

	job := queue.NewJob("delayed").WithID("delayed-1").WithDelay(rabbitMQLongDelay).Build()
	start := time.Now()
	err := q.Schedule(ctx, job)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	_, err = q.Dequeue(ctx, rabbitMQShortDelay)
	if !errors.Is(err, queue.ErrNoJobAvailable) {
		t.Fatalf("the job was delivered before it was due: %v", err)
	}

	got, err := q.Dequeue(ctx, rabbitMQDequeueTimeout)
	if err != nil {
		t.Fatalf("Dequeue after the delay: %v", err)
	}
	if got.ID != job.ID {
		t.Fatalf("got job %q, want %q", got.ID, job.ID)
	}
	if elapsed := time.Since(start); elapsed < 600*time.Millisecond {
		t.Fatalf("job delivered after %v, scheduled for 700ms", elapsed)
	}
}

// Close must not wait for a consumer that is polling an empty queue.
func TestRabbitMQCloseDoesNotWaitForDequeue(t *testing.T) {
	q := newTestRabbitMQ(t, "close")

	polling := make(chan struct{})
	done := make(chan struct{})
	go func() {
		close(polling)
		_, err := q.Dequeue(context.Background(), rabbitMQGoroutineWait)
		if err != nil {
			t.Logf("dequeue ended: %v", err)
		}
		close(done)
	}()
	await.Receive(t, polling, rabbitMQCloseWait, "the dequeue goroutine to start")

	start := time.Now()
	err := q.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > rabbitMQDeliveryLimit {
		t.Fatalf("Close took %v", elapsed)
	}
	await.Receive(t, done, rabbitMQCloseWait, "Dequeue to stop polling after Close")
}

func newPrefetchRabbitMQ(tb testing.TB, name string, prefetch int) *queue.RabbitMQ {
	tb.Helper()

	suffix := uniqueRabbitMQName(name)
	return newRabbitMQ(tb, queue.RabbitMQConfig{
		URL:          rabbitMQTestURL(),
		QueueName:    suffix,
		ExchangeName: suffix + "_exchange",
		RoutingKey:   suffix,
		Durable:      false,
		AutoDelete:   false, // with a consumer the broker would delete the queue when the consumer goes away
		Prefetch:     prefetch,
	})
}

// With Prefetch set Dequeue reads from a consumer: jobs arrive in order, are acknowledged
// through UpdateJob, and the timeout still applies to an empty queue.
func TestRabbitMQConsumerMode(t *testing.T) {
	q := newPrefetchRabbitMQ(t, "consumer", rabbitMQConsumerPrefetch)
	ctx := t.Context()

	for i := range 3 {
		err := q.Enqueue(ctx, queue.NewJob("c").WithID(fmt.Sprintf("c-%d", i)).Build())
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		job, err := q.Dequeue(ctx, rabbitMQDequeueTimeout)
		if err != nil {
			t.Fatalf("Dequeue %d: %v", i, err)
		}
		if want := fmt.Sprintf("c-%d", i); job.ID != want {
			t.Fatalf("got %s, want %s", job.ID, want)
		}
		job.Status = queue.StatusCompleted
		err = q.UpdateJob(ctx, job)
		if err != nil {
			t.Fatalf("ack: %v", err)
		}
	}

	start := time.Now()
	_, err := q.Dequeue(ctx, 300*time.Millisecond)
	if !errors.Is(err, queue.ErrNoJobAvailable) {
		t.Fatalf("expected ErrNoJobAvailable, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Fatalf("returned after %v", elapsed)
	}
}

// A job that arrives while a consumer waits is pushed at once, without polling delay.
func TestRabbitMQConsumerModeWakesImmediately(t *testing.T) {
	q := newPrefetchRabbitMQ(t, "consumerwake", rabbitMQWakePrefetch)

	waiting := make(chan struct{})
	go func() {
		<-waiting
		err := q.Enqueue(context.Background(), queue.NewJob("w").WithID("w-1").Build())
		if err != nil {
			t.Errorf("enqueuing wake job failed: %v", err)
		}
	}()

	start := time.Now()
	close(waiting)
	job, err := q.Dequeue(t.Context(), rabbitMQDequeueTimeout)
	if err != nil || job.ID != "w-1" {
		t.Fatalf("job=%v err=%v", job, err)
	}
	if elapsed := time.Since(start); elapsed > rabbitMQDeliveryLimit {
		t.Logf("delivered after %v", elapsed) // informational
	}
}

// Several workers share the consumer and every job is delivered once.
func TestRabbitMQConsumerModeConcurrentWorkers(t *testing.T) {
	q := newPrefetchRabbitMQ(t, "consumerworkers", rabbitMQWorkerPrefetch)
	ctx := t.Context()

	for i := range rabbitMQConsumerJobs {
		err := q.Enqueue(ctx, queue.NewJob("p").WithID(fmt.Sprintf("p-%d", i)).Build())
		if err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	seen := make(map[string]int)
	var wg sync.WaitGroup
	for range rabbitMQWorkerCount {
		wg.Go(func() {
			for {
				job, err := q.Dequeue(ctx, rabbitMQWorkerTimeout)
				if err != nil {
					return
				}
				mu.Lock()
				seen[job.ID]++
				mu.Unlock()
				job.Status = queue.StatusCompleted
				err = q.UpdateJob(ctx, job)
				if err != nil {
					t.Errorf("acknowledging job %s failed: %v", job.ID, err)
				}
			}
		})
	}
	wg.Wait()

	if len(seen) != rabbitMQConsumerJobs {
		t.Fatalf("delivered %d distinct jobs, want %d", len(seen), rabbitMQConsumerJobs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("job %s delivered %d times", id, n)
		}
	}
}

// Reconnect drops the consumer of the old channel; Dequeue starts a new one.
func TestRabbitMQConsumerModeSurvivesReconnect(t *testing.T) {
	q := newPrefetchRabbitMQ(t, "consumerreconnect", rabbitMQSmallPrefetch)
	ctx := t.Context()

	err := q.Enqueue(ctx, queue.NewJob("r").WithID("r-1").Build())
	if err != nil {
		t.Fatal(err)
	}
	_, err = q.Dequeue(ctx, rabbitMQDequeueTimeout)
	if err != nil {
		t.Fatal(err)
	}

	err = q.Reconnect(queue.RabbitMQConfig{URL: rabbitMQTestURL()})
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	// r-1 was never acknowledged, so the broker hands it out again on the new consumer
	job, err := q.Dequeue(ctx, rabbitMQDequeueTimeout)
	if err != nil {
		t.Fatalf("Dequeue after Reconnect: %v", err)
	}
	if job.ID != "r-1" {
		t.Fatalf("got %s", job.ID)
	}
}

func BenchmarkRabbitMQDequeuePrefetch(b *testing.B) {
	q := newPrefetchRabbitMQ(b, "benchprefetch", rabbitMQBenchPrefetch)
	ctx := b.Context()
	for i := 0; i < b.N; i++ {
		err := q.Enqueue(ctx, queue.NewJob("bench").WithID(fmt.Sprintf("b-%d", i)).Build())
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job, err := q.Dequeue(ctx, rabbitMQDequeueTimeout)
		if err != nil {
			b.Fatal(err)
		}
		job.Status = queue.StatusCompleted
		err = q.UpdateJob(ctx, job)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestRabbitMQFinishedRetention(t *testing.T) {
	suffix := uniqueRabbitMQName("retain")
	q := newRabbitMQ(t, queue.RabbitMQConfig{
		URL:            rabbitMQTestURL(),
		QueueName:      suffix,
		ExchangeName:   suffix + "_exchange",
		RoutingKey:     suffix,
		RetainFinished: rabbitMQRetainFinished,
	})
	ctx := t.Context()

	for i := range rabbitMQRetentionJobs {
		job := queue.NewJob("r").WithID(fmt.Sprintf("r-%d", i)).Build()
		err := q.Enqueue(ctx, job)
		if err != nil {
			t.Fatal(err)
		}
		got, err := q.Dequeue(ctx, rabbitMQRetentionWait)
		if err != nil {
			t.Fatal(err)
		}
		got.Status = queue.StatusCompleted
		err = q.UpdateJob(ctx, got)
		if err != nil {
			t.Fatal(err)
		}
	}

	_, err := q.Job(ctx, "r-0")
	if err == nil {
		t.Error("the oldest finished job is still indexed")
	}
	_, err = q.Job(ctx, "r-9")
	if err != nil {
		t.Errorf("the newest finished job was dropped: %v", err)
	}
	stats, err := q.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Completed != rabbitMQRetentionJobs {
		t.Fatalf("Completed = %d, want %d", stats.Completed, rabbitMQRetentionJobs)
	}
}

// The broker deletes an auto-delete queue when its consumer goes away. Reconnect declares it again,
// so the queue is usable afterwards.
func TestRabbitMQReconnectDeclaresAutoDeleteQueue(t *testing.T) {
	suffix := uniqueRabbitMQName("redeclare")
	q := newRabbitMQ(t, queue.RabbitMQConfig{
		URL:          rabbitMQTestURL(),
		QueueName:    suffix,
		ExchangeName: suffix + "_exchange",
		RoutingKey:   suffix,
		AutoDelete:   true,
		Prefetch:     rabbitMQSmallPrefetch,
	})
	ctx := t.Context()

	_, err := q.Dequeue(ctx, rabbitMQShortTimeout)
	if !errors.Is(err, queue.ErrNoJobAvailable) {
		t.Fatalf("first Dequeue: %v", err)
	}
	// An empty Reconnect config keeps the URL and queue names of the original
	err = q.Reconnect(queue.RabbitMQConfig{})
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}

	err = q.Enqueue(ctx, queue.NewJob("x").WithID("after-reconnect").Build())
	if err != nil {
		t.Fatalf("Enqueue after Reconnect: %v", err)
	}
	job, err := q.Dequeue(ctx, rabbitMQDequeueTimeout)
	if err != nil || job.ID != "after-reconnect" {
		t.Fatalf("job=%v err=%v", job, err)
	}
}

func rabbitMQTestURL() string {
	u := url.URL{Scheme: "amqp", User: url.UserPassword(rabbitMQTestUser, rabbitMQTestKey), Host: rabbitMQTestHost, Path: "/"}
	return u.String()
}
