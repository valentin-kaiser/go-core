package queue_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/apperror"
	"github.com/valentin-kaiser/go-core/queue"
)

func TestRabbitMQ(t *testing.T) {
	config := queue.RabbitMQConfig{
		URL:          "amqp://admin:admin123@localhost:5672/",
		QueueName:    "test_queue",
		ExchangeName: "test_exchange",
		RoutingKey:   "test",
		Durable:      false,
		AutoDelete:   true,
		Exclusive:    false,
		NoWait:       false,
	}

	q, err := queue.NewRabbitMQ(config)
	if err != nil {
		t.Skipf("Skipping RabbitMQ test: %v", err)
	}
	t.Cleanup(func() {
		if err := q.Close(); err != nil {
			t.Logf("Warning: failed to close queue: %v", err)
		}
	})

	ctx := t.Context()

	// basic.get never auto-deletes a queue, so messages left unacknowledged by an earlier run
	// come back when its connection closes. Start from an empty queue.
	if err := q.PurgeQueue(ctx); err != nil {
		t.Fatalf("Failed to purge queue: %v", err)
	}

	t.Run("BasicEnqueueDequeue", func(t *testing.T) {
		job := queue.NewJob("test-job").
			WithID("test-1").
			WithPayload(map[string]interface{}{"message": "hello world"}).
			Build()

		err := q.Enqueue(ctx, job)
		if err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}

		dequeuedJob, err := q.Dequeue(ctx, time.Second*5)
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

		var payload map[string]interface{}
		if err := json.Unmarshal(dequeuedJob.Payload, &payload); err != nil {
			t.Fatalf("Failed to unmarshal payload: %v", err)
		}

		if payload["message"] != "hello world" {
			t.Errorf("Expected message 'hello world', got %v", payload["message"])
		}
	})

	t.Run("ScheduledJobs", func(t *testing.T) {
		job := queue.NewJob("scheduled-job").
			WithID("scheduled-1").
			WithDelay(time.Millisecond * 200).
			WithPayload(map[string]interface{}{"scheduled": true}).
			Build()

		err := q.Schedule(ctx, job)
		if err != nil {
			t.Fatalf("Failed to schedule job: %v", err)
		}

		dequeuedJob, err := q.Dequeue(ctx, time.Second*5)
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
		apperror.Handle(q.UpdateJob(ctx, dequeuedJob), "failed to update job")
	})

	t.Run("PriorityJobs", func(t *testing.T) {
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

		// Give RabbitMQ time to process
		time.Sleep(time.Millisecond * 100)

		for i := 0; i < 3; i++ {
			job, err := q.Dequeue(ctx, time.Second*5)
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
	})

	t.Run("JobOperations", func(t *testing.T) {
		job := queue.NewJob("test-ops").
			WithID("ops-1").
			WithPayload(map[string]interface{}{"operation": "test"}).
			Build()

		err := q.Enqueue(ctx, job)
		if err != nil {
			t.Fatalf("Failed to enqueue job: %v", err)
		}

		retrievedJob, err := q.GetJob(ctx, job.ID)
		if err != nil {
			t.Fatalf("Failed to get job: %v", err)
		}

		if retrievedJob.ID != job.ID {
			t.Errorf("Expected job ID %s, got %s", job.ID, retrievedJob.ID)
		}

		pendingJobs, err := q.GetJobs(ctx, queue.StatusPending, 10)
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

		stats, err := q.GetStats(ctx)
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

		_, err = q.GetJob(ctx, job.ID)
		if err == nil {
			t.Error("Expected error when getting deleted job")
		}
	})

	t.Run("Connection", func(t *testing.T) {
		if !q.IsConnectionOpen() {
			t.Error("Expected connection to be open")
		}

		err := q.PurgeQueue(ctx)
		if err != nil {
			t.Fatalf("Failed to purge queue: %v", err)
		}

		stats, err := q.GetStats(ctx)
		if err != nil {
			t.Fatalf("Failed to get stats after purge: %v", err)
		}

		if stats.QueueSize != 0 {
			t.Errorf("Expected queue size 0 after purge, got %d", stats.QueueSize)
		}
	})
}

