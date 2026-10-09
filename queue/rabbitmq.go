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

// maxDequeuePause is the longest pause between two polls of an empty queue
const maxDequeuePause = 100 * time.Millisecond

// ErrNoJobAvailable is returned when no job is available in the queue
var ErrNoJobAvailable = apperror.NewError("no job available")

// RabbitMQ implements a RabbitMQ-backed job queue
type RabbitMQ struct {
	conn         *amqp.Connection
	channel      *amqp.Channel
	queueName    string
	delayQueue   string // holds scheduled jobs until their time, then dead-letters them to queueName
	exchangeName string
	routingKey   string
	jobs         map[string]*Job
	jobsMutex    sync.RWMutex
	closed       bool
	closeMutex   sync.RWMutex
	prefetch     int
	finished     finishedJobs
	topology     RabbitMQConfig // what NewRabbitMQ declared, so Reconnect can declare it again
	// deliveries is the channel of the consumer that Dequeue reads from when prefetch is set.
	// It is started by the first Dequeue and dropped again by Reconnect.
	deliveries   <-chan amqp.Delivery
	consumerLock sync.Mutex
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
	// Prefetch switches Dequeue from polling with basic.get to a consumer that the broker
	// pushes up to Prefetch unacknowledged jobs to. That removes the round trip per job and the
	// polling delay, which limit basic.get to about a thousand jobs per second. Set it to at
	// least the number of workers, otherwise they wait for each other. Jobs that were pushed but
	// not yet taken are unavailable to other consumers until they are acknowledged or the
	// connection closes. The broker deletes an AutoDelete queue when its last consumer
	// goes away; Reconnect declares the queues again. 0 (the default) keeps the polling behavior.
	Prefetch int
	// RetainFinished is how many completed, failed and dead-lettered jobs are kept for GetJob and
	// GetJobs. 0 selects the default of 10000, below 0 keeps all of them (which grows without
	// bound in a long running process). Older ones are dropped from the index; GetStats still
	// counts them.
	RetainFinished int
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

	err = declareTopology(channel, config)
	if err != nil {
		apperror.Handle(channel.Close(), "failed to close channel")
		apperror.Handle(conn.Close(), "failed to close connection")
		return nil, apperror.Wrap(err)
	}

	delayedQueueName := config.QueueName + "_schedule"
	rq := &RabbitMQ{
		topology:     config,
		conn:         conn,
		channel:      channel,
		queueName:    config.QueueName,
		delayQueue:   delayedQueueName,
		prefetch:     config.Prefetch,
		finished:     newFinishedJobs(retentionLimit(config.RetainFinished)),
		exchangeName: config.ExchangeName,
		routingKey:   config.RoutingKey,
		jobs:         make(map[string]*Job),
	}

	return rq, nil
}

