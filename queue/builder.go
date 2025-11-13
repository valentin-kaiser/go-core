package queue

import (
	"encoding/json"
	"time"
)

// JobBuilder helps create jobs with a fluent API
type JobBuilder struct {
	job *Job
}

// NewJob creates a new job builder
func NewJob(jobType string) *JobBuilder {
	return &JobBuilder{
		job: &Job{
			Type:        jobType,
			Payload:     json.RawMessage("{}"),
			Priority:    PriorityNormal,
			Status:      StatusPending,
			CreatedAt:   time.Now(),
			MaxAttempts: 3,
			Metadata:    make(map[string]string),
		},
	}
}

// WithID sets the job ID
func (jb *JobBuilder) WithID(id string) *JobBuilder {
	jb.job.ID = id
	return jb
}

// WithPayload sets the job payload
func (jb *JobBuilder) WithPayload(payload map[string]interface{}) *JobBuilder {
	data, err := json.Marshal(payload)
	if err == nil {
		jb.job.Payload = data
	}
	return jb
}

// WithJSONPayload sets the job payload from JSON
func (jb *JobBuilder) WithJSONPayload(jsonData []byte) *JobBuilder {
	jb.job.Payload = jsonData
	return jb
}

// WithPriority sets the job priority
func (jb *JobBuilder) WithPriority(priority Priority) *JobBuilder {
	jb.job.Priority = priority
	return jb
}

// WithMaxAttempts sets the maximum number of attempts
func (jb *JobBuilder) WithMaxAttempts(maxAttempts int) *JobBuilder {
	jb.job.MaxAttempts = maxAttempts
	return jb
}

// WithScheduleAt schedules the job for a specific time
func (jb *JobBuilder) WithScheduleAt(scheduleAt time.Time) *JobBuilder {
	jb.job.ScheduleAt = scheduleAt
	jb.job.Status = StatusScheduled
	return jb
}

// WithDelay schedules the job to run after a delay
func (jb *JobBuilder) WithDelay(delay time.Duration) *JobBuilder {
	jb.job.ScheduleAt = time.Now().Add(delay)
	jb.job.Status = StatusScheduled
	return jb
}

// WithMetadata sets job metadata
func (jb *JobBuilder) WithMetadata(key, value string) *JobBuilder {
	jb.job.Metadata[key] = value
	return jb
}

// Build returns the constructed job
func (jb *JobBuilder) Build() *Job {
	return jb.job
}
