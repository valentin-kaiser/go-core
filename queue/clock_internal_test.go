package queue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

const (
	clockTaskName     = "report"
	clockInterval     = time.Minute
	clockCronEvery5   = "*/5 * * * *"
	clockCronInterval = 5 * time.Minute
	clockJobID        = "job-1"
	clockJobType      = "email"
	clockScheduleIn   = time.Hour
	clockRetryBackoff = 2.0
)

func newClockTaskScheduler(clock *fakeClock) *TaskScheduler {
	s := NewTaskScheduler()
	s.now = clock.Now
	return s
}

// runDueTasks runs one scheduler tick and waits for the tasks it started.
func runDueTasks(t *testing.T, s *TaskScheduler) {
	t.Helper()
	s.checkAndRunTasks(t.Context())
	s.workerWg.Wait()
}

func TestTaskScheduler_IntervalTaskDueFollowsClock(t *testing.T) {
	clock := newFakeClock()
	s := newClockTaskScheduler(clock)
	var runs atomic.Int32
	err := s.RegisterIntervalTaskWithOptions(clockTaskName, clockInterval, func(context.Context) error {
		runs.Add(1)
		return nil
	}, TaskOptions{})
	if err != nil {
		t.Fatalf("registering task failed: %v", err)
	}

	tests := []struct {
		name     string
		advance  time.Duration
		wantRuns int32
	}{
		{name: "not due at registration", advance: 0, wantRuns: 0},
		{name: "not due halfway", advance: clockInterval / 2, wantRuns: 0},
		{name: "due after the interval", advance: clockInterval, wantRuns: 1},
		{name: "not due right after a run", advance: 0, wantRuns: 1},
		{name: "due after the next interval", advance: clockInterval + time.Second, wantRuns: 2},
	}
	for _, tt := range tests {
		clock.Advance(tt.advance)
		runDueTasks(t, s)
		if got := runs.Load(); got != tt.wantRuns {
			t.Fatalf("%s: runs = %d, want %d", tt.name, got, tt.wantRuns)
		}
	}

	task, err := s.Task(clockTaskName)
	if err != nil {
		t.Fatalf("looking up task failed: %v", err)
	}
	if want := clock.Now().Add(clockInterval); !task.NextRun.Equal(want) {
		t.Fatalf("NextRun = %v, want %v", task.NextRun, want)
	}
}

func TestTaskScheduler_CronTaskDueFollowsClock(t *testing.T) {
	clock := newFakeClock()
	s := newClockTaskScheduler(clock)
	var runs atomic.Int32
	err := s.RegisterCronTask(clockTaskName, clockCronEvery5, func(context.Context) error {
		runs.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("registering task failed: %v", err)
	}

	task, err := s.Task(clockTaskName)
	if err != nil {
		t.Fatalf("looking up task failed: %v", err)
	}
	wantFirst := time.Date(2030, time.January, 2, 3, 5, 0, 0, time.UTC)
	if !task.NextRun.Equal(wantFirst) {
		t.Fatalf("NextRun = %v, want %v", task.NextRun, wantFirst)
	}

	runDueTasks(t, s)
	if got := runs.Load(); got != 0 {
		t.Fatalf("runs before the first slot = %d, want 0", got)
	}

	clock.Advance(clockCronInterval)
	runDueTasks(t, s)
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs after the first slot = %d, want 1", got)
	}
	task, err = s.Task(clockTaskName)
	if err != nil {
		t.Fatalf("looking up task failed: %v", err)
	}
	wantSecond := time.Date(2030, time.January, 2, 3, 10, 0, 0, time.UTC)
	if !task.NextRun.Equal(wantSecond) {
		t.Fatalf("NextRun after run = %v, want %v", task.NextRun, wantSecond)
	}
}

func TestTaskScheduler_DisabledTaskIsNotDue(t *testing.T) {
	clock := newFakeClock()
	s := newClockTaskScheduler(clock)
	var runs atomic.Int32
	err := s.RegisterIntervalTaskWithOptions(clockTaskName, clockInterval, func(context.Context) error {
		runs.Add(1)
		return nil
	}, TaskOptions{})
	if err != nil {
		t.Fatalf("registering task failed: %v", err)
	}
	err = s.DisableTask(clockTaskName)
	if err != nil {
		t.Fatalf("disabling task failed: %v", err)
	}

	clock.Advance(2 * clockInterval)
	runDueTasks(t, s)
	if got := runs.Load(); got != 0 {
		t.Fatalf("runs of a disabled task = %d, want 0", got)
	}

	err = s.EnableTask(clockTaskName)
	if err != nil {
		t.Fatalf("enabling task failed: %v", err)
	}
	runDueTasks(t, s)
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs after enabling = %d, want 1", got)
	}
}

func TestScheduler_EnqueueDueScheduledJobsFollowsClock(t *testing.T) {
	clock := newFakeClock()
	m := NewScheduler()
	m.now = clock.Now
	ctx := context.Background()
	job := &Job{
		ID:         clockJobID,
		Type:       clockJobType,
		Status:     StatusScheduled,
		ScheduleAt: clock.Now().Add(clockScheduleIn),
	}
	err := m.queue.Enqueue(ctx, job)
	if err != nil {
		t.Fatalf("enqueueing job failed: %v", err)
	}

	m.enqueueDueScheduledJobs(ctx)
	if job.Status != StatusScheduled {
		t.Fatalf("status before the schedule time = %v, want %v", job.Status, StatusScheduled)
	}

	clock.Advance(clockScheduleIn)
	m.enqueueDueScheduledJobs(ctx)
	if job.Status != StatusPending {
		t.Fatalf("status at the schedule time = %v, want %v", job.Status, StatusPending)
	}

	if !job.ScheduleAt.IsZero() {
		t.Fatalf("ScheduleAt = %v, want zero after enqueueing", job.ScheduleAt)
	}
}

func TestScheduler_RetryJobSetsRetryAtFromClock(t *testing.T) {
	clock := newFakeClock()
	m := NewScheduler()
	m.now = clock.Now
	// The re-enqueue timer is real, so keep it far away from the end of the test.
	m.retryDelay = clockScheduleIn
	m.retryBackoff = clockRetryBackoff
	job := &Job{ID: clockJobID, Type: clockJobType, Attempts: 1, MaxAttempts: 3}

	m.retryJob(context.Background(), job, &RetryableError{Err: errors.New("temporary failure")})

	if want := clock.Now().Add(clockScheduleIn); !job.RetryAt.Equal(want) {
		t.Fatalf("RetryAt = %v, want %v", job.RetryAt, want)
	}
	if !job.UpdatedAt.Equal(clock.Now()) {
		t.Fatalf("UpdatedAt = %v, want %v", job.UpdatedAt, clock.Now())
	}
	if job.Status != StatusRetrying {
		t.Fatalf("status = %v, want %v", job.Status, StatusRetrying)
	}
}