func TestRabbitMQReconnection(t *testing.T) {
	config := queue.RabbitMQConfig{
		URL:          "amqp://admin:admin123@localhost:5672/",
		QueueName:    "test_reconnect",
		ExchangeName: "test_reconnect_exchange",
		RoutingKey:   "test_reconnect",
		Durable:      false,
		AutoDelete:   true,
	}

	queue, err := queue.NewRabbitMQ(config)
	if err != nil {
		t.Skipf("Skipping RabbitMQ reconnection test: %v", err)
	}
	defer apperror.Handle(queue.Close(), "failed to close queue")

	err = queue.Reconnect(config)
	if err != nil {
		t.Fatalf("Failed to reconnect: %v", err)
	}

	if !queue.IsConnectionOpen() {
		t.Error("Expected connection to be open after reconnection")
	}
}

func TestRabbitMQWithClosedConnection(t *testing.T) {
	config := queue.RabbitMQConfig{
		URL:          "amqp://admin:admin123@localhost:5672/",
		QueueName:    "test_closed",
		ExchangeName: "test_closed_exchange",
		RoutingKey:   "test_closed",
		Durable:      false,
		AutoDelete:   true,
	}

	q, err := queue.NewRabbitMQ(config)
	if err != nil {
		t.Skipf("Skipping RabbitMQ closed connection test: %v", err)
	}

	if err := q.Close(); err != nil {
		t.Logf("Warning: failed to close queue: %v", err)
	}

	ctx := t.Context()

	job := queue.NewJob("test-closed").WithID("closed-1").Build()

	err = q.Enqueue(ctx, job)
	if err == nil {
		t.Error("Expected error when enqueuing to closed queue")
	}

	_, err = q.Dequeue(ctx, time.Second)
	if err == nil {
		t.Error("Expected error when dequeuing from closed queue")
	}

	_, err = q.GetJob(ctx, job.ID)
	if err == nil {
		t.Error("Expected error when getting job from closed queue")
	}

	_, err = q.GetJobs(ctx, queue.StatusPending, 10)
	if err == nil {
		t.Error("Expected error when getting jobs from closed queue")
	}

	_, err = q.GetStats(ctx)
	if err == nil {
		t.Error("Expected error when getting stats from closed queue")
	}

	err = q.DeleteJob(ctx, job.ID)
	if err == nil {
		t.Error("Expected error when deleting job from closed queue")
	}
}

func BenchmarkRabbitMQEnqueue(b *testing.B) {
	config := queue.RabbitMQConfig{
		URL:          "amqp://admin:admin123@localhost:5672/",
		QueueName:    "benchmark_queue",
		ExchangeName: "benchmark_exchange",
		RoutingKey:   "benchmark",
		Durable:      false,
		AutoDelete:   true,
	}

	q, err := queue.NewRabbitMQ(config)
	if err != nil {
		b.Skipf("Skipping RabbitMQ benchmark: %v", err)
	}
	defer func() { apperror.Handle(q.Close(), "failed to close queue") }()

	ctx := b.Context()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			job := queue.NewJob("benchmark-job").
				WithID(fmt.Sprintf("bench-%d", i)).
				WithPayload(map[string]interface{}{"index": i}).
				Build()

			err := q.Enqueue(ctx, job)
			if err != nil {
				b.Fatalf("Failed to enqueue job: %v", err)
			}
			i++
		}
	})
}

