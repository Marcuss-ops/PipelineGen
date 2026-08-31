// Package operations — generation_submission_types.go is the
// canonical types + Service-orchestrator surface for FASE 2
// submission. The Submit method is the SOLE public entry point;
// it is intentionally small (~17 lines) and delegates to the
// per-responsibility siblings:
//
//   - generation_submission_decision.go
//     validateSubmitRequest, lookupPriorOperation,
//     decideReplayOrFresh
//   - generation_submission_transaction.go
//     persistSubmit (the SOLE place where the *sql.Tx is opened
//     and closed; the four typed ports participate via the same
//     tx so operations + jobs + outbox commit or roll back
//     together)
//   - generation_submission_ids.go
//     defaultJobIDGen, defaultOperationIDGen, generateID,
//     randomHexSuffix (pure entropy helpers)
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// FASE 2 submission flow. The HTTP layer in
// `internal/capabilities/script/handler_enqueue.go` consumes ONLY the
// typed `Submit` signature declared here; the handler must NOT
// bypass it with direct repository access (godlike/07 fail-
// closed at the application boundary).
//
// godlike/07 minimum-blast-radius: the service depends on 5
// narrow ports (defined in ports.go) so a hand-rolled fake
// can replace every dependency in unit tests. The canonical
// concrete adapters live in
// `internal/platform/sqlite/{jobs,outboxevents,operations}`
// and the composition root in `internal/app` wires them up.
//
// Thread safety: a single `submitMu sync.Mutex` serialises all
// Submit calls on the same process. SQLite single-writer
// semantics (`database/sql`'s BeginTx uses DEFERRED isolation
// by default — a 2nd BeginTx in the same process on the same
// DB will block until the 1st commits or rolls back) require
// the application-level mutex to avoid spurious
// `SQLITE_BUSY` errors. The mutex is intentionally independent
// of the existing `jobs.Service.enqueueMu` (the canonical
// jobs.Service is unchanged; the submission service is a
// new typed entry point).
package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// SubmitRequest is the canonical FASE 2 submission input.
//
// godlike/06 SSOT: this struct is the SOLE canonical
// submission-input shape. The HTTP layer in
// `internal/capabilities/script/handler_enqueue.go` MUST build a
// SubmitRequest from the inbound HTTP envelope + headers and
// pass it verbatim to `Submit`. Direct `Service.Enqueue` calls
// on the underlying `jobs.Service` are FORBIDDEN in the
// script.generate HTTP path (FASE 2 audit-pin — the old
// `enqueueEnvelopeFn` was a godlike/07 god-object that
// duplicated the application-layer concern).
//
// Field semantics:
//   - Scope: canonical operations.Scope (`ScopeScriptGenerate`).
//   - IdempotencyKey: caller-supplied 255-char opaque string
//     from the `Idempotency-Key` HTTP header. Validated by
//     `operations.IsValidIdempotencyKey` (printable ASCII,
//     1..255 chars). Whitespace-trimmed by the HTTP layer.
//   - RequestHash: caller-supplied 64-char lowercase hex
//     SHA-256 fingerprint of the request body. The HTTP
//     layer derives it from the canonical envelope identity
//     helper (or, for FASE 2, computes
//     `sha256.Sum256(BuildEnvelopeIdentity(env))` and emits
//     the 64-char hex). Validated by
//     `operations.IsValidRequestHash`.
//   - ForceRefresh: when true, the service ALWAYS creates a
//     new operation row (and, if a prior operation exists
//     in the same (scope, key) bucket, sets
//     `supersedes_operation_id` and flips the prior to
//     SUPERSEDED in the same atomic TX). Bypasses the
//     409 IDEMPOTENCY_CONFLICT gate (the conflict is the
//     intended outcome of a force_refresh).
//   - JobType: the canonical job type to enqueue
//     (`script.generate`). The service uses this for the
//     jobs.Type column. Job type dispatch (handler lookup)
//     is the canonical jobs.Service's concern, NOT the
//     submission service's.
//   - JobPayload: the canonical JSON payload to persist on
//     the jobs row (`script.generate` envelope bytes,
//     marshalled by the HTTP layer).
//   - JobPriority: 0..N priority for the worker queue
//     dispatch. Mirrored on the jobs.priority column.
//   - JobMaxRetries: -1 = unlimited, 0 = no retry, N = retry
//     up to N times. Mirrored on the jobs.max_retries
//     column. The pre-FASE-2 path sourced this from
//     `appjobs.Registry.DefaultMaxRetries(jType)` — the
//     FASE 2 caller MUST pass the resolved value explicitly
//     (the submission service does NOT inspect the
//     canonical registry to keep the service scope-agnostic).
//   - OperationID, JobID: optional pre-generated IDs. When
//     empty, the service generates them via
//     `time.Now().UnixNano()` + crypto/rand suffix (the
//     same shape as `generateJobID` in the canonical jobs
//     package). The caller MAY pass pre-generated IDs to
//     enable deterministic test fixtures.
type SubmitRequest struct {
	Scope          Scope
	IdempotencyKey string
	RequestHash    string
	ForceRefresh   bool

	JobType       string
	JobPayload    json.RawMessage
	JobPriority   int
	JobMaxRetries int

	// Optional pre-generated IDs. Empty → service generates.
	OperationID string
	JobID       string
}

