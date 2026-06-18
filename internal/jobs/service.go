package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
)

// MaxPayloadSize is the maximum allowed size for a serialized job payload in bytes.
const MaxPayloadSize = 1 << 20 // 1 MB

// Service manages job life cycle: enqueue, query, cancel.
type Service struct {
	repo       *SQLiteStore
	dispatcher *Dispatcher
	log        *zap.Logger

	// enqueueMu serializes FindActiveByKey + Create to prevent the
	// race where two concurrent Enqueue calls both find no existing
	// job and then both insert a duplicate.
	enqueueMu sync.Mutex
}

func NewService(repo *SQLiteStore, dispatcher *Dispatcher, log *zap.Logger) *Service {
	return &Service{
		repo:       repo,
		dispatcher: dispatcher,
		log:        log,
	}
}

// RegisterHandler registers a handler for the given job type (string).
func (s *Service) RegisterHandler(jobType string, handler HandlerFunc) error {
	return s.dispatcher.Register(jobType, handler)
}

// validateEnqueueRequest checks the EnqueueRequest for common errors.
func validateEnqueueRequest(req *EnqueueRequest) error {
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
	if len(req.Payload) > MaxPayloadSize {
		return fmt.Errorf("payload size %d exceeds maximum %d bytes", len(req.Payload), MaxPayloadSize)
	}
	return nil
}

func (s *Service) Enqueue(ctx context.Context, req *EnqueueRequest) (*Job, error) {
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

	var payload json.RawMessage
	if req.Payload != nil {
		payloadBytes, err := json.Marshal(req.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal payload: %w", err)
		}
		payload = payloadBytes
	}

	j := &Job{
		ID:            generateJobID(),
		Type:          req.Type,
		Status:        StatusQueued,
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

func (s *Service) Get(ctx context.Context, id string) (*Job, error) {
	return s.repo.Get(ctx, id)
}

func (s *Service) FindActiveByKey(ctx context.Context, activeKey string) (*Job, error) {
	return s.repo.FindActiveByKey(ctx, activeKey)
}

func (s *Service) List(ctx context.Context, filter Filter) ([]Job, error) {
	return s.repo.List(ctx, filter)
}

func (s *Service) Cancel(ctx context.Context, id string) error {
	return s.repo.Cancel(ctx, id)
}

func (s *Service) Retry(ctx context.Context, id string) (*Job, error) {
	return s.repo.Retry(ctx, id)
}

func (s *Service) Progress(ctx context.Context, id string, progress int, message string) error {
	return s.repo.SetProgress(ctx, id, progress, message)
}

func (s *Service) AddEvent(ctx context.Context, jobID string, eventType string, message string, data map[string]any) error {
	return s.repo.AddEvent(ctx, jobID, eventType, message, data)
}

func (s *Service) ListEvents(ctx context.Context, jobID string) ([]Event, error) {
	return s.repo.ListEvents(ctx, jobID)
}

func (s *Service) RequeueExpiredLeases(ctx context.Context) error {
	return s.repo.RequeueExpiredLeasesNoArg(ctx)
}

// GetStats returns aggregated job statistics.
func (s *Service) GetStats(ctx context.Context) (*JobStats, error) {
	return s.repo.GetStats(ctx)
}

func (s *Service) MarkStaleRunningJobsFailed(ctx context.Context, olderThan time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	return s.repo.MarkRunningJobsOlderThanFailed(ctx, cutoff, "stale job timeout")
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

func generateJobID() string {
	return fmt.Sprintf("job_%d_%s", time.Now().UnixNano(), hashutil.RandomString(8))
}
