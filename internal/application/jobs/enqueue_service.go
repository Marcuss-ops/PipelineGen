// Package jobs — enqueue_service.go: Enqueue + idempotency pipeline.
//
// PR-GODOBJ-6 (July 2026): mechanically extracted from service.go
// per the god-object decomposition plan. Zero behavior changes.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// MaxPayloadSize is the maximum allowed size for a serialized job payload in bytes.
const MaxPayloadSize = 1 << 20 // 1 MB

// hasRegistry reports whether this Service has a registry attached AND
// the registry has the given job type registered. Used by Enqueue() to
// gate the MaxRetries fallback so the legacy 3-retry safety net still
// fires for unregistered types.
func (s *Service) hasRegistry(jobType string) bool {
	if s == nil || s.registry == nil {
		return false
	}
	return s.registry.IsRegistered(jobType)
}

// resolveMaxRetries encodes the Issue 4 (June 2026, P1) MaxRetries
// fallback semantic in a single testable helper. Enqueue() delegates
// to this helper so the logic is decoupled from repo/dispatcher
// concerns (test fixtures only need &Service{} filled in).
//
// Three-way semantics, in priority order:
//
//  1. currentMR < 0  → 0      (explicit "no retries" sentinel —
//     pre-Issue-4 behaviour preserved verbatim; the worker treats 0
//     as "do not retry").
//
//  2. currentMR > 0  → currentMR  (caller pre-set value preserved
//     verbatim; registry is the fallback, not an override).
//
//  3. currentMR == 0 → registry.DefaultMaxRetries(jobType) when
//     a registry is attached AND the type is REGISTERED; otherwise
//     the legacy hard-coded 3-retry safety net.
//
// Nil-service / nil-registry paths are covered by the
// `s.hasRegistry` guard inside the third branch.
func (s *Service) resolveMaxRetries(jobType string, currentMR int) int {
	if currentMR < 0 {
		return 0
	}
	if currentMR > 0 {
		return currentMR
	}
	// currentMR == 0 — resolve via registry when attached and registered.
	if s.hasRegistry(jobType) {
		return s.registry.DefaultMaxRetries(jobType)
	}
	return 3
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

	// Issue 4 (June 2026, P1): MaxRetries fallback is now registry-aware.
	j.MaxRetries = s.resolveMaxRetries(j.Type, j.MaxRetries)

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

func generateJobID() string {
	return fmt.Sprintf("job_%d_%s", time.Now().UnixNano(), hashutil.RandomString(8))
}