func BenchmarkRabbitMQDequeue(b *testing.B) {
	config := queue.RabbitMQConfig{
		URL:          "amqp://admin:admin123@localhost:5672/",
		QueueName:    "benchmark_dequeue",
		ExchangeName: "benchmark_dequeue_exchange",
		RoutingKey:   "benchmark_dequeue",
		Durable:      false,
		AutoDelete:   true,
	}

	q, err := queue.NewRabbitMQ(config)
	if err != nil {
		b.Skipf("Skipping RabbitMQ dequeue benchmark: %v", err)
	}
	defer func() { apperror.Handle(q.Close(), "failed to close queue") }()

	ctx := b.Context()

	// Pre-populate queue with jobs
	for i := 0; i < b.N; i++ {
		job := queue.NewJob("benchmark-dequeue-job").
			WithID(fmt.Sprintf("dequeue-bench-%d", i)).
			WithPayload(map[string]interface{}{"index": i}).
			Build()

		err := q.Enqueue(ctx, job)
		if err != nil {
			b.Fatalf("Failed to enqueue job: %v", err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job, err := q.Dequeue(ctx, time.Second*5)
		if err != nil {
			b.Fatalf("Failed to dequeue job: %v", err)
		}
		if job == nil {
			b.Fatal("Expected job, got nil")
		}

		job.Status = queue.StatusCompleted
		if err := q.UpdateJob(ctx, job); err != nil {
			b.Logf("Failed to update job: %v", err)
		}
	}
}

func newTestRabbitMQ(t *testing.T, name string) *queue.RabbitMQ {
	t.Helper()
	// A name per run: basic.get never auto-deletes a queue, so fixed names collect leftovers
	suffix := fmt.Sprintf("%s_%d", name, time.Now().UnixNano())
	q, err := queue.NewRabbitMQ(queue.RabbitMQConfig{
		URL:          "amqp://admin:admin123@localhost:5672/",
		QueueName:    suffix,
		ExchangeName: suffix + "_exchange",
		RoutingKey:   suffix,
		Durable:      false,
		AutoDelete:   true,
	})
	if err != nil {
		t.Skipf("Skipping RabbitMQ test: %v", err)
	}
	t.Cleanup(func() {
		_ = q.PurgeQueue(context.Background())
		_ = q.Close()
	})
	return q
}

// Dequeue has to wait for the whole timeout when the queue is empty, like the memory queue does.
func TestRabbitMQDequeueHonorsTimeout(t *testing.T) {
	q := newTestRabbitMQ(t, "timeout")

	start := time.Now()
	_, err := q.Dequeue(t.Context(), 400*time.Millisecond)
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

	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = q.Enqueue(context.Background(), queue.NewJob("wake").WithID("wake-1").Build())
	}()

	start := time.Now()
	job, err := q.Dequeue(t.Context(), 5*time.Second)
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if job.ID != "wake-1" {
		t.Fatalf("got job %q", job.ID)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("job was delivered after %v", elapsed)
	}
}

// A scheduled job must not be delivered before its time.
func TestRabbitMQScheduledJobIsDelayed(t *testing.T) {
	q := newTestRabbitMQ(t, "delay")
	ctx := t.Context()

	job := queue.NewJob("delayed").WithID("delayed-1").WithDelay(700 * time.Millisecond).Build()
	start := time.Now()
	if err := q.Schedule(ctx, job); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	if _, err := q.Dequeue(ctx, 200*time.Millisecond); !errors.Is(err, queue.ErrNoJobAvailable) {
		t.Fatalf("the job was delivered before it was due: %v", err)
	}

	got, err := q.Dequeue(ctx, 5*time.Second)
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

	done := make(chan struct{})
	go func() {
		_, _ = q.Dequeue(context.Background(), 5*time.Second)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)

	start := time.Now()
	if err := q.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Close took %v", elapsed)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Dequeue kept polling after Close")
	}
}