// declareTopology declares the exchange, the work queue, the queue for scheduled jobs and the
// binding. Declaring is idempotent when the arguments match.
func declareTopology(channel *amqp.Channel, config RabbitMQConfig) error {
	err := channel.ExchangeDeclare(
		config.ExchangeName, // name
		"direct",            // type
		config.Durable,      // durable
		config.AutoDelete,   // auto-deleted
		config.Exclusive,    // exclusive
		config.NoWait,       // no-wait
		nil,                 // arguments
	)
	if err != nil {
		return err
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
		return err
	}

	err = channel.QueueBind(
		config.QueueName,    // queue name
		config.RoutingKey,   // routing key
		config.ExchangeName, // exchange
		config.NoWait,       // no-wait
		nil,                 // arguments
	)
	if err != nil {
		return err
	}

	// Scheduled jobs wait here with a per message expiration and are dead-lettered to the
	// work queue when it runs out. The queue must not have a queue level TTL, which would cap
	// the expiration. It has its own name: a queue declared earlier under the old name has
	// different arguments, and redeclaring it with other arguments is refused by the broker.
	_, err = channel.QueueDeclare(
		config.QueueName+"_schedule", // name
		config.Durable,               // durable
		config.AutoDelete,            // delete when unused
		config.Exclusive,             // exclusive
		config.NoWait,                // no-wait
		map[string]interface{}{
			"x-dead-letter-exchange":    config.ExchangeName,
			"x-dead-letter-routing-key": config.RoutingKey,
		},
	)
	return err
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
		return rq.scheduleJob(ctx, message, job.ScheduleAt)
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
func (rq *RabbitMQ) scheduleJob(ctx context.Context, message amqp.Publishing, runAt time.Time) error {
	// Per message expiration in milliseconds. Published through the default exchange
	// straight into the schedule queue. RabbitMQ expires messages only at the head of a queue,
	// so a job with a long delay holds back jobs behind it that are due earlier.
	message.Expiration = strconv.FormatInt(max(time.Until(runAt).Milliseconds(), 0), 10)
	return rq.channel.PublishWithContext(
		ctx,
		"",
		rq.delayQueue,
		false,
		false,
		message,
	)
}

// next returns the next message, waiting until the context is done. It returns
// ErrNoJobAvailable when that happens first.
func (rq *RabbitMQ) next(ctx context.Context) (amqp.Delivery, error) {
	if rq.prefetch > 0 {
		deliveries, err := rq.consumer()
		if err != nil {
			return amqp.Delivery{}, err
		}
		select {
		case msg, ok := <-deliveries:
			if !ok {
				return amqp.Delivery{}, apperror.NewError("consumer closed")
			}
			return msg, nil
		case <-ctx.Done():
			return amqp.Delivery{}, ErrNoJobAvailable
		}
	}

	msg, ok, err := rq.get()
	if err != nil {
		return amqp.Delivery{}, apperror.Wrap(err)
	}

	// basic.get does not wait, so poll until the timeout. Start with short pauses so a job that
	// arrives right away is picked up quickly, then back off to spare the broker.
	for pause := 5 * time.Millisecond; !ok; pause = min(pause*2, maxDequeuePause) {
		select {
		case <-ctx.Done():
			return amqp.Delivery{}, ErrNoJobAvailable
		case <-time.After(pause):
		}

		msg, ok, err = rq.get()
		if err != nil {
			return amqp.Delivery{}, apperror.Wrap(err)
		}
	}
	return msg, nil
}

// consumer returns the delivery channel, starting the consumer on first use
func (rq *RabbitMQ) consumer() (<-chan amqp.Delivery, error) {
	// Same order as Reconnect: the close lock first, then the consumer lock
	rq.closeMutex.RLock()
	defer rq.closeMutex.RUnlock()

	rq.consumerLock.Lock()
	defer rq.consumerLock.Unlock()

	if rq.closed {
		return nil, apperror.NewError("queue is closed")
	}
	if rq.deliveries != nil {
		return rq.deliveries, nil
	}

	if err := rq.channel.Qos(rq.prefetch, 0, false); err != nil {
		return nil, apperror.Wrap(err)
	}
	deliveries, err := rq.channel.Consume(rq.queueName, "", false, false, false, false, nil)
	if err != nil {
		return nil, apperror.Wrap(err)
	}
	rq.deliveries = deliveries
	return deliveries, nil
}

// get fetches one message without waiting. The lock is held for the call only, so a Dequeue
// that is waiting for a job does not hold up Close.
func (rq *RabbitMQ) get() (amqp.Delivery, bool, error) {
	rq.closeMutex.RLock()
	defer rq.closeMutex.RUnlock()

	if rq.closed {
		return amqp.Delivery{}, false, apperror.NewError("queue is closed")
	}
	msg, ok, err := rq.channel.Get(rq.queueName, false)
	if err != nil {
		return amqp.Delivery{}, false, apperror.Wrap(err)
	}
	return msg, ok, nil
}

// Dequeue retrieves a job from the queue
func (rq *RabbitMQ) Dequeue(ctx context.Context, timeout time.Duration) (*Job, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msg, err := rq.next(timeoutCtx)
	if err != nil {
		return nil, err
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
	rq.finished.record(rq.jobs, job)

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

	rq.finished.addTo(stats)

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

	// The connection may be gone already; closing is then a no-op, not a reason to panic
	if rq.channel != nil {
		if err := rq.channel.Close(); err != nil {
			logger.Debug().Err(err).Msg("channel was not closed cleanly")
		}
	}

	if rq.conn != nil {
		if err := rq.conn.Close(); err != nil {
			logger.Debug().Err(err).Msg("connection was not closed cleanly")
		}
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

	// Reconnect exists to replace a broken connection, so the old channel and connection
	// are often closed already. That is not an error here and must not panic.
	if rq.channel != nil {
		if err := rq.channel.Close(); err != nil {
			logger.Debug().Err(err).Msg("old channel was not closed cleanly")
		}
	}
	if rq.conn != nil {
		if err := rq.conn.Close(); err != nil {
			logger.Debug().Err(err).Msg("old connection was not closed cleanly")
		}
	}

	url := config.URL
	if strings.TrimSpace(url) == "" {
		url = rq.topology.URL
	}
	conn, err := amqp.Dial(url)
	if err != nil {
		return apperror.Wrap(err)
	}

	channel, err := conn.Channel()
	if err != nil {
		apperror.Handle(conn.Close(), "failed to close connection")
		return apperror.Wrap(err)
	}

	// The broker may have lost the queues: an AutoDelete queue is deleted with its last
	// consumer, and a non-durable one does not survive a broker restart. Declaring them again
	// is harmless when they still exist.
	if err := declareTopology(channel, rq.topology); err != nil {
		apperror.Handle(channel.Close(), "failed to close channel")
		apperror.Handle(conn.Close(), "failed to close connection")
		return apperror.Wrap(err)
	}

	rq.conn = conn
	rq.channel = channel
	rq.closed = false
	rq.topology.URL = url
	rq.consumerLock.Lock()
	rq.deliveries = nil
	rq.consumerLock.Unlock()

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
	// Scheduled jobs that have not come due yet are part of the queue
	_, err = rq.channel.QueuePurge(rq.delayQueue, false)
	if err != nil {
		return apperror.Wrap(err)
	}

	rq.jobsMutex.Lock()
	rq.jobs = make(map[string]*Job)
	rq.finished = newFinishedJobs(rq.finished.limit)
	rq.jobsMutex.Unlock()

	return nil
}
