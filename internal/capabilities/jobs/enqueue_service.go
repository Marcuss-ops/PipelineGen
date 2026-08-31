// Package jobs — enqueue_service.go: Enqueue orchestrator + idempotency pipeline.
//
// PR-GODOBJ-6 (July 2026): mechanically extracted from service.go
// per the god-object decomposition plan.
//
// 2026-07-06 (Phase 1 decomposition): further split per Pattern 5:
//
//	enqueue_validate.go — validateEnqueueRequest + MaxPayloadSize (input validation)
//	enqueue_retry.go    — resolveMaxRetries (typed lookup contract)
//	enqueue_id.go       — generateJobID (identity)
//
// Zero behavior changes. Same-package visibility preserves all caller paths.
//
// PR-jobs-retry-contract (July 2026): the typed sqlite3.Error UNIQUE-constraint
// rescue probe replaces the pre-PR strings.Contains("UNIQUE constraint")
// heuristic (godlike/07 driver-invariant typed-error contract).
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"
	"go.uber.org/zap"

	jobqueue "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/pkg/background"
	corid "github.com/Marcuss-ops/PipelineGen/pkg/corid"
)

const correlationLookupTimeout = 2 * time.Second

// Enqueue enqueues a job from a domain request. Implements job.Service.
//
// FASE 2 observability (kernel/observability): when the enqueue happens
// inside a parent run's execution context (job-handler fan-out — script
// items, voiceover siblings, image siblings), the outcome is registered
// on the parent run's children summary. A NEW row → requested child; an
// enqueue error → failed child. Idempotent returns of an already-existing
// job are NOT re-registered (no double counting). This is the single
// canonical child-creation point: every child path routes through here
// with the parent's ctx.
func (s *Service) Enqueue(ctx context.Context, req *job.EnqueueRequest) (ret *job.Job, retErr error) {
	// Child-run linkage (nil-tolerant: no run bound = plain enqueue,
	// e.g. API-triggered).
	parentRun := kernobs.FromContext(ctx)
	created := false
	if parentRun != nil {
		defer func() {
			switch {
			case created && ret != nil:
				parentRun.RegisterChild(&kernobs.RunReport{
					JobID:   ret.ID,
					JobType: ret.Type,
					Status:  kernobs.StatusRunning,
				})
			case !created && retErr != nil && req != nil:
				parentRun.RegisterChild(&kernobs.RunReport{
					JobType: req.Type,
					Status:  kernobs.StatusFailed,
				})
			}
		}()
	}

	if err := validateEnqueueRequest(req); err != nil {
		return nil, err
	}

	// Idempotency: auto-inject correlation_id from the request context
	// into a local value. Do not mutate the caller-owned request: callers
	// may safely reuse one request concurrently, and enqueue serialization
	// is intentionally provided by SQLite rather than this service.
	correlationID := req.CorrelationID
	if correlationID == "" {
		if cid := corid.FromContext(ctx); cid != "" {
			correlationID = cid
		}
	}
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
	if correlationID != "" {
		existing, err := s.findExistingByCorrelation(ctx, req.Type, correlationID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			s.log.Info("returning existing job with same (type, correlation_id)",
				zap.String("job_id", existing.ID),
				zap.String("type", req.Type),
				zap.String("correlation_id", correlationID),
			)
			return existing, nil
		}
	}

	// PG-M2M (Aug 2026): idempotency on (client_id, idempotency_key).
	// Distinct from (type, correlation_id) above: the M2M surface uses a
	// caller-controlled, per-client dedup key so two different clients
	// can legitimately reuse the same key string without colliding. The
	// pre-check mirrors the correlation_id path; the rescue on
	// UNIQUE-constraint collision (idx_jobs_client_idempotency) is
	// handled in the Create error branch below. Skipped for
	// admin/internal enqueues where either field is empty.
	if req.ClientID != "" && req.IdempotencyKey != "" {
		existing, err := s.findExistingByClientAndIdempotency(ctx, req.ClientID, req.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			s.log.Info("returning existing job with same (client_id, idempotency_key)",
				zap.String("job_id", existing.ID),
				zap.String("client_id", req.ClientID),
				zap.String("idempotency_key", req.IdempotencyKey),
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
		ID:             generateJobID(),
		Type:           req.Type,
		Status:         job.StatusQueued,
		Priority:       req.Priority,
		Project:        req.Project,
		VideoName:      req.VideoName,
		Payload:        payload,
		RetryCount:     0,
		MaxRetries:     req.MaxRetries,
		Progress:       0,
		CreatedAt:      now,
		UpdatedAt:      now,
		ActiveKey:      req.ActiveKey,
		CorrelationID:  correlationID,
		ClientID:       req.ClientID,
		IdempotencyKey: req.IdempotencyKey,
	}

	// Step 6 (July 2026): fail-closed handler gate. When the dispatcher is
	// wired (non-nil), reject enqueue for job types that have no registered
	// handler — a job enqueued without a consumer will sit in the queue
	// forever and never be claimed. The gate is skipped when the dispatcher
	// is nil (test/minimal compositions that don't wire handlers) to avoid
	// spurious idempotency-key / correlation-id rejections. The dispatcher’s
	// AllHandlers() map is the canonical source of truth for handler presence.
	//
	// godlike/07 NO-FAKE-AVAILABILITY: pre-Step-6, this check was absent.
	// Jobs for unregistered types were silently accepted (HTTP 200), rows
	// accumulated in the jobs table, and the only diagnostic was the /ready
	// handlers check (which only covers 2 required types). The gate closes
	// the silent-queue-buildup class by surfacing the typed sentinel at
	// enqueue time.
	if s.dispatcher != nil {
		if err := jobqueue.RequireConsumer(j.Type, s); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrNoHandlerForJobType, j.Type)
		}
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

	// Canonical correlation (godlike/06 SSOT): parent_job_id and
	// root_job_id are resolved ONCE at enqueue and persisted on the jobs
	// row, so derived projections (performance_runs.root_job_id, the
	// control-plane verifier) never have to re-derive or guess the lineage.
	// A root job (no parent linkage in the payload) is its own root; a child
	// inherits its parent's already-resolved root. Resolution failures fail
	// open to the immediate parent id — the lineage hint must never block the
	// enqueue itself.
	j.ParentJobID = job.ParentLinkFromPayload(j.Payload).ParentJobID
	j.RootJobID = j.ID
	if j.ParentJobID != "" {
		j.RootJobID = j.ParentJobID
		if parent, err := s.repo.Get(ctx, j.ParentJobID); err == nil && parent != nil && parent.RootJobID != "" {
			j.RootJobID = parent.RootJobID
		}
	}

	if err := s.repo.Create(ctx, j); err != nil {
		// PR-jobs-retry-contract (July 2026): typed sqlite3 probe replaces
		// the pre-PR strings.Contains(err.Error(), "UNIQUE constraint")
		// string-compare. Driver-invariant (mattn/go-sqlite3.Error is an
		// exported struct with int-backed Code (ErrNo) + ExtendedCode
		// (ErrNoExtended) fields — neither depends on the error string
		// format, so a future driver string change cannot silently disable
		// the rescue path).
		if correlationID != "" || req.ActiveKey != "" || (req.ClientID != "" && req.IdempotencyKey != "") {
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
			// probe still discriminates UNIQUE-constraint failures		// from generic create-job errors (the godlike/07
			// typed-string-compare anti-pattern is NOT reintroduced).
			// SILENT LOGIC BUG surfaced by this audit: the pre-commit-9
			// int() cast compared 19 (base constraint code) against 2067
			// (UNIQUE extended code) — these NEVER matched, so the
			// rescue path was effectively DEAD CODE. Every UNIQUE-constraint
			// race condition returned the generic "failed to create job"
			// error instead of the existing job.
			if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
				if correlationID != "" {
					if existing, findErr := s.findExistingByCorrelation(ctx, j.Type, correlationID); findErr == nil && existing != nil {
						s.log.Info("returning existing job by (type, correlation_id) — caught race on SQLite UNIQUE constraint (typed sqlite3 probe)",
							zap.String("job_id", existing.ID),
							zap.String("type", j.Type),
							zap.String("correlation_id", correlationID),
						)
						return existing, nil
					}
				}
				if req.ActiveKey != "" {
					if existing, findErr := s.repo.FindActiveByKey(ctx, req.ActiveKey); findErr == nil && existing != nil && !existing.IsTerminal() {
						s.log.Info("returning existing job by active key — caught race on SQLite UNIQUE constraint",
							zap.String("job_id", existing.ID),
							zap.String("active_key", req.ActiveKey),
						)
						return existing, nil
					}
				}
				// PG-M2M (Aug 2026): rescue on (client_id, idempotency_key)
				// UNIQUE collision (idx_jobs_client_idempotency).
				if req.ClientID != "" && req.IdempotencyKey != "" {
					if existing, findErr := s.repo.FindByClientAndIdempotencyKey(ctx, req.ClientID, req.IdempotencyKey); findErr == nil && existing != nil {
						s.log.Info("returning existing job by (client_id, idempotency_key) — caught race on SQLite UNIQUE constraint",
							zap.String("job_id", existing.ID),
							zap.String("client_id", req.ClientID),
							zap.String("idempotency_key", req.IdempotencyKey),
						)
						return existing, nil
					}
				}
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
				// driver error chain for diagnostics (Go 1.20+).					return nil, fmt.Errorf("%w: %w", ErrUniqueConstraintViolation, err)
			}
		}
		return nil, fmt.Errorf("failed to create job: %w", err)
	}
	created = true

	s.log.Info("job enqueued",
		zap.String("job_id", j.ID),
		zap.String("type", j.Type),
		zap.String("correlation_id", j.CorrelationID),
	)
	return j, nil
}

