// Package jobs — Service.go (Wave 22 PR-B, June 2026).
//
// Service is the application-layer facade over the canonical job.Store
// port (job.JobBroker). The previous shape held *sqljobs.SQLiteStore
// directly (a godlike/06 violation — application → infrastructure). PR-B
// switches the field type to job.JobBroker (= job.Store in the canonical
// embedding). The compile-time assertion `var _ job.JobBroker = (*SQLiteStore)(nil)`
// in the infrastructure layer guarantees the seam is conformant.
//
// Service-internal transitions (Enqueue idempotency, FindActiveByKey /
// FindByTypeAndCorrelation / Retry / ListEvents) are part of the canonical
// Store surface as of PR-B. SQLite-specific helpers (GetStats,
// RequeueExpiredLeasesNoArg, MarkRunningJobsOlderThanFailed) intentionally
// do NOT live on this Service — the compile-time assertion
// `var _ job.JobBroker = (*SQLiteStore)(nil)` in
// `internal/infrastructure/database/sqlite/jobs/repository.go` is the load-bearing
// invariant: a future PR that resurrects `RequeueExpiredLeasesNoArg` (or any
// other SQLite-only method) on this Service would have to widen the JobBroker
// port to expose it, which the architecture review would catch at PR-merge time.
// Composition-root callers in `internal/app` already hold the concrete
// *SQLiteStore via JobsBundle.Repo and call those helpers directly.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	sqljobs "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/jobs"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// MaxPayloadSize is the maximum allowed size for a serialized job payload in bytes.
const MaxPayloadSize = 1 << 20 // 1 MB

// Service manages job life cycle: enqueue, query, cancel.
//
// PR-B: repo field is the canonical job.JobBroker port, not the concrete
// *sqljobs.SQLiteStore. Any future broker adapter (e.g. PostgreSQL) can be
// injected without touching this file.
type Service struct {
	repo       job.JobBroker
	dispatcher *Dispatcher
	log        *zap.Logger

	// enqueueMu serializes FindActiveByKey + Create to prevent the
	// race where two concurrent Enqueue calls both find no existing
	// job and then both insert a duplicate.
	enqueueMu sync.Mutex
}

type requeueExpiredLeaser interface {
	RequeueExpiredLeasesNoArg(context.Context) error
}

type statsProvider interface {
	GetStats(context.Context) (*sqljobs.JobStats, error)
}

// NewService constructs the Service from the canonical job.JobBroker port.
// Composition root injects *sqljobs.SQLiteStore today; future PR-`postgres`
// injects *pgbroker.Store (declared via `var _ job.JobBroker = (*pgbroker.Store)(nil)`).
func NewService(repo job.JobBroker, dispatcher *Dispatcher, log *zap.Logger) *Service {
	return &Service{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
	}
}

// RegisterHandler registers a handler for the given job type.
// Accepts any handler; performs a type-assertion to HandlerFunc.
// Implements job.Service interface.
func (s *Service) RegisterHandler(jobType string, handler any) error {
	switch h := handler.(type) {
	case HandlerFunc:
		return s.dispatcher.Register(jobType, h)
	case func(context.Context, *job.Job, *JobTools) (map[string]any, error):
		return s.dispatcher.Register(jobType, HandlerFunc(h))
	}

	rv := reflect.ValueOf(handler)
	if rv.Kind() != reflect.Func {
		return fmt.Errorf("job.Service.RegisterHandler: handler must be appjobs.HandlerFunc, got %T", handler)
	}
	rt := rv.Type()
	if rt.NumIn() != 3 || rt.NumOut() != 2 {
		return fmt.Errorf("job.Service.RegisterHandler: handler must be appjobs.HandlerFunc, got %T", handler)
	}
	if !rt.In(0).AssignableTo(reflect.TypeOf((*context.Context)(nil)).Elem()) ||
		!rt.In(1).AssignableTo(reflect.TypeOf((*job.Job)(nil))) ||
		!rt.In(2).AssignableTo(reflect.TypeOf((*JobTools)(nil))) ||
		!rt.Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
		return fmt.Errorf("job.Service.RegisterHandler: handler must be appjobs.HandlerFunc, got %T", handler)
	}

	wrapped := func(ctx context.Context, j *job.Job, tools *JobTools) (map[string]any, error) {
		results := rv.Call([]reflect.Value{
			reflect.ValueOf(ctx),
			reflect.ValueOf(j),
			reflect.ValueOf(tools),
		})
		var out map[string]any
		if !results[0].IsNil() {
			out, _ = results[0].Interface().(map[string]any)
		}
		var err error
		if !results[1].IsNil() {
			err, _ = results[1].Interface().(error)
		}
		return out, err
	}
	return s.dispatcher.Register(jobType, wrapped)
}

// validateEnqueueRequest checks the domain EnqueueRequest for common errors.
func validateEnqueueRequest(req *job.EnqueueRequest) error {
	if req == nil {
		return fmt.Errorf("enqueue request is nil")
	}
	if req.Type == "" {
		return fmt.Errorf("job type is required")
	}
	if req.Priority < 0 {
		return fmt.Errorf("priority must be non-negative, got %d", req.Priority)
	}
	if req.MaxRetries < -1 {
		return fmt.Errorf("max_retries must be >= -1, got %d", req.MaxRetries)
	}
	return nil
}

