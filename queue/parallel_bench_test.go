package queue_test

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/valentin-kaiser/go-core/v2/queue"
)

// Nothing consumes the jobs, so the queue grows during the run and a large part of the time is
// garbage collection and map growth, not the queue lock. BenchmarkMemoryQueueEnqueueDequeueParallel
// below keeps the queue short and is the one to compare lock contention with.
func BenchmarkMemoryQueueEnqueueParallel(b *testing.B) {
	ctx := b.Context()
	q := queue.NewMemoryQueue()
	var n atomic.Int64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			job := queue.NewJob("bench").WithID("job-" + strconv.FormatInt(n.Add(1), 10)).Build()
			err := q.Enqueue(ctx, job)
			if err != nil {
				b.Error(err)
			}
		}
	})
}

// Producers and consumers share the single queue mutex.
func BenchmarkMemoryQueueEnqueueDequeueParallel(b *testing.B) {
	ctx := b.Context()
	q := queue.NewMemoryQueue()
	var n atomic.Int64
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			job := queue.NewJob("bench").WithID("job-" + strconv.FormatInt(n.Add(1), 10)).Build()
			err := q.Enqueue(ctx, job)
			if err != nil {
				b.Error(err)
			}
			_, err = q.Dequeue(ctx, 10*time.Millisecond)
			if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				b.Error(err)
			}
		}
	})
}
