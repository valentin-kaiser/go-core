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
	ring    []finishedEntry
	start   int
	count   int
	seq     uint64 // last sequence number handed out, so the first one is 1
	evicted map[Status]int64
}

// finishedEntry is one completion in the ring. The ID alone is not enough: a job that is retried
// and finishes again has a newer entry, and the older one must not delete it.
type finishedEntry struct {
	id  string
	seq uint64
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
		f.ring = make([]finishedEntry, f.limit)
	}

	f.seq++
	job.finishSeq = f.seq
	entry := finishedEntry{id: job.ID, seq: f.seq}

	if f.count == f.limit {
		oldest := f.ring[f.start]
		f.ring[f.start] = entry
		f.start = (f.start + 1) % f.limit
		// The job may have been retried, deleted or replaced since, and may have finished again
		// with a newer entry that is still retained. Only the completion this entry stands for
		// is dropped.
		if old, ok := jobs[oldest.id]; ok && old.finishSeq == oldest.seq && isFinished(old.Status) {
			f.evicted[old.Status]++
			delete(jobs, oldest.id)
		}
		return
	}
	f.ring[(f.start+f.count)%f.limit] = entry
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
