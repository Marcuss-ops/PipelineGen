// Package jobs — enqueue_service.go: Enqueue + idempotency pipeline.
//
// PR-GODOBJ-6 (July 2026): mechanically extracted from service.go
// per the god-object decomposition plan. Zero behavior changes.
//
// PR-jobs-retry-contract (July 2026): tighten the *Service*-side
// MaxRetries resolution path to a canonical typed lookup via
// Registry.GetMaxRetries(jobType). Removed the pre-PR legacy
// hard-coded 3-retry fallback for unregistered types; the strict
// typed-error contract propagates ErrMaxRetriesUnknown to the caller.
// Also replaced the pre-PR strings.Contains(err.Error(), "UNIQUE
// constraint") idempotency-rescue probe with a typed sqlite3.Error
// probe (`errors.As(&sqliteErr)` +
// `sqliteErr.ExtendedCode==sqlite3.ErrConstraintUnique`) so a future
// driver string change does not silently disable the rescue path
// (godlike/07 NO-FAKE-AVAILABILITY).
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

// MaxPayloadSize is the maximum allowed size for a serialized job payload in bytes.
const MaxPayloadSize = 1 << 20 // 1 MB

// resolveMaxRetries encodes the strict typed MaxRetries fallback
// semantic in a single testable helper. Enqueue() delegates to this
// helper so the logic is decoupled from repo/dispatcher concerns
// (test fixtures only need typed Service+Registry wiring).
//
// Three-way semantics, in priority order:
//
//  1. currentMR < 0  → 0      (explicit "no retries" sentinel —
//     pre-Issue-4 behavior preserved verbatim).
//
//  2. currentMR > 0  → currentMR  (caller pre-set value preserved
//     verbatim; registry is the fallback, not an override).
//
//  3. currentMR == 0 → registry.GetMaxRetries(jobType) (strict
//     typed lookup; the registry MUST already be attached at
//     construction time per the 4-arg NewService fail-closed
//     constructor). Unknown jobTypes return ErrMaxRetriesUnknown —
//     the caller (Enqueue) propagates the error so a missing
//     registration is loud, NOT silenced by a legacy 3-retry
//     fallback (PR-jobs-retry-contract removes the legacy
//     `return 3` line per godlike/07 NO-FAKE-AVAILABILITY).
//
// godlike/06 SSOT: this strict lookup supersedes the pre-PR
// hasRegistry() guard + the legacy `return 3` line. Removing those
// shapes eliminates two silent-success surfaces in one sweep.
func (s *Service) resolveMaxRetries(jobType string, currentMR int) (int, error) {
	if currentMR < 0 {
		return 0, nil
	}
	if currentMR > 0 {
		return currentMR, nil
	}
	// currentMR == 0 — single typed lookup.
	if s.registry == nil {
		// Defense-in-depth — should be unreachable given 4-arg NewService.
		return 0, ErrRegistryRequired
	}
	return s.registry.GetMaxRetries(jobType)
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

	// PR-jobs-retry-contract (July 2026): strict typed MaxRetries resolution
	// via Registry.GetMaxRetries. Errors propagate (no silent fallback).
	maxRetries, err := s.resolveMaxRetries(j.Type, j.MaxRetries)
	if err != nil {
		return nil, err
	}
	j.MaxRetries = maxRetries

	if j.Payload == nil || len(j.Payload) == 0 || string(j.Payload) == "null" {
		j.Payload = json.RawMessage("{}")
	}

	if err := s.repo.Create(ctx, j); err != nil {
		// PR-jobs-retry-contract (July 2026): typed sqlite3 probe replaces
		// the pre-PR strings.Contains(err.Error(), "UNIQUE constraint")
		// string-compare. Driver-invariant (mattn/go-sqlite3.Error is an
		// exported struct with int-backed Code (ErrNo) + ExtendedCode
		// (ErrNoExtended) fields — neither depends on the error string
		// format, so a future driver string change cannot silently disable
		// the rescue path).
		if j.CorrelationID != "" {
			var sqliteErr sqlite3.Error
			// PR-JOBS-SQLITE3-PROBE-FIX (commit-9, 2026-07-05): canonical
			// ExtendedCode == sqlite3.ErrConstraintUnique comparison.
			// Per mattn/go-sqlite3 idioms, ErrConstraintUnique is of
			// type sqlite3.ErrNoExtended (= sqlite3.ErrConstraint.Extend(8),
			// value 2067 = 19 + 8*256) and matches the ExtendedCode
			// field. The pre-commit-9 int() cast on Code (which compared
			// the base constraint code 19 against the UNIQUE extended
			// code 2067 — NEVER matching) is RETIRED; the canonical
			// pattern is direct typed equality between matching
			// sqlite3.ErrNoExtended values, with no int() cast. The
			// probe still discriminates UNIQUE-constraint failures
			// from generic create-job errors (the godlike/07
			// typed-string-compare anti-pattern is NOT reintroduced).
			// SILENT LOGIC BUG surfaced by this audit: the pre-commit-9
			// int() cast compared 19 (base constraint code) against 2067
			// (UNIQUE extended code) — these NEVER matched, so the
			// rescue path was effectively DEAD CODE. Every UNIQUE-constraint
			// race condition returned the generic "failed to create job"
			// error instead of the existing job.
			if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
				if existing, findErr := s.repo.FindByTypeAndCorrelation(ctx, j.Type, j.CorrelationID); findErr == nil && existing != nil {
					s.log.Info("returning existing job by (type, correlation_id) — caught race on SQLite UNIQUE constraint (typed sqlite3 probe)",
						zap.String("job_id", existing.ID),
						zap.String("type", j.Type),
						zap.String("correlation_id", j.CorrelationID),
					)
					return existing, nil
				}
				// typed probe fired but no existing job found — propagate
				// as typed sentinel so callers can errors.Is it.
				// typed probe fired but no existing job found — propagate
				// as typed sentinel so callers can errors.Is it.
				//
				// PR-jobs-retry-contract behavioral audit-pin: this path
				// REPLACES the pre-PR `return nil, fmt.Errorf("failed to
				// create job: %w", err)` wrapper. The "failed to create
				// job" string framing has been RETIRED in favor of the
				// typed ErrUniqueConstraintViolation sentinel (godlike/06
				// SSOT: typed sentinels are the canonical owner of error
				// classification). Existing callers that branched on the
				// "failed to create job" substring MUST migrate to
				// errors.Is(err, ErrUniqueConstraintViolation) — the
				// pre-PR substring is now gone. The double-`%w` wrap
				// preserves both the typed sentinel AND the underlying
				// driver error chain for diagnostics (Go 1.20+).
				return nil, fmt.Errorf("%w: %w", ErrUniqueConstraintViolation, err)
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
