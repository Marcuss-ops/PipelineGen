// Package operations — generation_submission_service.go is the
// canonical FASE 2 use case: a single `Submit` entry point that
// owns validation, idempotency, atomic operation+job+outbox TX,
// and the typed-error surface for the HTTP layer.
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// FASE 2 submission flow. The HTTP layer in
// `internal/api/script/handler_enqueue.go` consumes ONLY the
// typed `Submit` signature declared here; the handler must NOT
// bypass it with direct repository access (godlike/07 fail-
// closed at the application boundary).
//
// godlike/07 minimum-blast-radius: the service depends on 4
// narrow ports (defined in ports.go) so a hand-rolled fake
// can replace every dependency in unit tests. The canonical
// concrete adapters live in
// `internal/infrastructure/database/sqlite/{jobs,outboxevents,operations}`
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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	domainops "github.com/Marcuss-ops/PipelineGen/internal/domain/operations"
)

// FASE 2 canonical event_type for the outbox row that the
// submission service emits in the same atomic TX as the
// operations INSERT + jobs INSERT. The worker that drains
// the outbox reads this event_type to route the event to the
// canonical `script.generate` consumer.
const EventTypeScriptGenerateQueued = "script.generate.queued"

// aggregateTypeScriptGenerate is the canonical "scope" name
// carried on the outbox row's aggregate_type column. Mirrors
// the operations.Scope value (`"script.generate"`).
const aggregateTypeScriptGenerate = "script.generate"

// SubmitRequest is the canonical FASE 2 submission input.
//
// godlike/06 SSOT: this struct is the SOLE canonical
// submission-input shape. The HTTP layer in
// `internal/api/script/handler_enqueue.go` MUST build a
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
	Scope          domainops.Scope
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
	Operation        *domainops.Operation
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
// unexported helpers.
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

// defaultJobIDGen produces a unique job ID with the canonical
// `job_<unix_nano>_<8hex>` shape. The 8-char hex suffix is
// derived from crypto/rand (16 bits × 4 bits = ~32 bits
// of entropy) which is sufficient for the FASE 2 Submit
// concurrency range (the submitMu mutex serialises Submits
// on the same process; the suffix breaks the same-nanosecond
// tie).
func defaultJobIDGen() string {
	return generateID("job")
}

// defaultOperationIDGen produces a unique operation ID with
// the canonical `op_<unix_nano>_<8hex>` shape.
func defaultOperationIDGen() string {
	return generateID("op")
}

