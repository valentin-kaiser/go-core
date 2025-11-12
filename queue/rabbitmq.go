package queue

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/valentin-kaiser/go-core/apperror"
)

// ErrNoJobAvailable is returned when no job is available in the queue
var ErrNoJobAvailable = apperror.NewError("no job available")

// RabbitMQ implements a RabbitMQ-backed job queue
type RabbitMQ struct {
	conn         *amqp.Connection
	channel      *amqp.Channel
	queueName    string
	exchangeName string
	routingKey   string
	jobs         map[string]*Job
	jobsMutex    sync.RWMutex
	closed       bool
	closeMutex   sync.RWMutex
}

// RabbitMQConfig holds configuration for RabbitMQ queue
type RabbitMQConfig struct {
	URL          string
	QueueName    string
	ExchangeName string
	RoutingKey   string
	Durable      bool
	AutoDelete   bool
	Exclusive    bool
	NoWait       bool
	MaxPriority  int // Maximum priority level for the queue (0-255)
}

// NewRabbitMQ creates a new RabbitMQ-backed queue.
//
// Configuration Parameters:
// - URL: The RabbitMQ server URL. This is required for establishing a connection.
// - QueueName: The name of the queue to use. This is required and must be unique.
// - ExchangeName: The name of the exchange to bind the queue to. This is required.
// - RoutingKey: The routing key for binding the queue to the exchange. This is required.
// - Durable: If true, the queue will survive a broker restart.
// - AutoDelete: If true, the queue will be deleted when no consumers are connected.
// - Exclusive: If true, the queue will be used by only one connection and deleted when the connection closes.
// - NoWait: If true, the queue declaration will not wait for a server response.
// - MaxPriority: The maximum priority level for the queue (0-255). Defaults to 10 if not specified.
//
// Connection Setup:
// The function establishes a connection to the RabbitMQ server using the provided URL.
// It then declares a queue with the specified configuration parameters and binds it to the exchange.
//
// Error Conditions:
// - Missing or empty URL, QueueName, ExchangeName, or RoutingKey will result in an error.
// - Connection failures (e.g., network issues, invalid URL) will return a wrapped error.
// - Invalid MaxPriority values (outside the range 0-255) may cause unexpected behavior.
func NewRabbitMQ(config RabbitMQConfig) (*RabbitMQ, error) {
	if strings.TrimSpace(config.URL) == "" {
		return nil, apperror.NewError("RabbitMQ URL is required")
	}
	if strings.TrimSpace(config.QueueName) == "" {
		return nil, apperror.NewError("Queue name is required")
	}
	if strings.TrimSpace(config.ExchangeName) == "" {
		return nil, apperror.NewError("Exchange name is required")
	}
	if strings.TrimSpace(config.RoutingKey) == "" {
		return nil, apperror.NewError("Routing key is required")
	}
	if config.MaxPriority == 0 {
		config.MaxPriority = 10 // Default to 10 priority levels
	}

	conn, err := amqp.Dial(config.URL)
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	channel, err := conn.Channel()
	if err != nil {
		apperror.Handle(conn.Close(), "failed to close connection")
		return nil, apperror.Wrap(err)
	}

	err = channel.ExchangeDeclare(
		config.ExchangeName, // name
		"direct",            // type
		config.Durable,      // durable
		config.AutoDelete,   // auto-deleted
		config.Exclusive,    // exclusive
		config.NoWait,       // no-wait
		nil,                 // arguments
	)
	if err != nil {
		apperror.Handle(channel.Close(), "failed to close channel")
		apperror.Handle(conn.Close(), "failed to close connection")
		return nil, apperror.Wrap(err)
	}

	queueArgs := amqp.Table{}
	if config.MaxPriority > 0 {
		queueArgs["x-max-priority"] = config.MaxPriority
	}

	_, err = channel.QueueDeclare(
		config.QueueName,  // name
		config.Durable,    // durable
		config.AutoDelete, // delete when unused
		config.Exclusive,  // exclusive
		config.NoWait,     // no-wait
		queueArgs,         // arguments
	)
	if err != nil {
		apperror.Handle(channel.Close(), "failed to close channel")
		apperror.Handle(conn.Close(), "failed to close connection")
		return nil, apperror.Wrap(err)
	}

	err = channel.QueueBind(
		config.QueueName,    // queue name
		config.RoutingKey,   // routing key
		config.ExchangeName, // exchange
		config.NoWait,       // no-wait
		nil,                 // arguments
	)
	if err != nil {
		apperror.Handle(channel.Close(), "failed to close channel")
		apperror.Handle(conn.Close(), "failed to close connection")
		return nil, apperror.Wrap(err)
	}

	delayedQueueName := config.QueueName + "_delayed"
	_, err = channel.QueueDeclare(
		delayedQueueName,  // name
		config.Durable,    // durable
		config.AutoDelete, // delete when unused
		config.Exclusive,  // exclusive
		config.NoWait,     // no-wait
		map[string]interface{}{
			"x-message-ttl":             int32(0),
			"x-dead-letter-exchange":    config.ExchangeName,
			"x-dead-letter-routing-key": config.RoutingKey,
		},
	)
	if err != nil {
		apperror.Handle(channel.Close(), "failed to close channel")
		apperror.Handle(conn.Close(), "failed to close connection")
		return nil, apperror.Wrap(err)
	}

	rq := &RabbitMQ{
		conn:         conn,
		channel:      channel,
		queueName:    config.QueueName,
		exchangeName: config.ExchangeName,
		routingKey:   config.RoutingKey,
		jobs:         make(map[string]*Job),
	}

	return rq, nil
}

