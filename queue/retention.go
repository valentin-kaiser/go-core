package queue

// defaultFinishedRetention is how many finished jobs a queue keeps for GetJob and GetJobs
const defaultFinishedRetention = 10000

// finishedJobs remembers the order in which jobs finished and drops the oldest ones from the
// queue's job index once more than limit are kept. A queue that is used for years would
// otherwise hold every job it ever ran. The jobs themselves are not lost, only the index entry
// used by GetJob; the counts of dropped jobs are kept so GetStats still adds up.
//
// It has no lock of its own: the owning queue calls it with its job lock held.
type finishedJobs struct {
	limit   int // 0 or less keeps everything
	ring    []string
	start   int
	count   int
	evicted map[Status]int64
}

func newFinishedJobs(limit int) finishedJobs {
	return finishedJobs{limit: limit, evicted: make(map[Status]int64)}
}

// retentionLimit maps a configured value to a limit: 0 selects the default, below 0 keeps everything
func retentionLimit(configured int) int {
	switch {
	case configured == 0:
		return defaultFinishedRetention
	case configured < 0:
		return 0
	default:
		return configured
	}
}

// isFinished reports whether the status is one a job does not leave on its own
func isFinished(s Status) bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusDeadLetter
}

// record notes that the job reached a finished status and drops the oldest finished
// job from the index if the limit is exceeded.
func (f *finishedJobs) record(jobs map[string]*Job, job *Job) {
	if f.limit <= 0 || !isFinished(job.Status) {
		return
	}

	if f.ring == nil {
		f.ring = make([]string, f.limit)
	}
	if f.count == f.limit {
		oldest := f.ring[f.start]
		f.ring[f.start] = job.ID
		f.start = (f.start + 1) % f.limit
		// The job may have been retried or deleted since, in which case it stays
		if old, ok := jobs[oldest]; ok && isFinished(old.Status) {
			f.evicted[old.Status]++
			delete(jobs, oldest)
		}
		return
	}
	f.ring[(f.start+f.count)%f.limit] = job.ID
	f.count++
}

// addTo adds the jobs dropped from the index to the statistics
func (f *finishedJobs) addTo(stats *Stats) {
	for status, n := range f.evicted {
		stats.TotalJobs += n
		switch status {
		case StatusCompleted:
			stats.Completed += n
			stats.JobsProcessed += n
		case StatusFailed:
			stats.Failed += n
			stats.JobsFailed += n
		case StatusDeadLetter:
			stats.DeadLetter += n
		}
	}
}