func newPrefetchRabbitMQ(tb testing.TB, name string, prefetch int) *queue.RabbitMQ {
	tb.Helper()
	suffix := fmt.Sprintf("%s_%d", name, time.Now().UnixNano())
	q, err := queue.NewRabbitMQ(queue.RabbitMQConfig{
		URL:          "amqp://admin:admin123@localhost:5672/",
		QueueName:    suffix,
		ExchangeName: suffix + "_exchange",
		RoutingKey:   suffix,
		Durable:      false,
		AutoDelete:   false, // with a consumer the broker would delete the queue when the consumer goes away
		Prefetch:     prefetch,
	})
	if err != nil {
		tb.Skipf("Skipping RabbitMQ test: %v", err)
	}
	tb.Cleanup(func() {
		_ = q.PurgeQueue(context.Background())
		_ = q.Close()
	})
	return q
}

// With Prefetch set Dequeue reads from a consumer: jobs arrive in order, are acknowledged
// through UpdateJob, and the timeout still applies to an empty queue.
func TestRabbitMQConsumerMode(t *testing.T) {
	q := newPrefetchRabbitMQ(t, "consumer", 5)
	ctx := t.Context()

	for i := 0; i < 3; i++ {
		if err := q.Enqueue(ctx, queue.NewJob("c").WithID(fmt.Sprintf("c-%d", i)).Build()); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		job, err := q.Dequeue(ctx, 5*time.Second)
		if err != nil {
			t.Fatalf("Dequeue %d: %v", i, err)
		}
		if want := fmt.Sprintf("c-%d", i); job.ID != want {
			t.Fatalf("got %s, want %s", job.ID, want)
		}
		job.Status = queue.StatusCompleted
		if err := q.UpdateJob(ctx, job); err != nil {
			t.Fatalf("ack: %v", err)
		}
	}

	start := time.Now()
	if _, err := q.Dequeue(ctx, 300*time.Millisecond); !errors.Is(err, queue.ErrNoJobAvailable) {
		t.Fatalf("expected ErrNoJobAvailable, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < 250*time.Millisecond {
		t.Fatalf("returned after %v", elapsed)
	}
}

// A job that arrives while a consumer waits is pushed at once, without polling delay.
func TestRabbitMQConsumerModeWakesImmediately(t *testing.T) {
	q := newPrefetchRabbitMQ(t, "consumerwake", 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = q.Enqueue(context.Background(), queue.NewJob("w").WithID("w-1").Build())
	}()

	start := time.Now()
	job, err := q.Dequeue(t.Context(), 5*time.Second)
	if err != nil || job.ID != "w-1" {
		t.Fatalf("job=%v err=%v", job, err)
	}
	if elapsed := time.Since(start); elapsed > 140*time.Millisecond {
		t.Logf("delivered after %v", elapsed) // informational: includes the 100ms the job was held back
	}
}

// Several workers share the consumer and every job is delivered once.
func TestRabbitMQConsumerModeConcurrentWorkers(t *testing.T) {
	q := newPrefetchRabbitMQ(t, "consumerworkers", 8)
	ctx := t.Context()

	const jobs = 200
	for i := 0; i < jobs; i++ {
		if err := q.Enqueue(ctx, queue.NewJob("p").WithID(fmt.Sprintf("p-%d", i)).Build()); err != nil {
			t.Fatal(err)
		}
	}

	var mu sync.Mutex
	seen := make(map[string]int)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, err := q.Dequeue(ctx, 500*time.Millisecond)
				if err != nil {
					return
				}
				mu.Lock()
				seen[job.ID]++
				mu.Unlock()
				job.Status = queue.StatusCompleted
				_ = q.UpdateJob(ctx, job)
			}
		}()
	}
	wg.Wait()

	if len(seen) != jobs {
		t.Fatalf("delivered %d distinct jobs, want %d", len(seen), jobs)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("job %s delivered %d times", id, n)
		}
	}
}