// Enqueue enqueues a job from a domain request. Implements job.Service.
func (s *Service) Enqueue(ctx context.Context, req *job.EnqueueRequest) (*job.Job, error) {
	if err := validateEnqueueRequest(req); err != nil {
		return nil, err
	}

	// Idempotency: auto-inject correlation_id from the request context.
	if req.CorrelationID == "" {
		if cid := corid.FromContext(ctx); cid != "" {
			req.CorrelationID = cid
		}
	}

	s.enqueueMu.Lock()
	defer s.enqueueMu.Unlock()

	if req.ActiveKey != "" {
		existing, err := s.repo.FindActiveByKey(ctx, req.ActiveKey)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing job: %w", err)
		}
		if existing != nil && !existing.IsTerminal() {
			s.log.Info("returning existing job with same active key", zap.String("job_id", existing.ID))
			return existing, nil
		}
	}

	// Idempotency on (type, correlation_id).
	if req.CorrelationID != "" {
		existing, err := s.repo.FindByTypeAndCorrelation(ctx, req.Type, req.CorrelationID)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing job by correlation: %w", err)
		}
		if existing != nil {
			s.log.Info("returning existing job with same (type, correlation_id)",
				zap.String("job_id", existing.ID),
				zap.String("type", req.Type),
				zap.String("correlation_id", req.CorrelationID),
			)
			return existing, nil
		}
	}

	now := time.Now()

	// Marshal the payload (typed struct or map[string]any).
	var payload json.RawMessage
	if req.Payload != nil {
		payloadBytes, err := json.Marshal(req.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		if len(payloadBytes) > MaxPayloadSize {
			return nil, fmt.Errorf("payload size %d exceeds maximum %d bytes", len(payloadBytes), MaxPayloadSize)
		}
		payload = payloadBytes
	}

	j := &job.Job{
		ID:            generateJobID(),
		Type:          req.Type,
		Status:        job.StatusQueued,
		Priority:      req.Priority,
		Project:       req.Project,
		VideoName:     req.VideoName,
		Payload:       payload,
		RetryCount:    0,
		MaxRetries:    req.MaxRetries,
		Progress:      0,
		CreatedAt:     now,
		UpdatedAt:     now,
		ActiveKey:     req.ActiveKey,
		CorrelationID: req.CorrelationID,
	}

	// Backward-compatible default: MaxRetries == 0 means 3 retries.
	if j.MaxRetries == 0 {
		j.MaxRetries = 3
	} else if j.MaxRetries < 0 {
		j.MaxRetries = 0
	}

	if j.Payload == nil || len(j.Payload) == 0 || string(j.Payload) == "null" {
		j.Payload = json.RawMessage("{}")
	}

	if err := s.repo.Create(ctx, j); err != nil {
		// Idempotency safety net.
		if j.CorrelationID != "" && strings.Contains(err.Error(), "UNIQUE constraint") {
			if existing, findErr := s.repo.FindByTypeAndCorrelation(ctx, j.Type, j.CorrelationID); findErr == nil && existing != nil {
				s.log.Info("returning existing job by (type, correlation_id) — caught race on UNIQUE constraint",
					zap.String("job_id", existing.ID),
					zap.String("type", j.Type),
					zap.String("correlation_id", j.CorrelationID),
				)
				return existing, nil
			}
		}
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	s.log.Info("job enqueued",
		zap.String("job_id", j.ID),
		zap.String("type", j.Type),
		zap.String("correlation_id", j.CorrelationID),
	)
	return j, nil
}

func (s *Service) Get(ctx context.Context, id string) (*job.Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) FindActiveByKey(ctx context.Context, activeKey string) (*job.Job, error) {
	return s.repo.FindActiveByKey(ctx, activeKey)
}

func (s *Service) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	return s.repo.List(ctx, filter)
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	return s.repo.Cancel(ctx, id)
}

func (s *Service) Retry(ctx context.Context, id string) (*job.Job, error) {
	return s.repo.Retry(ctx, id)
}

func (s *Service) Progress(ctx context.Context, id string, progress int, message string) error {
	return s.repo.SetProgress(ctx, id, progress, message)
}

func (s *Service) AddEvent(ctx context.Context, jobID string, eventType string, message string, data map[string]any) error {
	return s.repo.AddEvent(ctx, jobID, eventType, message, data)
}

// ListEvents returns the timeline events for a given job.
// Implements job.Service interface.
func (s *Service) ListEvents(ctx context.Context, jobID string) ([]job.Event, error) {
	return s.repo.ListEvents(ctx, jobID)
}

// IsTerminal reports whether the job status is a terminal state.
// Implements job.Service interface.
func (s *Service) IsTerminal(status job.Status) bool {
	return status.IsTerminal()
}

func (s *Service) RequeueExpiredLeases(ctx context.Context) error {
	provider, ok := s.repo.(requeueExpiredLeaser)
	if !ok {
		return fmt.Errorf("requeue expired leases: repository does not support RequeueExpiredLeasesNoArg")
	}
	return provider.RequeueExpiredLeasesNoArg(ctx)
}

// GetStats returns aggregated job statistics.
func (s *Service) GetStats(ctx context.Context) (*sqljobs.JobStats, error) {
	provider, ok := s.repo.(statsProvider)
	if !ok {
		return nil, fmt.Errorf("get stats: repository does not support GetStats")
	}
	return provider.GetStats(ctx)
}

// Complete marks a job as completed.
func (s *Service) Complete(ctx context.Context, id string, result map[string]any) error {
	resultJSON, _ := json.Marshal(result)
	return s.repo.Complete(ctx, id, "", "", 0, resultJSON)
}

// Fail marks a job as failed.
func (s *Service) Fail(ctx context.Context, id string, err error) error {
	return s.repo.Fail(ctx, id, "", "", 0, err.Error())
}

// Compile-time assertion: *Service satisfies the domain job.Service interface.
var _ job.Service = (*Service)(nil)

func generateJobID() string {
	return fmt.Sprintf("job_%d_%s", time.Now().UnixNano(), hashutil.RandomString(8))
}
