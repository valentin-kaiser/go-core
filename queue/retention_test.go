package queue_test

import (
	"fmt"
	"testing"

	"github.com/valentin-kaiser/go-core/v2/queue"
)

const (
	finishedRetentionLimit = 5
	retriedRetentionLimit  = 2
	ringRetentionLimit     = 3
	finishedJobCount       = 20
	retriedFillerJobCount  = 5
	unlimitedJobCount      = 50
)

func finish(t *testing.T, q *queue.MemoryQueue, id string, status queue.Status) {
	t.Helper()
	ctx := t.Context()
	job := queue.NewJob("r").WithID(id).Build()
	err := q.Enqueue(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = status
	err = q.UpdateJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
}

// Finished jobs beyond the limit leave the index but stay in the statistics.
func TestMemoryQueueFinishedRetention(t *testing.T) {
	q := queue.NewMemoryQueue().WithFinishedRetention(finishedRetentionLimit)
	ctx := t.Context()

	for i := range finishedJobCount {
		finish(t, q, fmt.Sprintf("done-%d", i), queue.StatusCompleted)
	}
	finish(t, q, "failed-1", queue.StatusFailed)
	finish(t, q, "dead-1", queue.StatusDeadLetter)

	_, err := q.GetJob(ctx, "done-0")
	if err == nil {
		t.Error("the oldest finished job is still indexed")
	}
	_, err = q.GetJob(ctx, "dead-1")
	if err != nil {
		t.Errorf("the newest finished job was dropped: %v", err)
	}

	stats, err := q.GetStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Completed != finishedJobCount || stats.Failed != 1 || stats.DeadLetter != 1 || stats.TotalJobs != finishedJobCount+2 {
		t.Fatalf("stats do not add up: %+v", stats)
	}
}

// A job that is retried after it failed must not be dropped while it is active again.
func TestMemoryQueueRetentionKeepsRetriedJobs(t *testing.T) {
	q := queue.NewMemoryQueue().WithFinishedRetention(retriedRetentionLimit)
	ctx := t.Context()

	job := queue.NewJob("r").WithID("retry-me").Build()
	err := q.Enqueue(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = queue.StatusFailed
	err = q.UpdateJob(ctx, job)
	if err != nil {
		t.Error(err)
	}
	job.Status = queue.StatusRetrying
	err = q.UpdateJob(ctx, job)
	if err != nil {
		t.Error(err)
	}

	for i := range retriedFillerJobCount {
		finish(t, q, fmt.Sprintf("other-%d", i), queue.StatusCompleted)
	}
	_, err = q.GetJob(ctx, "retry-me")
	if err != nil {
		t.Fatalf("an active job was dropped: %v", err)
	}
}

func TestMemoryQueueUnlimitedRetention(t *testing.T) {
	q := queue.NewMemoryQueue().WithFinishedRetention(0)
	for i := range unlimitedJobCount {
		finish(t, q, fmt.Sprintf("done-%d", i), queue.StatusCompleted)
	}
	_, err := q.GetJob(t.Context(), "done-0")
	if err != nil {
		t.Fatalf("a job was dropped although retention is unlimited: %v", err)
	}
}

// A job that finishes, is retried and finishes again has a newer ring entry. Evicting the older
// entry must not drop the job while that newer entry is still retained.
func TestMemoryQueueRetentionKeepsNewerIncarnation(t *testing.T) {
	q := queue.NewMemoryQueue().WithFinishedRetention(ringRetentionLimit)
	ctx := t.Context()

	job := queue.NewJob("r").WithID("again").Build()
	err := q.Enqueue(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = queue.StatusFailed
	err = q.UpdateJob(ctx, job)
	if err != nil { // entry 1
		t.Fatal(err)
	}
	finish(t, q, "a", queue.StatusCompleted) // entry 2
	job.Status = queue.StatusRunning
	err = q.UpdateJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	job.Status = queue.StatusCompleted
	err = q.UpdateJob(ctx, job)
	if err != nil { // entry 3, the ring is full
		t.Fatal(err)
	}
	finish(t, q, "b", queue.StatusCompleted) // evicts entry 1

	_, err = q.GetJob(ctx, "again")
	if err != nil {
		t.Fatalf("the newer completion was dropped by its older ring entry: %v", err)
	}
	finish(t, q, "c", queue.StatusCompleted) // evicts entry 2
	finish(t, q, "d", queue.StatusCompleted) // evicts entry 3, the job's own
	_, err = q.GetJob(ctx, "again")
	if err == nil {
		t.Fatal("the job is still indexed after its own entry was evicted")
	}
}