// NewRabbitMQFromURL creates a new RabbitMQ queue with a simple URL
func NewRabbitMQFromURL(url string) (*RabbitMQ, error) {
	config := RabbitMQConfig{
		URL:          url,
		QueueName:    "jobs",
		ExchangeName: "jobs_exchange",
		RoutingKey:   "jobs",
		Durable:      true,
		AutoDelete:   false,
		Exclusive:    false,
		NoWait:       false,
	}
	return NewRabbitMQ(config)
}

// Enqueue adds a job to the queue
func (rq *RabbitMQ) Enqueue(ctx context.Context, job *Job) error {
	rq.closeMutex.RLock()
	defer rq.closeMutex.RUnlock()

	if rq.closed {
		return apperror.NewError("queue is closed")
	}

	rq.jobsMutex.Lock()
	rq.jobs[job.ID] = job
	rq.jobsMutex.Unlock()

	jobData, err := json.Marshal(job)
	if err != nil {
		return apperror.Wrap(err)
	}

	headers := amqp.Table{
		"job_id":       job.ID,
		"job_type":     job.Type,
		"priority":     int32(job.Priority),
		"max_attempts": int32(job.MaxAttempts),
		"attempts":     int32(job.Attempts),
		"status":       int32(job.Status),
	}

	for key, value := range job.Metadata {
		headers["meta_"+key] = value
	}

	message := amqp.Publishing{
		Headers:         headers,
		ContentType:     "application/json",
		ContentEncoding: "",
		Body:            jobData,
		DeliveryMode:    amqp.Persistent, // Make message persistent
		Priority:        uint8(job.Priority),
		Timestamp:       time.Now(),
	}

	if job.IsScheduled() {
		return rq.scheduleJob(ctx, message)
	}

	return rq.channel.PublishWithContext(
		ctx,
		rq.exchangeName,
		rq.routingKey,
		false, // mandatory
		false, // immediate
		message,
	)
}

// scheduleJob handles scheduled job publishing
func (rq *RabbitMQ) scheduleJob(ctx context.Context, message amqp.Publishing) error {
	return rq.channel.PublishWithContext(
		ctx,
		rq.exchangeName,
		rq.routingKey,
		false,
		false,
		message,
	)
}

// Dequeue retrieves a job from the queue
func (rq *RabbitMQ) Dequeue(ctx context.Context, timeout time.Duration) (*Job, error) {
	rq.closeMutex.RLock()
	defer rq.closeMutex.RUnlock()

	if rq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg, ok, err := rq.channel.Get(rq.queueName, false) // manual ack
	if err != nil {
		return nil, apperror.Wrap(err)
	}

	if !ok {
		// No message available, wait a bit and try again
		select {
		case <-timeoutCtx.Done():
			return nil, ErrNoJobAvailable // Timeout, no job available
		case <-time.After(100 * time.Millisecond):
			msg, ok, err = rq.channel.Get(rq.queueName, false)
			if err != nil {
				return nil, apperror.Wrap(err)
			}
			if !ok {
				return nil, ErrNoJobAvailable // Still no message
			}
		}
	}

	var job Job
	err = json.Unmarshal(msg.Body, &job)
	if err != nil {
		apperror.Handle(msg.Nack(false, false), "failed to nack message")
		return nil, apperror.Wrap(err)
	}

	job.Status = StatusRunning
	job.UpdatedAt = time.Now()

	if job.Metadata == nil {
		job.Metadata = make(map[string]string)
	}
	job.Metadata["delivery_tag"] = strconv.FormatUint(msg.DeliveryTag, 10)

	rq.jobsMutex.Lock()
	rq.jobs[job.ID] = &job
	rq.jobsMutex.Unlock()

	return &job, nil
}

// Schedule adds a job to be processed at a specific time
func (rq *RabbitMQ) Schedule(ctx context.Context, job *Job) error {
	job.Status = StatusScheduled
	return rq.Enqueue(ctx, job)
}

