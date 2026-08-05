// Package processmetrics defines the application-layer port for durable
// process phase timing. It deliberately knows nothing about SQLite or
// Prometheus; infrastructure supplies the Repository implementation.
package processmetrics

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Metric is the application representation of one process phase result.
type Metric struct {
	ProcessType string
	JobID       string
	ParentJobID string
	Phase       string
	Language    string
	Provider    string
	StartedAt   time.Time
	DurationMs  int64
	QueueWaitMs int64
	Status      string
	ErrorCode   string
	ItemsIn     int64
	ItemsOut    int64
	BytesIn     int64
	BytesOut    int64
	RetryCount  int64
	CreatedAt   time.Time
	Details     map[string]any
}

// Repository is the narrow persistence port consumed by Recorder.
type Repository interface {
	Insert(ctx context.Context, metric *Metric) error
}

// Recorder starts durable timing samples.
type Recorder interface {
	Start(ctx context.Context, input StartInput) *Handle
}

// CanonicalMetric is a timing sample already measured by the canonical
// observability Run. It exists only as a compatibility projection input: the
// processmetrics recorder must not start another clock for these samples.
type CanonicalMetric struct {
	ProcessType string
	JobID       string
	ParentJobID string
	Phase       string
	Language    string
	Provider    string
	StartedAt   time.Time
	DurationMs  int64
	Status      string
	ErrorCode   string
	CreatedAt   time.Time
}

// CanonicalRecorder accepts measurements owned by kernel observability.
type CanonicalRecorder interface {
	RecordCanonical(context.Context, CanonicalMetric) error
}

// StartInput identifies a phase.
type StartInput struct {
	ProcessType string
	JobID       string
	ParentJobID string
	Phase       string
	Language    string
	Provider    string
}

// Handle accumulates phase counters until End is called.
type Handle struct {
	recorder *RecorderImpl
	ctx      context.Context
	started  time.Time
	metric   Metric
	endErr   error
	prepared bool
	ended    bool
	mu       sync.Mutex
}

// RecorderImpl is the common recorder implementation.
type RecorderImpl struct {
	repo Repository
	now  func() time.Time
}

// NewRecorder constructs a recorder. A nil repository creates a safe no-op
// recorder suitable for tests and explicitly non-persistent deployments.
func NewRecorder(repo Repository) *RecorderImpl {
	return &RecorderImpl{repo: repo, now: func() time.Time { return time.Now().UTC() }}
}

var _ Recorder = (*RecorderImpl)(nil)
var _ CanonicalRecorder = (*RecorderImpl)(nil)

// RecordCanonical persists a duration measured by the canonical Run. No
// time.Now call is made here; the supplied timestamps and duration remain
// authoritative for the compatibility window.
func (r *RecorderImpl) RecordCanonical(ctx context.Context, sample CanonicalMetric) error {
	if r == nil || r.repo == nil {
		return nil
	}
	metric := &Metric{
		ProcessType: sample.ProcessType,
		JobID:       sample.JobID,
		ParentJobID: sample.ParentJobID,
		Phase:       sample.Phase,
		Language:    sample.Language,
		Provider:    sample.Provider,
		StartedAt:   sample.StartedAt,
		DurationMs:  nonNegative(sample.DurationMs),
		Status:      sample.Status,
		ErrorCode:   sample.ErrorCode,
		CreatedAt:   sample.CreatedAt,
	}
	if metric.StartedAt.IsZero() || metric.CreatedAt.IsZero() {
		return errors.New("processmetrics: canonical metric timestamps are required")
	}
	return r.repo.Insert(ctx, metric)
}

// Start begins a phase sample.
func (r *RecorderImpl) Start(ctx context.Context, input StartInput) *Handle {
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now().UTC()
	if r != nil && r.now != nil {
		now = r.now()
	}
	return &Handle{
		recorder: r,
		ctx:      ctx,
		started:  now,
		metric: Metric{
			ProcessType: input.ProcessType,
			JobID:       input.JobID,
			ParentJobID: input.ParentJobID,
			Phase:       input.Phase,
			Language:    input.Language,
			Provider:    input.Provider,
			StartedAt:   now,
		},
	}
}

// SetItems records input and output item counts.
func (h *Handle) SetItems(in, out int64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		return
	}
	h.metric.ItemsIn = nonNegative(in)
	h.metric.ItemsOut = nonNegative(out)
}

// SetBytes records input and output byte counts.
func (h *Handle) SetBytes(in, out int64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		return
	}
	h.metric.BytesIn = nonNegative(in)
	h.metric.BytesOut = nonNegative(out)
}

// SetQueueWait records time spent waiting before phase execution.
func (h *Handle) SetQueueWait(duration time.Duration) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		return
	}
	h.metric.QueueWaitMs = nonNegative(duration.Milliseconds())
}

// SetRetryCount records provider or phase retry attempts.
func (h *Handle) SetRetryCount(count int64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		return
	}
	h.metric.RetryCount = nonNegative(count)
}

// SetDetails adds process-specific measurements to details_json.
func (h *Handle) SetDetails(details map[string]any) {
	if h == nil || len(details) == 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		return
	}
	if h.metric.Details == nil {
		h.metric.Details = make(map[string]any, len(details))
	}
	for key, value := range details {
		h.metric.Details[key] = value
	}
}

// End closes the phase and persists one row. A persistence failure leaves the
// handle retryable; a later End call retries the same terminal phase result.
func (h *Handle) End(err error) error {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		return nil
	}
	if !h.prepared {
		h.endErr = err
		end := time.Now().UTC()
		if h.recorder != nil && h.recorder.now != nil {
			end = h.recorder.now()
		}
		h.metric.DurationMs = nonNegative(end.Sub(h.started).Milliseconds())
		h.metric.CreatedAt = end
		if h.endErr != nil {
			h.metric.Status = "failure"
			h.metric.ErrorCode = errorCode(h.endErr)
		} else {
			h.metric.Status = "success"
		}
		h.prepared = true
	}
	if h.recorder == nil || h.recorder.repo == nil {
		h.ended = true
		return nil
	}
	if persistErr := h.recorder.repo.Insert(h.ctx, &h.metric); persistErr != nil {
		return persistErr
	}
	h.ended = true
	return nil
}

// ErrorCoder lets typed errors provide a stable persisted error code.
type ErrorCoder interface {
	ErrorCode() string
}

func errorCode(err error) string {
	var coded ErrorCoder
	if errors.As(err, &coded) && coded.ErrorCode() != "" {
		return coded.ErrorCode()
	}
	return "phase_error"
}

func nonNegative(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