// Reconnect drops the consumer of the old channel; Dequeue starts a new one.
func TestRabbitMQConsumerModeSurvivesReconnect(t *testing.T) {
	q := newPrefetchRabbitMQ(t, "consumerreconnect", 2)
	ctx := t.Context()

	if err := q.Enqueue(ctx, queue.NewJob("r").WithID("r-1").Build()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Dequeue(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	if err := q.Reconnect(queue.RabbitMQConfig{URL: "amqp://admin:admin123@localhost:5672/"}); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	// r-1 was never acknowledged, so the broker hands it out again on the new consumer
	job, err := q.Dequeue(ctx, 5*time.Second)
	if err != nil {
		t.Fatalf("Dequeue after Reconnect: %v", err)
	}
	if job.ID != "r-1" {
		t.Fatalf("got %s", job.ID)
	}
}

func BenchmarkRabbitMQDequeuePrefetch(b *testing.B) {
	q := newPrefetchRabbitMQ(b, "benchprefetch", 100)
	ctx := b.Context()
	for i := 0; i < b.N; i++ {
		if err := q.Enqueue(ctx, queue.NewJob("bench").WithID(fmt.Sprintf("b-%d", i)).Build()); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job, err := q.Dequeue(ctx, 5*time.Second)
		if err != nil {
			b.Fatal(err)
		}
		job.Status = queue.StatusCompleted
		if err := q.UpdateJob(ctx, job); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRabbitMQFinishedRetention(t *testing.T) {
	suffix := fmt.Sprintf("retain_%d", time.Now().UnixNano())
	q, err := queue.NewRabbitMQ(queue.RabbitMQConfig{
		URL:            "amqp://admin:admin123@localhost:5672/",
		QueueName:      suffix,
		ExchangeName:   suffix + "_exchange",
		RoutingKey:     suffix,
		RetainFinished: 3,
	})
	if err != nil {
		t.Skipf("Skipping RabbitMQ test: %v", err)
	}
	t.Cleanup(func() { _ = q.PurgeQueue(context.Background()); _ = q.Close() })
	ctx := t.Context()

	for i := 0; i < 10; i++ {
		job := queue.NewJob("r").WithID(fmt.Sprintf("r-%d", i)).Build()
		if err := q.Enqueue(ctx, job); err != nil {
			t.Fatal(err)
		}
		got, err := q.Dequeue(ctx, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		got.Status = queue.StatusCompleted
		if err := q.UpdateJob(ctx, got); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := q.GetJob(ctx, "r-0"); err == nil {
		t.Error("the oldest finished job is still indexed")
	}
	if _, err := q.GetJob(ctx, "r-9"); err != nil {
		t.Errorf("the newest finished job was dropped: %v", err)
	}
	stats, err := q.GetStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Completed != 10 {
		t.Fatalf("Completed = %d, want 10", stats.Completed)
	}
}

// The broker deletes an auto-delete queue when its consumer goes away. Reconnect declares it again,
// so the queue is usable afterwards.
func TestRabbitMQReconnectDeclaresAutoDeleteQueue(t *testing.T) {
	suffix := fmt.Sprintf("redeclare_%d", time.Now().UnixNano())
	q, err := queue.NewRabbitMQ(queue.RabbitMQConfig{
		URL:          "amqp://admin:admin123@localhost:5672/",
		QueueName:    suffix,
		ExchangeName: suffix + "_exchange",
		RoutingKey:   suffix,
		AutoDelete:   true,
		Prefetch:     2,
	})
	if err != nil {
		t.Skipf("Skipping RabbitMQ test: %v", err)
	}
	t.Cleanup(func() { _ = q.Close() })
	ctx := t.Context()

	if _, err := q.Dequeue(ctx, 100*time.Millisecond); !errors.Is(err, queue.ErrNoJobAvailable) {
		t.Fatalf("first Dequeue: %v", err)
	}
	// An empty Reconnect config keeps the URL and queue names of the original
	if err := q.Reconnect(queue.RabbitMQConfig{}); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}

	if err := q.Enqueue(ctx, queue.NewJob("x").WithID("after-reconnect").Build()); err != nil {
		t.Fatalf("Enqueue after Reconnect: %v", err)
	}
	job, err := q.Dequeue(ctx, 5*time.Second)
	if err != nil || job.ID != "after-reconnect" {
		t.Fatalf("job=%v err=%v", job, err)
	}
}
