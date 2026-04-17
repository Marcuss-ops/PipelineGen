package queue

import (
	"context"
	"sync"
	"time"

	"velox/go-master/pkg/logger"
	"go.uber.org/zap"
)

// JobHandler is a function that processes a job message.
type JobHandler func(ctx context.Context, msg Message) error

// Consumer manages job consumption from a Queue.
// It implements runtime.BackgroundService.
type Consumer struct {
	queue      Queue
	workerID   string
	handlers   map[string]JobHandler
	maxWorkers int
	stopCh     chan struct{}
	wg         sync.WaitGroup
	mu         sync.RWMutex
	running    bool
}

// NewConsumer creates a new Job Consumer.
func NewConsumer(q Queue, workerID string, maxWorkers int) *Consumer {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	return &Consumer{
		queue:      q,
		workerID:   workerID,
		handlers:   make(map[string]JobHandler),
		maxWorkers: maxWorkers,
		stopCh:     make(chan struct{}),
	}
}

// Register adds a handler for a specific topic.
func (c *Consumer) Register(topic string, handler JobHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[topic] = handler
}

// Start begins the consumption loop.
func (c *Consumer) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return nil
	}
	c.running = true
	c.mu.Unlock()

	logger.Info("Starting Job Consumer",
		zap.String("worker_id", c.workerID),
		zap.Int("max_workers", c.maxWorkers),
		zap.String("backend", string(c.queue.Backend())),
	)

	for i := 0; i < c.maxWorkers; i++ {
		c.wg.Add(1)
		go c.workerLoop(ctx, i)
	}

	return nil
}

// Stop signals all workers to stop and waits for them.
func (c *Consumer) Stop() error {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	close(c.stopCh)
	c.mu.Unlock()

	c.wg.Wait()
	
	c.mu.Lock()
	c.running = false
	c.mu.Unlock()
	
	logger.Info("Job Consumer stopped", zap.String("worker_id", c.workerID))
	return nil
}

// Name returns the service name for lifecycle management.
func (c *Consumer) Name() string {
	return "JobConsumer"
}

func (c *Consumer) workerLoop(ctx context.Context, workerIdx int) {
	defer c.wg.Done()

	log := logger.Get().With(
		zap.String("worker_id", c.workerID),
		zap.Int("worker_idx", workerIdx),
	)

	log.Debug("Worker loop started")

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		default:
			// Attempt to lease a job
			lease, err := c.queue.LeaseNext(ctx, c.workerID)
			if err != nil {
				log.Error("Failed to lease next job", zap.Error(err))
				time.Sleep(2 * time.Second)
				continue
			}

			if lease == nil {
				// No jobs available, back off
				time.Sleep(5 * time.Second)
				continue
			}

			// Process the job
			c.processJob(ctx, lease, log)
		}
	}
}

func (c *Consumer) processJob(ctx context.Context, lease *Lease, log *zap.Logger) {
	msg := lease.Message
	topic := msg.Topic

	c.mu.RLock()
	handler, ok := c.handlers[topic]
	c.mu.RUnlock()

	if !ok {
		log.Warn("No handler registered for topic", zap.String("topic", topic), zap.String("job_id", msg.JobID))
		_ = c.queue.Nack(ctx, *lease, false) // Don't requeue if we can't handle it
		return
	}

	log.Info("Processing job", zap.String("topic", topic), zap.String("job_id", msg.JobID))

	// Catch panics in handlers
	defer func() {
		if r := recover(); r != nil {
			log.Error("Job handler panicked", zap.Any("recover", r), zap.String("job_id", msg.JobID))
			_ = c.queue.Nack(ctx, *lease, true)
		}
	}()

	err := handler(ctx, msg)
	if err != nil {
		log.Error("Job processing failed", zap.Error(err), zap.String("job_id", msg.JobID))
		// Requeue the job
		_ = c.queue.Nack(ctx, *lease, true)
		return
	}

	log.Info("Job completed successfully", zap.String("job_id", msg.JobID))
	_ = c.queue.Ack(ctx, *lease)
}
