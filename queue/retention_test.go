package queue_test

import (
	"fmt"
	"testing"

	"github.com/valentin-kaiser/go-core/queue"
)

func finish(t *testing.T, q *queue.MemoryQueue, id string, status queue.Status) {
	t.Helper()
	ctx := t.Context()
	job := queue.NewJob("r").WithID(id).Build()
	if err := q.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Status = status
	if err := q.UpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
}

// Finished jobs beyond the limit leave the index but stay in the statistics.
func TestMemoryQueueFinishedRetention(t *testing.T) {
	q := queue.NewMemoryQueue().WithFinishedRetention(5)
	ctx := t.Context()

	for i := 0; i < 20; i++ {
		finish(t, q, fmt.Sprintf("done-%d", i), queue.StatusCompleted)
	}
	finish(t, q, "failed-1", queue.StatusFailed)
	finish(t, q, "dead-1", queue.StatusDeadLetter)

	if _, err := q.GetJob(ctx, "done-0"); err == nil {
		t.Error("the oldest finished job is still indexed")
	}
	if _, err := q.GetJob(ctx, "dead-1"); err != nil {
		t.Errorf("the newest finished job was dropped: %v", err)
	}

	stats, err := q.GetStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Completed != 20 || stats.Failed != 1 || stats.DeadLetter != 1 || stats.TotalJobs != 22 {
		t.Fatalf("stats do not add up: %+v", stats)
	}
}

// A job that is retried after it failed must not be dropped while it is active again.
func TestMemoryQueueRetentionKeepsRetriedJobs(t *testing.T) {
	q := queue.NewMemoryQueue().WithFinishedRetention(2)
	ctx := t.Context()

	job := queue.NewJob("r").WithID("retry-me").Build()
	if err := q.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Status = queue.StatusFailed
	_ = q.UpdateJob(ctx, job)
	job.Status = queue.StatusRetrying
	_ = q.UpdateJob(ctx, job)

	for i := 0; i < 5; i++ {
		finish(t, q, fmt.Sprintf("other-%d", i), queue.StatusCompleted)
	}
	if _, err := q.GetJob(ctx, "retry-me"); err != nil {
		t.Fatalf("an active job was dropped: %v", err)
	}
}

func TestMemoryQueueUnlimitedRetention(t *testing.T) {
	q := queue.NewMemoryQueue().WithFinishedRetention(0)
	for i := 0; i < 50; i++ {
		finish(t, q, fmt.Sprintf("done-%d", i), queue.StatusCompleted)
	}
	if _, err := q.GetJob(t.Context(), "done-0"); err != nil {
		t.Fatalf("a job was dropped although retention is unlimited: %v", err)
	}
}

// A job that finishes, is retried and finishes again has a newer ring entry. Evicting the older
// entry must not drop the job while that newer entry is still retained.
func TestMemoryQueueRetentionKeepsNewerIncarnation(t *testing.T) {
	q := queue.NewMemoryQueue().WithFinishedRetention(3)
	ctx := t.Context()

	job := queue.NewJob("r").WithID("again").Build()
	if err := q.Enqueue(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Status = queue.StatusFailed
	if err := q.UpdateJob(ctx, job); err != nil { // entry 1
		t.Fatal(err)
	}
	finish(t, q, "a", queue.StatusCompleted) // entry 2
	job.Status = queue.StatusRunning
	if err := q.UpdateJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	job.Status = queue.StatusCompleted
	if err := q.UpdateJob(ctx, job); err != nil { // entry 3, the ring is full
		t.Fatal(err)
	}
	finish(t, q, "b", queue.StatusCompleted) // evicts entry 1

	if _, err := q.GetJob(ctx, "again"); err != nil {
		t.Fatalf("the newer completion was dropped by its older ring entry: %v", err)
	}
	finish(t, q, "c", queue.StatusCompleted) // evicts entry 2
	finish(t, q, "d", queue.StatusCompleted) // evicts entry 3, the job's own
	if _, err := q.GetJob(ctx, "again"); err == nil {
		t.Fatal("the job is still indexed after its own entry was evicted")
	}
}
