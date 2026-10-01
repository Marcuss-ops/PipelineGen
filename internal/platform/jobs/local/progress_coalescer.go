package local

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ProgressSink is the canonical sink for persisted job progress updates.
type ProgressSink interface {
	SetProgress(ctx context.Context, jobID string, progress int, message string) error
}

type progressUpdate struct {
	pct     int
	message string
	data    json.RawMessage
}

// ProgressCoalesceConfig controls the in-memory coalescing window.
type ProgressCoalesceConfig struct {
	Window time.Duration
}

// ProgressCoalescer batches progress writes per job ID and flushes the
// latest update on a fixed cadence. Window=0 disables batching and writes
// through immediately.
type ProgressCoalescer struct {
	sink   ProgressSink
	window time.Duration
	log    *zap.Logger

	mu      sync.Mutex
	pending map[string]progressUpdate
	stopCh  chan struct{}
}

func NewProgressCoalescer(sink ProgressSink, cfg ProgressCoalesceConfig, log *zap.Logger) *ProgressCoalescer {
	if cfg.Window < 0 {
		cfg.Window = 0
	}
	return &ProgressCoalescer{
		sink:    sink,
		window:  cfg.Window,
		log:     log,
		pending: make(map[string]progressUpdate),
		stopCh:  make(chan struct{}),
	}
}

func (c *ProgressCoalescer) Window() time.Duration {
	if c == nil {
		return 0
	}
	return c.window
}

func (c *ProgressCoalescer) Start(ctx context.Context) {
	if c == nil || c.window == 0 {
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(c.window)
	defer ticker.Stop()

	flushAll := func() {
		batch := c.popAll()
		for jobID, update := range batch {
			if err := c.setProgress(context.Background(), jobID, update); err != nil && c.log != nil {
				c.log.Warn("progress coalescer flush failed", zap.String("job_id", jobID), zap.Error(err))
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			flushAll()
			return
		case <-c.stopCh:
			flushAll()
			return
		case <-ticker.C:
			flushAll()
		}
	}
}

func (c *ProgressCoalescer) Stop() {
	if c == nil {
		return
	}
	select {
	case <-c.stopCh:
		return
	default:
		close(c.stopCh)
	}
}

func (c *ProgressCoalescer) Take(ctx context.Context, jobID string, progress int, message string) error {
	return c.TakeData(ctx, jobID, progress, message, nil)
}

// TakeData coalesces progress while retaining the structured activity payload.
func (c *ProgressCoalescer) TakeData(ctx context.Context, jobID string, progress int, message string, data json.RawMessage) error {
	if c == nil {
		return fmt.Errorf("progress coalescer is nil")
	}
	update := progressUpdate{pct: progress, message: message, data: append(json.RawMessage(nil), data...)}
	if c.window == 0 {
		return c.setProgress(ctx, jobID, update)
	}

	c.mu.Lock()
	c.pending[jobID] = update
	c.mu.Unlock()
	return nil
}

func (c *ProgressCoalescer) setProgress(ctx context.Context, jobID string, update progressUpdate) error {
	if len(update.data) > 0 {
		var payload map[string]any
		if json.Unmarshal(update.data, &payload) == nil {
			if sink, ok := c.sink.(interface {
				SetProgressData(context.Context, string, int, string, map[string]any) error
			}); ok {
				return sink.SetProgressData(ctx, jobID, update.pct, update.message, payload)
			}
		}
	}
	return c.sink.SetProgress(ctx, jobID, update.pct, update.message)
}

func (c *ProgressCoalescer) FlushJob(jobID string) (*progressUpdate, error) {
	if c == nil {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	update, ok := c.pending[jobID]
	if !ok {
		return nil, nil
	}
	delete(c.pending, jobID)
	out := update
	return &out, nil
}

func (c *ProgressCoalescer) popAll() map[string]progressUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make(map[string]progressUpdate, len(c.pending))
	for k, v := range c.pending {
		out[k] = v
	}
	c.pending = make(map[string]progressUpdate)
	return out
}