// SubmitResult is the canonical FASE 2 submission output.
//
// godlike/06 SSOT: this struct is the SOLE canonical output
// of `Submit`. The HTTP layer maps the bool flags to the
// canonical HTTP status codes (IsIdempotencyHit → 200 OK
// with the existing operation; otherwise 202 Accepted with
// the new operation). The `Operation` field is always
// non-nil on success (any non-nil error path means the
// service did NOT commit a row).
//
// Field semantics:
//   - Operation: the operation row in its committed state
//     (QUEUED for new + supersede; the existing row returned
//     verbatim for idempotency hit).
//   - IsIdempotencyHit: true when the service found an
//     existing operation in the same (scope, key) bucket
//     with the SAME request_hash and returned it without
//     creating a new row. False when a new operation was
//     created (first-time submission OR force_refresh).
//   - IsSupersede: true when the new operation was created
//     with `supersedes_operation_id` set (force_refresh on
//     a bucket with a prior operation). Mutually exclusive
//     with `IsIdempotencyHit` (a force_refresh can never
//     be an idempotency hit — the caller explicitly asked
//     for a new operation).
type SubmitResult struct {
	Operation *Operation
	// Job is the canonical live Job state. On fresh submission
	// and supersede paths, this is the Job just INSERTed in the
	// atomic TX (no extra DB read needed). On the idempotency-
	// hit path, the Job is fetched OUTSIDE the TX via JobGetter
	// so the caller observes the canonical post-terminal Job
	// state (e.g. SUCCEEDED, FAILED) instead of a stale QUEUED
	// snapshot. May be nil if JobGetter fails (logged as a typed
	// warn; caller treats as advisory).
	Job              *job.Job
	IsIdempotencyHit bool
	IsSupersede      bool
}

// Service is the canonical FASE 2 submission service. It owns
// the atomic-TX shape: operations + jobs + outbox_events
// commit TOGETHER or roll back TOGETHER. There is exactly one
// exported entry point (`Submit`); all other methods are
// unexported helpers (split among the per-responsibility
// sibling files).
type Service struct {
	ops       OperationsRepository
	jobs      JobEnqueuer
	jobGetter JobGetter
	outbox    OutboxEmitter
	txMgr     TxManager
	jobIDGen  func() string
	opIDGen   func() string
	log       *zap.Logger
	submitMu  sync.Mutex
	nowFunc   func() time.Time // injectable for tests; defaults to time.Now
}

