package queue

import (
	"context"
	"strconv"
	"testing"
	"time"
)

func benchScheduler(b *testing.B, n int) *TaskScheduler {
	b.Helper()
	s := NewTaskScheduler()
	for i := 0; i < n; i++ {
		err := s.RegisterIntervalTaskWithOptions("task-"+strconv.Itoa(i), time.Hour, func(context.Context) error { return nil }, TaskOptions{})
		if err != nil {
			b.Fatal(err)
		}
	}
	return s
}

// One scheduler tick with nothing due: the cost of scanning all tasks under their locks.
func BenchmarkSchedulerCheckIdle1000(b *testing.B) {
	s := benchScheduler(b, 1000)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.checkAndRunTasks(ctx)
	}
}

func BenchmarkSchedulerRunTask(b *testing.B) {
	s := benchScheduler(b, 1)
	task, err := s.GetTask("task-0")
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.workerWg.Add(1)
		s.runTask(ctx, task)
	}
}

func BenchmarkSchedulerGetTasks1000(b *testing.B) {
	s := benchScheduler(b, 1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.GetTasks()
	}
}