// UpdateJob updates a job's status
func (rq *RabbitMQ) UpdateJob(_ context.Context, job *Job) error {
	rq.jobsMutex.Lock()
	defer rq.jobsMutex.Unlock()

	if rq.closed {
		return apperror.NewError("queue is closed")
	}

	rq.jobs[job.ID] = job

	if job.Metadata != nil {
		if deliveryTagStr, exists := job.Metadata["delivery_tag"]; exists {
			deliveryTag, err := strconv.ParseUint(deliveryTagStr, 10, 64)
			if err == nil {
				switch job.Status {
				case StatusCompleted:
					return rq.channel.Ack(deliveryTag, false)
				case StatusFailed:
					if job.Attempts >= job.MaxAttempts {
						return rq.channel.Ack(deliveryTag, false)
					}
					return rq.channel.Nack(deliveryTag, false, true)
				case StatusRetrying:
					return rq.channel.Nack(deliveryTag, false, true)
				}
			}
		}
	}

	return nil
}

// GetJob retrieves a job by ID
func (rq *RabbitMQ) GetJob(_ context.Context, id string) (*Job, error) {
	rq.jobsMutex.RLock()
	defer rq.jobsMutex.RUnlock()

	if rq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	job, exists := rq.jobs[id]
	if !exists {
		return nil, apperror.NewError("job not found")
	}

	return job, nil
}

// GetJobs retrieves jobs by status
func (rq *RabbitMQ) GetJobs(_ context.Context, status Status, limit int) ([]*Job, error) {
	rq.jobsMutex.RLock()
	defer rq.jobsMutex.RUnlock()

	if rq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	var jobs []*Job
	count := 0

	for _, job := range rq.jobs {
		if job.Status == status {
			jobs = append(jobs, job)
			count++
			if limit > 0 && count >= limit {
				break
			}
		}
	}

	return jobs, nil
}

// GetStats returns queue statistics
func (rq *RabbitMQ) GetStats(_ context.Context) (*Stats, error) {
	rq.jobsMutex.RLock()
	defer rq.jobsMutex.RUnlock()

	if rq.closed {
		return nil, apperror.NewError("queue is closed")
	}

	stats := &Stats{}
	for _, job := range rq.jobs {
		switch job.Status {
		case StatusPending:
			stats.Pending++
		case StatusRunning:
			stats.Running++
		case StatusCompleted:
			stats.Completed++
		case StatusFailed:
			stats.Failed++
		case StatusRetrying:
			stats.Retrying++
		case StatusScheduled:
			stats.Scheduled++
		case StatusDeadLetter:
			stats.DeadLetter++
		}
		stats.TotalJobs++
	}

	queueInfo, err := rq.channel.QueueDeclarePassive(rq.queueName, true, false, false, false, nil)
	if err == nil {
		stats.QueueSize = int64(queueInfo.Messages)
	}

	return stats, nil
}

// DeleteJob removes a job from the queue
func (rq *RabbitMQ) DeleteJob(_ context.Context, id string) error {
	rq.jobsMutex.Lock()
	defer rq.jobsMutex.Unlock()

	if rq.closed {
		return apperror.NewError("queue is closed")
	}

	delete(rq.jobs, id)
	return nil
}

// Close closes the RabbitMQ connection
func (rq *RabbitMQ) Close() error {
	rq.closeMutex.Lock()
	defer rq.closeMutex.Unlock()

	if rq.closed {
		return nil
	}

	rq.closed = true

	if rq.channel != nil {
		apperror.Handle(rq.channel.Close(), "failed to close channel")
	}

	if rq.conn != nil {
		apperror.Handle(rq.conn.Close(), "failed to close connection")
	}

	return nil
}

// IsConnectionOpen checks if the RabbitMQ connection is open
func (rq *RabbitMQ) IsConnectionOpen() bool {
	rq.closeMutex.RLock()
	defer rq.closeMutex.RUnlock()

	return !rq.closed && rq.conn != nil && !rq.conn.IsClosed()
}

// Reconnect attempts to reconnect to RabbitMQ
func (rq *RabbitMQ) Reconnect(config RabbitMQConfig) error {
	rq.closeMutex.Lock()
	defer rq.closeMutex.Unlock()

	if rq.channel != nil {
		apperror.Handle(rq.channel.Close(), "failed to close old channel")
	}
	if rq.conn != nil {
		apperror.Handle(rq.conn.Close(), "failed to close old connection")
	}

	conn, err := amqp.Dial(config.URL)
	if err != nil {
		return apperror.Wrap(err)
	}

	channel, err := conn.Channel()
	if err != nil {
		apperror.Handle(conn.Close(), "failed to close connection")
		return apperror.Wrap(err)
	}

	rq.conn = conn
	rq.channel = channel
	rq.closed = false

	return nil
}

// PurgeQueue removes all messages from the queue
func (rq *RabbitMQ) PurgeQueue(_ context.Context) error {
	rq.closeMutex.RLock()
	defer rq.closeMutex.RUnlock()

	if rq.closed {
		return apperror.NewError("queue is closed")
	}

	_, err := rq.channel.QueuePurge(rq.queueName, false)
	if err != nil {
		return apperror.Wrap(err)
	}

	rq.jobsMutex.Lock()
	rq.jobs = make(map[string]*Job)
	rq.jobsMutex.Unlock()

	return nil
}