func (s *Service) findExistingByCorrelation(ctx context.Context, jobType, correlationID string) (*job.Job, error) {
	if correlationID == "" {
		return nil, nil
	}

	lookupCtx, cancel := background.DetachWithTimeout(ctx, "jobs-correlation-lookup", correlationLookupTimeout)
	defer cancel()

	existing, err := s.repo.FindByTypeAndCorrelation(lookupCtx, jobType, correlationID)
	if err == nil {
		return existing, nil
	}

	if isTransientCorrelationLookupError(err) {
		s.log.Warn("job correlation lookup unavailable; proceeding without pre-check",
			zap.String("type", jobType),
			zap.String("correlation_id", correlationID),
			zap.Error(err),
		)
		return nil, nil
	}

	return nil, fmt.Errorf("failed to check existing job by correlation: %w", err)
}

// findExistingByClientAndIdempotency is the M2M counterpart of
// findExistingByCorrelation. It probes the (client_id, idempotency_key)
// pair via FindByClientAndIdempotencyKey with the same detached-ctx
// timeout + transient-error tolerance so a transient DB hiccup does not
// block the enqueue (the UNIQUE constraint is the backstop). Mirrors
// the correlation_id path verbatim so the two idempotency surfaces
// behave identically under store instability (PG-M2M, Aug 2026).
func (s *Service) findExistingByClientAndIdempotency(ctx context.Context, clientID, idempotencyKey string) (*job.Job, error) {
	if clientID == "" || idempotencyKey == "" {
		return nil, nil
	}

	lookupCtx, cancel := background.DetachWithTimeout(ctx, "jobs-m2m-idempotency-lookup", correlationLookupTimeout)
	defer cancel()

	existing, err := s.repo.FindByClientAndIdempotencyKey(lookupCtx, clientID, idempotencyKey)
	if err == nil {
		return existing, nil
	}

	if isTransientCorrelationLookupError(err) {
		s.log.Warn("job M2M idempotency lookup unavailable; proceeding without pre-check",
			zap.String("client_id", clientID),
			zap.String("idempotency_key", idempotencyKey),
			zap.Error(err),
		)
		return nil, nil
	}

	return nil, fmt.Errorf("failed to check existing job by client+idempotency_key: %w", err)
}

func isTransientCorrelationLookupError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}