// NewService constructs the canonical submission service.
//
// godlike/07 fail-closed: every dependency is REQUIRED. A nil
// dep panics at construction time (composition-bug detection).
// The caller owns the service's lifecycle — the composition
// root in `internal/app` is the SOLE constructor caller.
// jobGetter supplies canonical live Job state on the
// idempotency-hit replay path (FASE 2 close-out).
func NewService(
	ops OperationsRepository,
	jobs JobEnqueuer,
	jobGetter JobGetter,
	outbox OutboxEmitter,
	txMgr TxManager,
	log *zap.Logger,
) *Service {
	if ops == nil {
		panic("operations.NewService: OperationsRepository is nil (composition bug)")
	}
	if jobs == nil {
		panic("operations.NewService: JobEnqueuer is nil (composition bug)")
	}
	if jobGetter == nil {
		panic("operations.NewService: JobGetter is nil (composition bug)")
	}
	if outbox == nil {
		panic("operations.NewService: OutboxEmitter is nil (composition bug)")
	}
	if txMgr == nil {
		panic("operations.NewService: TxManager is nil (composition bug)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		ops:       ops,
		jobs:      jobs,
		jobGetter: jobGetter,
		outbox:    outbox,
		txMgr:     txMgr,
		jobIDGen:  defaultJobIDGen,
		opIDGen:   defaultOperationIDGen,
		log:       log,
		nowFunc:   time.Now,
	}
}

// Submit is the canonical FASE 2 entry point.
//
// The method is intentionally small (~17 lines): it orchestrates
// three sibling helpers — validateSubmitRequest (decision.go),
// lookupPriorOperation + decideReplayOrFresh (decision.go),
// and persistSubmit (transaction.go) — without itself containing
// any input validation, decision branching, or transaction
// boundaries. The submission flow's atomicity guarantees come
// from the SOLE-LONE position of persistSubmit in the package
// (godlike/06 single-transaction-owner).
//
// Atomic-TX shape (godlike/06 SSOT — the SOLE canonical flow):
//  1. validateSubmitRequest (decision.go): fail-closed at input.
//  2. submitMu mutex acquisition.
//  3. lookupPriorOperation (decision.go): READ prior via
//     OperationsRepository.GetLatestForKey; treat
//     StateSuperseded as "no prior" (Push 2.2a HIGH fix).
//  4. decideReplayOrFresh (decision.go): idempotency hit
//     (returns SubmitResult, no DB write) / idempotency
//     conflict (returns typed WrapIdempotencyConflict error,
//     no DB write) / fresh or force_refresh supersede
//     (mustPersist=true, calls persistSubmit) /
//     ErrSelfSupersedeReference (pre-tx guard, no DB write).
//  5. persistSubmit (transaction.go): the SOLE owner of
//     txMgr.BeginTx + tx.Commit + tx.Rollback + the four
//     typed-port calls (ops.Insert, ops.UpdateState,
//     jobs.CreateInTx, outbox.Enqueue).
//
// Any error in steps 5-10 of persistSubmit triggers Rollback
// (deferred inside persistSubmit). The prior-op UpdateState
// is part of the same TX so it commits/rolls-back together
// with the new-op Insert (force_refresh supersede path).
func (s *Service) Submit(ctx context.Context, req SubmitRequest) (*SubmitResult, error) {
	if err := validateSubmitRequest(req); err != nil {
		return nil, err
	}
	s.submitMu.Lock()
	defer s.submitMu.Unlock()

	prior, err := s.lookupPriorOperation(ctx, req.Scope, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("operations.Submit: lookup prior: %w", err)
	}

	result, mustPersist, err := s.decideReplayOrFresh(ctx, prior, req)
	if err != nil {
		return nil, err
	}
	if !mustPersist {
		return result, nil
	}

	return s.persistSubmit(ctx, req, prior, s.nowFunc())
}