// Submit is the canonical FASE 2 entry point.
//
// Atomic-TX shape (godlike/06 SSOT — the SOLE canonical flow):
//  1. Validate input (scope, idempotency_key, request_hash,
//     job_type, job_payload). Fail-closed at the input
//     boundary (godlike/07).
//  2. Acquire `submitMu` mutex (serialise SQLite writes on
//     this process).
//  3. Lookup prior operation via `GetLatestForKey` (READ —
//     no TX needed, but inside the mutex for consistency).
//  4. Decide the SubmitResult outcome:
//     - force_refresh=false, no prior → create new (IsSupersede=false, IsIdempotencyHit=false).
//     - force_refresh=false, prior with SAME hash → idempotency hit (return prior, IsIdempotencyHit=true).
//     - force_refresh=false, prior with DIFFERENT hash → ErrIdempotencyConflict (no DB write).
//     - force_refresh=true, no prior → create new (IsSupersede=false; no supersedes link).
//     - force_refresh=true, prior exists → create new + set supersedes + flip prior to SUPERSEDED.
//  5. Stamp `now := time.Now()` ONCE — used for both the
//     operation's CreatedAt/UpdatedAt AND the job's
//     CreatedAt/UpdatedAt, guaranteeing audit-invariant
//     alignment (the operation and its job were committed
//     at the same instant).
//  6. Begin TX.
//  7. INSERT operations row (new).
//  8. If supersede: UPDATE prior op's state to SUPERSEDED.
//  9. INSERT jobs row via JobEnqueuer.CreateInTx.
//  10. INSERT outbox_events row via OutboxEmitter.Enqueue
//     (event_type=`script.generate.queued`, event_key=operation_id,
//     aggregate_id=operation_id, aggregate_type=`script.generate`).
//  11. Commit TX (or Rollback on any error).
//  12. Release mutex, return SubmitResult.
//
// Any error in steps 7-10 triggers Rollback. The prior-op
// UpdateState is part of the same TX so it commits/rolls-back
// together with the new-op Insert.
func (s *Service) Submit(ctx context.Context, req SubmitRequest) (*SubmitResult, error) {
	// ── Step 1: validate input ──────────────────────────────────────
	if !req.Scope.IsValid() {
		return nil, fmt.Errorf("%w: %q", domainops.ErrInvalidOperationScope, req.Scope)
	}
	if !domainops.IsValidIdempotencyKey(req.IdempotencyKey) {
		return nil, domainops.ErrIdempotencyKeyInvalid
	}
	if !domainops.IsValidRequestHash(req.RequestHash) {
		return nil, domainops.ErrRequestHashInvalid
	}
	if req.JobType == "" {
		return nil, fmt.Errorf("operations.Submit: empty JobType")
	}
	if len(req.JobPayload) == 0 {
		return nil, fmt.Errorf("operations.Submit: empty JobPayload")
	}

	// ── Step 2: acquire mutex ───────────────────────────────────────
	s.submitMu.Lock()
	defer s.submitMu.Unlock()

	// ── Step 3: lookup prior op ─────────────────────────────────────
	prior, err := s.ops.GetLatestForKey(ctx, req.Scope, req.IdempotencyKey, nil)
	if err != nil {
		return nil, fmt.Errorf("operations.Submit: lookup prior: %w", err)
	}

	// ── Step 4: decide outcome ───────────────────────────────────────
	//
	// Push 2.2a (HIGH severity code-review fix): a prior operation
	// marked SUPERSEDED is NOT an "active" prior for the lookup —
	// the user already ran a force_refresh and the prior was
	// replaced. Treating it as a normal prior would (a) return a
	// terminal-state op as an "idempotency hit" or (b) chain a
	// new supersedes link to the wrong row. The fix is to treat
	// SUPERSEDED priors as "no prior" and fall through to a fresh
	// INSERT.
	if prior != nil && prior.State == domainops.StateSuperseded {
		prior = nil
	}

	// Push 2.2a (MEDIUM severity code-review fix): a caller
	// pre-supplying `req.OperationID == prior.OperationID` and
	// force_refresh=true would surface as `ErrSelfSupersedeReference`
	// from the repository's `validateForWrite` AFTER BeginTx. The
	// TX rolls back cleanly, but the error arrives mid-flow
	// instead of at the input boundary. Detect and surface the
	// typed sentinel here.
	if prior != nil && req.OperationID != "" && req.OperationID == prior.OperationID {
		return nil, fmt.Errorf("%w: operation_id=%q",
			domainops.ErrSelfSupersedeReference, req.OperationID)
	}

	switch {
	case prior == nil:
		// No prior — fresh submission. Falls through to INSERT.
	case !req.ForceRefresh && prior.RequestHash == req.RequestHash:
		// Idempotency hit — same key, same hash, no force_refresh.
		//
		// FASE 2 close-out: read canonical live Job state via
		// JobGetter so the HTTP layer surfaces the canonical
		// post-terminal Job.Status on replay (no longer a stale
		// QUEUED snapshot from the prior operation row). A
		// JobGetter failure is treated as advisory (typed warn
		// logged; SubmitResult.Job is nil; caller proceeds with
		// the Operation only — the canonical job snapshot is
		// still available via GET /api/jobs/{id}/full).
		s.log.Info("operations.Submit: idempotency hit",
			zap.String("operation_id", prior.OperationID),
			zap.String("scope", string(req.Scope)),
			zap.String("idempotency_key", req.IdempotencyKey),
		)
		var canonicalJob *job.Job
		canonicalJob, jobErr := s.jobGetter.Get(ctx, prior.JobID)
		if jobErr != nil {
			s.log.Warn("operations.Submit: canonical job lookup failed on replay",
				zap.String("operation_id", prior.OperationID),
				zap.String("job_id", prior.JobID),
				zap.Error(jobErr),
			)
		}
		return &SubmitResult{
			Operation:        prior,
			Job:              canonicalJob,
			IsIdempotencyHit: true,
		}, nil
	case !req.ForceRefresh && prior.RequestHash != req.RequestHash:
		// 409 IDEMPOTENCY_CONFLICT — same key, different hash.
		// No DB write. Caller (HTTP layer) maps to 409.
		return nil, domainops.WrapIdempotencyConflict(
			req.Scope, req.IdempotencyKey, prior.RequestHash, req.RequestHash)
	case req.ForceRefresh:
		// Force-refresh on a prior op — falls through to INSERT
		// with supersedes_operation_id set + UPDATE prior to
		// SUPERSEDED in the same atomic TX.
	default:
		// Defensive: unreachable (all cases covered).
		return nil, fmt.Errorf("operations.Submit: unreachable state (prior=%+v, force_refresh=%v)", prior, req.ForceRefresh)
	}

	// ── Step 5: stamp now ───────────────────────────────────────────
	now := s.nowFunc()

	// ── Step 6: begin TX ────────────────────────────────────────────
	tx, err := s.txMgr.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("operations.Submit: BeginTx: %w", err)
	}
	// Rollback is a no-op after a successful Commit (driver-level),
	// so the deferred Rollback handles the early-return + error paths
	// uniformly. The double-rollback protection is the driver contract.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// ── Step 7: INSERT new operation ─────────────────────────────────
	operationID := req.OperationID
	if operationID == "" {
		operationID = s.opIDGen()
	}
	jobID := req.JobID
	if jobID == "" {
		jobID = s.jobIDGen()
	}

	newOp := &domainops.Operation{
		OperationID:    operationID,
		Scope:          req.Scope,
		IdempotencyKey: req.IdempotencyKey,
		RequestHash:    req.RequestHash,
		JobID:          jobID,
		State:          domainops.StateQueued,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if prior != nil {
		// Step 4 above guarantees this branch only fires when
		// req.ForceRefresh == true (a non-supersede same-hash
		// path returned an idempotency hit before reaching here).
		newOp.SupersedesOperationID = prior.OperationID
	}

	if err := s.ops.Insert(ctx, newOp, tx); err != nil {
		return nil, fmt.Errorf("operations.Submit: insert operation: %w", err)
	}

	// ── Step 8: flip prior op to SUPERSEDED (if supersede) ──────────
	if newOp.SupersedesOperationID != "" {
		if err := s.ops.UpdateState(ctx, newOp.SupersedesOperationID, domainops.StateSuperseded, tx); err != nil {
			return nil, fmt.Errorf("operations.Submit: supersede prior: %w", err)
		}
	}

	// ── Step 9: INSERT jobs row ─────────────────────────────────────
	newJob := &job.Job{
		ID:            jobID,
		Type:          req.JobType,
		Status:        job.StatusQueued,
		Priority:      req.JobPriority,
		Payload:       req.JobPayload,
		MaxRetries:    req.JobMaxRetries,
		RetryCount:    0,
		CorrelationID: req.IdempotencyKey, // mirror: same value as idempotency_key for trace
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.jobs.CreateInTx(ctx, tx, newJob); err != nil {
		return nil, fmt.Errorf("operations.Submit: insert job: %w", err)
	}

	// ── Step 10: INSERT outbox_events row ───────────────────────────
	payloadJSON, err := json.Marshal(map[string]string{
		"operation_id": operationID,
		"job_id":       jobID,
	})
	if err != nil {
		return nil, fmt.Errorf("operations.Submit: marshal outbox payload: %w", err)
	}
	if _, err := s.outbox.Enqueue(
		ctx, tx,
		EventTypeScriptGenerateQueued,
		operationID, // aggregate_id
		aggregateTypeScriptGenerate,
		string(payloadJSON),
		operationID, // event_key (UNIQUE on the outbox table)
	); err != nil {
		return nil, fmt.Errorf("operations.Submit: enbox: %w", err)
	}
	// event_key == operationID is unique per Submit (the
	// submitMu mutex + the fresh ID generation guarantee no
	// collisions within this process), so the underlying
	// ON CONFLICT DO NOTHING is a defensive no-op; Inserted=true
	// is guaranteed. We ignore the EnqueueResult for FASE 2 —
	// a future multi-process Submit would need to surface
	// Inserted=false as a typed no-op (the prior event already
	// represents this operation).

	// ── Step 11: commit ─────────────────────────────────────────────
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("operations.Submit: commit: %w", err)
	}
	committed = true

	// godlike/07 minimum-blast-radius: Service.Submit does NOT
	// wake the worker pool post-COMMIT. The canonical 1-5s worker
	// poll interval is the FASE 2 latency floor; the pre-FASE-2
	// immediate-wake optimisation is dropped in favor of
	// atomicity. The pre-FASE-2 `Service.Enqueue` (which is
	// unchanged) preserves the immediate-wake path for callers
	// that don't need FASE 2 atomicity.

	s.log.Info("operations.Submit: success",
		zap.String("operation_id", newOp.OperationID),
		zap.String("job_id", newOp.JobID),
		zap.String("scope", string(req.Scope)),
		zap.String("idempotency_key", req.IdempotencyKey),
		zap.Bool("force_refresh", req.ForceRefresh),
		zap.Bool("is_supersede", newOp.SupersedesOperationID != ""),
	)

	return &SubmitResult{
		Operation:   newOp,
		Job:         newJob,
		IsSupersede: newOp.SupersedesOperationID != "",
	}, nil
}

// ── internal helpers ──────────────────────────────────────────────

// generateID returns a `<prefix>_<unix_nano>_<8hex>` ID.
func generateID(prefix string) string {
	now := time.Now().UnixNano()
	suf := randomHexSuffix(4)
	return fmt.Sprintf("%s_%d_%s", prefix, now, suf)
}

// randomHexSuffix returns a lowercase hex suffix. On crypto/rand
// failure it falls back to a time-derived byte slice so the ID
// remains non-empty and stable enough for operational use.
func randomHexSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		ns := time.Now().UnixNano()
		for i := range buf {
			buf[i] = byte(ns >> (uint(i) * 8))
		}
	}
	return hex.EncodeToString(buf)
}
