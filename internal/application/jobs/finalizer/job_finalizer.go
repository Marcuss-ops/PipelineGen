// Package finalizer provides the concrete implementation of the
// canonical JobFinalizer interface (Spina Dorsale, Fase 2-3, July 2026).
//
// The Finalizer is the SINGLE writer of the terminal SUCCEEDED state.
// Every capability that completes a job MUST route through
// CompleteWithArtifacts — never set SUCCEEDED directly.
//
// Transactional contract (Piano d'Azione § 4.4):
//
//	BEGIN IMMEDIATE
//	  SELECT job (lease check)
//	  check prior terminal result (idempotent completion)
//	  delegate to AssetFinalizerTx.FinalizeAsset (for each artifact)
//	    → UPSERT media_assets
//	    → INSERT asset_versions
//	    → UPSERT asset_locations
//	    → return ArtifactRef + OutboxEvent descriptors
//	  INSERT outbox_events (for each event from AssetFinalizerTx + request)
//	  INSERT job_events
//	  UPDATE jobs SET status = 'SUCCEEDED'
//	COMMIT
//
// All writes happen in ONE transaction. If any step fails, the
// deferred rollback undoes everything.
//
// Idempotency contract (Piano d'Azione § 4.5):
//
//   - Same result hash + same artifacts → idempotent success (no-op).
//   - Different result hash on already-SUCCEEDED job → ErrCompletionConflict.
//   - Stale attempt (request.Attempt < current.Attempt) → ErrStaleAttempt.
package finalizer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"go.uber.org/zap"

	assetfinalizer "github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Finalizer is the concrete implementation of finalization.JobFinalizer.
//
// It holds a *sql.DB (to open transactions), an *outboxevents.Repository
// (to enqueue outbox events), and an *AssetTxFinalizer (to write canonical
// asset records inside the transaction).
type Finalizer struct {
	db      *sql.DB
	outbox  *outboxevents.Repository
	assetTx finalization.AssetFinalizerTx
	log     *zap.Logger
}

// New creates a Finalizer with the given database, outbox repository,
// and asset-finalizer port (Pattern-0 port abstraction). Production
// callers pass *assetfinalizer.AssetTxFinalizer which satisfies the
// interface.
func New(db *sql.DB, outbox *outboxevents.Repository, assetTx finalization.AssetFinalizerTx, log *zap.Logger) *Finalizer {
	if log == nil {
		log = zap.NewNop()
	}
	return &Finalizer{
		db:      db,
		outbox:  outbox,
		assetTx: assetTx,
		log:     log,
	}
}

// Compile-time assertion: Finalizer implements finalization.JobFinalizer.
var _ finalization.JobFinalizer = (*Finalizer)(nil)

// CompleteWithArtifacts finalises a job atomically with its published
// artifacts. See finalization.JobFinalizer for the full contract.
func (f *Finalizer) CompleteWithArtifacts(
	ctx context.Context,
	req finalization.FinalizationRequest,
) (*finalization.FinalizationResult, error) {
	// 1. Pre-validation (outside transaction — fail-fast).
	if err := f.validateRequest(&req); err != nil {
		return nil, err
	}

	// 2. Open transaction.
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("finalizer: begin tx: %w", err)
	}
	defer tx.Rollback()

	// 3. SELECT job with lease fence.
	jobRow, err := f.selectJobForFinalization(ctx, tx, &req.Lease)
	if err != nil {
		return nil, err
	}

	// 4. Check prior terminal result (idempotent completion).
	if jobRow.status == "SUCCEEDED" {
		return f.handleIdempotentCompletion(ctx, jobRow, &req)
	}

	now := time.Now().UTC()

	// 5. Build the optional-artifact audit report (P1.2). Pure
	// function over the request struct — runs BEFORE the SQL
	// operations so a cross-reference mismatch
	// (ErrOptionalArtifactFinalizedMismatch when a declaration
	// promises Finalized but the ArtifactID is missing from
	// Artifacts) fails the request BEFORE any table is touched.
	// The report is later persisted to the `optional_artifact_report`
	// job_events row inside markSucceeded.
	optionalReport, reportErr := f.buildOptionalArtifactReport(&req, now)
	if reportErr != nil {
		return nil, reportErr
	}

	// 6. Adapt *sql.Tx to finalization.Transaction for AssetFinalizerTx.
	domainTx := assetfinalizer.WrapTx(tx)

	// 7. Delegate artifact writes to AssetFinalizerTx.
	var refs []finalization.ArtifactRef
	allEvents := make([]finalization.OutboxEvent, 0, len(req.Events)+len(req.Artifacts))
	allEvents = append(allEvents, req.Events...)
	for i, a := range req.Artifacts {
		ref, events, err := f.assetTx.FinalizeAsset(ctx, domainTx, a)
		if err != nil {
			return nil, fmt.Errorf("finalizer: artifact[%d] (%s): %w", i, a.ArtifactID, err)
		}
		refs = append(refs, ref)
		allEvents = append(allEvents, events...)
	}

	// 8. Write outbox events (from request + AssetFinalizerTx).
	if err := f.writeOutboxEvents(ctx, tx, allEvents); err != nil {
		return nil, err
	}

	// 9. Write result manifest + mark job SUCCEEDED + persist the
	//    optional_artifact_report audit sidecar inside the same
	//    SQLite transaction.
	if err := f.markSucceeded(ctx, tx, &req, optionalReport); err != nil {
		return nil, err
	}

	// 10. Commit.
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("finalizer: commit: %w", err)
	}

	f.log.Info("job finalised with artifacts",
		zap.String("job_id", req.Result.JobID),
		zap.Int("attempt", req.Result.Attempt),
		zap.Int("artifact_count", len(refs)),
		zap.Int("outbox_events", len(allEvents)),
		zap.Int("optional_artifact_count", len(optionalReport)),
	)

	return &finalization.FinalizationResult{
		JobID:          req.Result.JobID,
		Status:         "SUCCEEDED",
		CompletedAt:    now,
		ArtifactRefs:   refs,
		OptionalArtifactReport: optionalReport,
	}, nil
}

// ── Pre-validation ──────────────────────────────────────────────────

// validateRequest performs fail-fast validation before opening a
// transaction. This catches programming errors early and avoids
// wasted transaction opens.
func (f *Finalizer) validateRequest(req *finalization.FinalizationRequest) error {
	// Lease validation.
	if req.Lease.JobID == "" {
		return finalization.NewFinalizationError(
			"INVALID_LEASE", "lease has empty JobID",
			"", 0, finalization.ErrLeaseExpired,
		)
	}
	if !req.Lease.Valid() {
		return finalization.NewFinalizationError(
			"LEASE_EXPIRED", "lease has expired",
			req.Lease.JobID, req.Lease.Attempt, finalization.ErrLeaseExpired,
		)
	}
	if req.Lease.LeaseID == "" {
		return finalization.NewFinalizationError(
			"INVALID_LEASE", "lease has empty LeaseID",
			req.Lease.JobID, req.Lease.Attempt, finalization.ErrLeaseExpired,
		)
	}
	if req.Lease.WorkerID == "" {
		return finalization.NewFinalizationError(
			"INVALID_LEASE", "lease has empty WorkerID",
			req.Lease.JobID, req.Lease.Attempt, finalization.ErrLeaseExpired,
		)
	}

	// Result manifest validation.
	if req.Result.JobID == "" {
		return finalization.NewFinalizationError(
			"INVALID_RESULT", "result manifest has empty JobID",
			"", 0, nil,
		)
	}
	if req.Result.JobID != req.Lease.JobID {
		return finalization.NewFinalizationError(
			"MISMATCHED_JOB_ID", "result JobID does not match lease JobID",
			req.Result.JobID, req.Lease.Attempt, nil,
		)
	}

	// Artifact validation.
	seen := make(map[string]bool)
	hasRequired := false
	for i, a := range req.Artifacts {
		if a.ArtifactID == "" {
			return finalization.NewFinalizationError(
				"INVALID_ARTIFACT",
				fmt.Sprintf("artifact[%d] has empty ArtifactID", i),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrRequiredArtifactMissing,
			)
		}
		// P1.2 (July 2026): typed Requirement enum. The zero value
		// (ArtifactRequirementInvalid) is explicitly rejected so a
		// forgotten-to-set field fail-closes loudly — mirrors how
		// PublishAction's empty-string zero value is handled.
		if !a.Requirement.Valid() {
			return finalization.NewFinalizationError(
				"INVALID_REQUIREMENT",
				fmt.Sprintf("artifact[%d] (%s) has Requirement=%s — must be Required or Optional", i, a.ArtifactID, a.Requirement),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrArtifactRequirementInvalid,
			)
		}
		if a.IdempotencyKey == "" {
			return finalization.NewFinalizationError(
				"INVALID_IDEMPOTENCY_KEY",
				fmt.Sprintf("artifact[%d] (%s) has empty IdempotencyKey", i, a.ArtifactID),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrInvalidIdempotencyKey,
			)
		}
		if seen[a.ArtifactID] {
			return finalization.NewFinalizationError(
				"DUPLICATE_ARTIFACT",
				fmt.Sprintf("artifact[%d] (%s) is a duplicate", i, a.ArtifactID),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrDuplicateArtifact,
			)
		}
		seen[a.ArtifactID] = true
		// P1.2 (July 2026): typed Requirement enum replaces the
		// legacy Required bool. hasRequired still tracks the
		// required-set membership for the "at least one required"
		// invariant below.
		if a.Requirement == finalization.ArtifactRequirementRequired {
			hasRequired = true
		}
	}

	// P1.2 (July 2026): OptionalDeclarations is the OPTIONAL sidecar
	// only. We surface three failure classes with distinct typed
	// sentinels so log scrapers and dashboards can attribute the
	// issue without parsing the error message:
	//
	//   (a) Requirement=Invalid (zero value) — fail-fast: caller
	//       forgot to set the field. Mirrors how an artifact literal
	//       with Invalid Requirement is rejected above.
	//   (b) Requirement=Required — fail-fast: caller put a required
	//       artifact on the optional sidecar. Required artifacts
	//       belong on `Artifacts`, not on the declaration sidecar.
	//   (c) Duplicate ArtifactID within declarations — fail-fast:
	//       caller emitted two records for one optional artifact.
	//       Without this check, the cross-reference would produce
	//       two audit rows and surfaces misleading outcome counts.
	seenDecl := make(map[string]bool, len(req.OptionalDeclarations))
	for i, d := range req.OptionalDeclarations {
		if d.Requirement == finalization.ArtifactRequirementInvalid {
			return finalization.NewFinalizationError(
				"DECLARATION_HAS_INVALID_REQUIREMENT",
				fmt.Sprintf("OptionalDeclarations[%d] (%s) has Requirement=INVALID (zero value) — set explicitly to Required or Optional", i, d.ArtifactID),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrArtifactRequirementInvalid,
			)
		}
		if d.Requirement != finalization.ArtifactRequirementOptional {
			return finalization.NewFinalizationError(
				"DECLARATION_HAS_REQUIRED_REQUIREMENT",
				fmt.Sprintf("OptionalDeclarations[%d] (%s) has Requirement=%s — required artifacts belong on Artifacts, declarations are the optional sidecar only", i, d.ArtifactID, d.Requirement),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrOptionalDeclarationHasRequiredRequirement,
			)
		}
		if seenDecl[d.ArtifactID] {
			return finalization.NewFinalizationError(
				"DUPLICATE_OPTIONAL_DECLARATION",
				fmt.Sprintf("OptionalDeclarations[%d] (%s) is a duplicate", i, d.ArtifactID),
				req.Result.JobID, req.Lease.Attempt,
				finalization.ErrDuplicateArtifact,
			)
		}
		seenDecl[d.ArtifactID] = true
	}

	// At least one artifact must be required (otherwise the job has
	// nothing substantive to record).
	if len(req.Artifacts) > 0 && !hasRequired {
		return finalization.NewFinalizationError(
			"NO_REQUIRED_ARTIFACTS",
			"all artifacts are optional — at least one required artifact expected",
			req.Result.JobID, req.Lease.Attempt,
			finalization.ErrRequiredArtifactMissing,
		)
	}

	return nil
}

// ── Job row (lease fence) ───────────────────────────────────────────

// jobRow holds the result of the lease-fenced SELECT.
type jobRow struct {
	status      string
	workerID    string
	leaseID     string
	revision    int
	retryCount  int
	leaseExpiry sql.NullString
	resultJSON  string
}

// selectJobForFinalization reads the job row inside the transaction
// and validates the lease fence.
func (f *Finalizer) selectJobForFinalization(
	ctx context.Context,
	tx *sql.Tx,
	lease *finalization.Lease,
) (*jobRow, error) {
	var row jobRow
	err := tx.QueryRowContext(ctx,
		`SELECT status, worker_id, lease_id, revision, retry_count, lease_expiry, COALESCE(result_json, '')
		 FROM jobs
		 WHERE id = ?
		   AND (lease_expiry IS NULL OR lease_expiry > CURRENT_TIMESTAMP)`,
		lease.JobID,
	).Scan(&row.status, &row.workerID, &row.leaseID, &row.revision, &row.retryCount, &row.leaseExpiry, &row.resultJSON)
	if err == sql.ErrNoRows {
		return nil, finalization.NewFinalizationError(
			"JOB_NOT_FOUND", "job not found",
			lease.JobID, lease.Attempt, nil,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("finalizer: select job: %w", err)
	}

	// Validate lease ownership inside the transaction (defence against
	// race between pre-validation and commit).
	if row.status != "RUNNING" && row.status != "FINALIZING" && row.status != "SUCCEEDED" {
		return nil, finalization.NewFinalizationError(
			"INVALID_STATUS",
			fmt.Sprintf("job status %q is not completable", row.status),
			lease.JobID, lease.Attempt, nil,
		)
	}

	// Already-SUCCEEDED jobs skip lease ownership checks because
	// markSucceeded clears worker_id + lease_id on completion.
	// The caller routes these to handleIdempotentCompletion which
	// compares completion fingerprints to decide idempotent success
	// vs ErrCompletionConflict.
	if row.status == "SUCCEEDED" {
		return &row, nil
	}

	// SUCCEEDED rows are already terminal — worker_id and lease_id
	// are cleared by markSucceeded at completion time, so a
	// subsequent idempotent call would otherwise fail the
	// ownership check. The caller identity is no longer relevant
	// once the job is committed; handleIdempotentCompletion compares
	// fingerprints instead. Step 3 (d) of the 12-step plan (July
	// 2026) formalises this path.
	if row.status == "SUCCEEDED" {
		return &row, nil
	}
	if row.workerID != lease.WorkerID {
		return nil, finalization.NewFinalizationError(
			"LEASE_OWNER_MISMATCH",
			fmt.Sprintf("lease owner mismatch: worker %q != expected %q", row.workerID, lease.WorkerID),
			lease.JobID, lease.Attempt, finalization.ErrLeaseOwnerMismatch,
		)
	}
	if row.leaseID != lease.LeaseID {
		return nil, finalization.NewFinalizationError(
			"LEASE_ID_MISMATCH",
			fmt.Sprintf("lease ID mismatch: %q != expected %q", row.leaseID, lease.LeaseID),
			lease.JobID, lease.Attempt, finalization.ErrLeaseExpired,
		)
	}

	// Re-validate lease expiry against the DB row (not the request value).
	// The pre-validation check on req.Lease.Valid() uses the request's
	// ExpiresAt; the DB row carries the canonical value.
	if row.leaseExpiry.Valid {
		expiryTime, parseErr := time.Parse(time.RFC3339, row.leaseExpiry.String)
		if parseErr == nil && time.Now().UTC().After(expiryTime) {
			return nil, finalization.NewFinalizationError(
				"LEASE_EXPIRED_DB",
				fmt.Sprintf("lease expired at %s (checked from DB row)", row.leaseExpiry.String),
				lease.JobID, lease.Attempt, finalization.ErrLeaseExpired,
			)
		}
	}

	// The request attempt must equal the job's retry_count + 1
	// (the attempt counter increments on each retry).
	expectedAttempt := row.retryCount + 1
	if lease.Attempt != expectedAttempt {
		return nil, finalization.NewFinalizationError(
			"STALE_ATTEMPT",
			fmt.Sprintf("request attempt %d != expected %d (retry_count=%d)", lease.Attempt, expectedAttempt, row.retryCount),
			lease.JobID, lease.Attempt, finalization.ErrStaleAttempt,
		)
	}

	return &row, nil
}

// ── Idempotent completion ───────────────────────────────────────────

// handleIdempotentCompletion checks whether an already-SUCCEEDED job
// was completed with the same result and artifacts. If so, it returns
// idempotent success. If the completion fingerprint differs, it returns
// ErrCompletionConflict.
//
// The completion fingerprint is a SHA-256 hash of the result manifest
// data + all artifact IDs (sorted) + SHA256s + source versions + remote
// asset IDs. Two completions with the same result but different artifacts
// produce different fingerprints and correctly fail as conflict.
func (f *Finalizer) handleIdempotentCompletion(
	ctx context.Context,
	row *jobRow,
	req *finalization.FinalizationRequest,
) (*finalization.FinalizationResult, error) {
	// P1.2: rebuild the optional-artifact audit report so callers
	// retrying an already-SUCCEEDED job (idempotent replay path)
	// see the same per-optional outcome on the in-memory return as
	// the first successful commit. Best-effort attach — a build
	// error here would mean the FIRST commit also failed, and the
	// stored fingerprint comparison below is the louder signal.
	//
	// The cross-reference is deterministic over the request struct
	// (P1.2 typed-data invariant: same OptionalDeclarations +
	// same Artifacts -> same OptionalArtifactReport), so the
	// recompute produces a byte-equivalent report to what was
	// persisted on the first commit. We attach it to the returned
	// FinalizationResult so dashboards reading `finResult` on a
	// retry see the actual outcome rather than a silent empty list.
	optionalReport, _ := f.buildOptionalArtifactReport(req, time.Now().UTC())

	requestFingerprint := computeCompletionFingerprint(req.Result.Data, req.Artifacts)

	// If the stored result is empty, we can't compare — treat as conflict.
	if row.resultJSON == "" || row.resultJSON == "{}" || row.resultJSON == "null" {
		f.log.Warn("job already SUCCEEDED with empty result, cannot verify idempotency",
			zap.String("job_id", req.Result.JobID),
		)
		return nil, finalization.NewFinalizationError(
			"COMPLETION_CONFLICT",
			"job already SUCCEEDED with empty/nil result — cannot verify idempotency",
			req.Result.JobID, req.Lease.Attempt, finalization.ErrCompletionConflict,
		)
	}

	storedFingerprint := extractCompletionFingerprint(row.resultJSON)
	if storedFingerprint == "" {
		// Legacy: no fingerprint stored — fall back to result-data-only hash.
		storedHash := hashJSONString(row.resultJSON)
		requestHash := hashJSONString(string(req.Result.Data))
		if storedHash == requestHash {
			f.log.Info("job already SUCCEEDED with same result hash — idempotent success (legacy fallback)",
				zap.String("job_id", req.Result.JobID),
				zap.String("hash", requestHash),
			)
			return &finalization.FinalizationResult{
				JobID:       req.Result.JobID,
				Status:      "SUCCEEDED",
				CompletedAt: time.Now().UTC(),
			}, nil
		}
		return nil, finalization.NewFinalizationError(
			"COMPLETION_CONFLICT",
			fmt.Sprintf("job already SUCCEEDED with different result (stored_hash=%s request_hash=%s)", storedHash, requestHash),
			req.Result.JobID, req.Lease.Attempt, finalization.ErrCompletionConflict,
		)
	}

	if storedFingerprint == requestFingerprint {
		f.log.Info("job already SUCCEEDED with same completion fingerprint — idempotent success",
			zap.String("job_id", req.Result.JobID),
			zap.String("fingerprint", requestFingerprint),
		)
		return &finalization.FinalizationResult{
			JobID:          req.Result.JobID,
			Status:         "SUCCEEDED",
			CompletedAt:    time.Now().UTC(),
			OptionalArtifactReport: optionalReport,
		}, nil
	}

	return nil, finalization.NewFinalizationError(
		"COMPLETION_CONFLICT",
		fmt.Sprintf("job already SUCCEEDED with different completion fingerprint (stored=%s request=%s)",
			storedFingerprint, requestFingerprint),
		req.Result.JobID, req.Lease.Attempt, finalization.ErrCompletionConflict,
	)
}



// ── Outbox events ───────────────────────────────────────────────────

// writeOutboxEvents enqueues all outbox events inside the transaction
// using the outboxevents.Repository.
func (f *Finalizer) writeOutboxEvents(
	ctx context.Context,
	tx *sql.Tx,
	events []finalization.OutboxEvent,
) error {
	for i, evt := range events {
		payloadJSON := string(evt.Payload)
		if payloadJSON == "" || payloadJSON == "null" {
			payloadJSON = "{}"
		}
		_, err := f.outbox.Enqueue(ctx, tx,
			evt.EventType,
			evt.AggregateID,
			"", // aggregate_type — not required for asset events
			payloadJSON,
			evt.EventKey,
		)
		if err != nil {
			return fmt.Errorf("finalizer: outbox event[%d] (%s): %w", i, evt.EventType, err)
		}
	}
	return nil
}

// ── Mark succeeded ──────────────────────────────────────────────────

// markSucceeded writes the result manifest (wrapped with completion
// fingerprint for artifact-aware idempotency), inserts a job event for
// `job_completed`, persists the optional-artifact audit sidecar (P1.2)
// as a separate `optional_artifact_report` job_events row, and
// updates the job status to SUCCEEDED — all inside the transaction.
//
// godlike/07 typed-error contract: the optional-artifact sidecar row
// lands atomically with the job_completed flip so a partial commit
// cannot corrupt the operator's view of which optional artifacts
// shipped (P1.2 invariant: success == sidecar persisted).
func (f *Finalizer) markSucceeded(
	ctx context.Context,
	tx *sql.Tx,
	req *finalization.FinalizationRequest,
	optionalReport []finalization.OptionalArtifactRecord,
) error {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	// Compute completion fingerprint: result + sorted artifact hashes.
	fingerprint := computeCompletionFingerprint(req.Result.Data, req.Artifacts)

	// Wrap result JSON to include the fingerprint so idempotent
	// re-completion can compare full artifact state, not just result data.
	type resultWithFingerprint struct {
		Data                  json.RawMessage `json:"data"`
		CompletionFingerprint string          `json:"completion_fingerprint"`
	}
	wrapped, err := json.Marshal(resultWithFingerprint{
		Data:                  req.Result.Data,
		CompletionFingerprint: fingerprint,
	})
	if err != nil {
		return fmt.Errorf("finalizer: marshal wrapped result: %w", err)
	}
	resultJSON := string(wrapped)

	// Atomic UPDATE with lease fence (same pattern as the existing
	// SQLiteStore.Complete in repository_lifecycle.go). Accepts both
	// RUNNING and FINALIZING to cover the FASE 2b state transition.
	res, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status = 'SUCCEEDED', completed_at = ?, result_json = ?,
		 progress = 100, worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING')
		 AND worker_id = ? AND lease_id = ?`,
		nowStr, resultJSON, nowStr,
		req.Result.JobID, req.Lease.WorkerID, req.Lease.LeaseID,
	)
	if err != nil {
		return fmt.Errorf("finalizer: update jobs: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return finalization.NewFinalizationError(
			"TRANSITION_CONFLICT",
			"job row was modified by another transaction after lease validation",
			req.Result.JobID, req.Lease.Attempt, nil,
		)
	}

	// Insert job event — propagate the error (previously silently ignored).
	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), randomHex(6))
	_, err = tx.ExecContext(ctx,
		`INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, req.Result.JobID, "job_completed",
		"job completed with artifacts via JobFinalizer", "{}", nowStr,
	)
	if err != nil {
		return fmt.Errorf("finalizer: insert job event: %w", err)
	}

	// P1.2 (July 2026): Persist the optional-artifact audit report
	// as a distinct job_events row inside the same SQLite transaction.
	// Skip when len(optionalReport)==0 to avoid bloating job_events
	// with empty sidecar rows on jobs that produced no optional
	// artifacts. The Err field on each record is json:"-" so we
	// serialise the typed error's Error() into the typed-data
	// ErrorMessage carrier for observability (the audit row reads
	// cleanly through standard JSON marshaling).
	if len(optionalReport) > 0 {
		payload, marshalErr := json.Marshal(struct {
			SchemaVersion string                                `json:"schema_version"`
			Records       []finalization.OptionalArtifactRecord `json:"records"`
		}{
			SchemaVersion: "v1",
			Records:       optionalReport,
		})
		if marshalErr != nil {
			return fmt.Errorf("finalizer: marshal optional_artifact_report: %w", marshalErr)
		}
		reportEvtID := fmt.Sprintf("evt_%d_%s_opt", now.UnixNano(), randomHex(6))
		_, err = tx.ExecContext(ctx,
			`INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			reportEvtID, req.Result.JobID, "optional_artifact_report",
			fmt.Sprintf("optional artifact audit report (%d records)", len(optionalReport)),
			string(payload), nowStr,
		)
		if err != nil {
			return fmt.Errorf("finalizer: insert optional_artifact_report job event: %w", err)
		}
	}

	return nil
}

// ── Optional artifact audit report (P1.2) ──────────────────────────

// buildOptionalArtifactReport cross-references OptionalDeclarations
// against the request's Artifacts list to produce the per-optional-
// artifact audit sidecar that's persisted in the `optional_artifact_report`
// job_events row alongside the existing `job_completed` event.
//
// Phase 1 — explicit declarations are AUTHORITATIVE.
//
//   - OptionalArtifactStatusFinalized: the artifact MUST appear in
//     Artifacts (matched by ArtifactID). When missing, returns
//     ErrOptionalArtifactFinalizedMismatch — the worker promised
//     the artifact but it's absent from the publish-side, which is
//     almost certainly a programmer error (the worker dropped the
//     artifact on the way to BuildFinalizationRequest, or set the
//     wrong ArtifactID). Loud-fail is preferred over emitting a
//     misleading Finalized record.
//
//     When present, the canonical Filename and IdempotencyKey are
//     copied from the matching PublishedArtifact (NOT the
//     declaration's hint) — the PublishedArtifact is the
//     authoritative source per godlike/06 SSOT (one canonical
//     owner per fact).
//
//   - OptionalArtifactStatusFailed: typed-data envelope populated
//     verbatim from the declaration. Err is preserved in-memory
//     for runtime errors.Is / errors.As traversal; ErrorMessage
//     carries the string into job_events data_json via json.Marshal
//     (Err has json:"-" so a separate persistent carrier is required).
//
//   - OptionalArtifactStatusMissing: silent absent — no Err,
//     no ErrorMessage. Validates that the worker was loud-and-clear
//     about NOT producing the artifact.
//
// validateRequest already rejects OptionalDeclarations entries
// with Requirement != Optional (ErrOptionalDeclarationHasRequiredRequirement)
// and artifact.Requirement == Invalid (ErrArtifactRequirementInvalid),
// so this method assumes canonical declarations on input.
//
// Phase 2 — inference fallback iterates Artifacts filtered to
// Requirement==Optional and surfaces Finalized records for any not
// already covered by a Phase 1 declaration. Note that the fallback
// CANNOT surface Missing / Failed artifacts (those are only visible
// when the worker emits explicit declarations) — this is by design:
// silent FAIL-flips are a recurring source of hidden degradation;
// explicit declarations are the operator's signal that they WANT
// visibility onto a particular optional slot. The fallback exists
// for backwards-compat with workers that haven't yet migrated to
// the explicit-declaration path.
//
// godlike/06 SSOT: this method is the single canonical owner of
// "what does the optional-artifact audit report look like for
// job X?" — callers MUST NOT compute their own cross-reference
// outside this method.
func (f *Finalizer) buildOptionalArtifactReport(
	req *finalization.FinalizationRequest,
	now time.Time,
) ([]finalization.OptionalArtifactRecord, error) {
	pubByID := make(map[string]finalization.PublishedArtifact, len(req.Artifacts))
	for _, a := range req.Artifacts {
		pubByID[a.ArtifactID] = a
	}

	report := make([]finalization.OptionalArtifactRecord, 0, len(req.OptionalDeclarations)+len(req.Artifacts))
	seen := make(map[string]bool, len(req.OptionalDeclarations))

	// Phase 1 — process explicit declarations (authoritative).
	for _, d := range req.OptionalDeclarations {
		rec := finalization.OptionalArtifactRecord{
			ArtifactID:     d.ArtifactID,
			Kind:           d.Kind,
			Requirement:    finalization.ArtifactRequirementOptional,
			Status:         d.Status,
			Filename:       d.Filename,
			IdempotencyKey: d.IdempotencyKey,
			RecordedAt:     now,
		}
		switch d.Status {
		case finalization.OptionalArtifactStatusFinalized:
			pa, ok := pubByID[d.ArtifactID]
			if !ok {
				return nil, finalization.NewFinalizationError(
					"OPTIONAL_FINALIZED_MISMATCH",
					fmt.Sprintf("OptionalDeclarations[%s] declared Finalized but is missing from Artifacts", d.ArtifactID),
					req.Result.JobID, req.Lease.Attempt,
					finalization.ErrOptionalArtifactFinalizedMismatch,
				)
			}
			// Overwrite Phase 1 guesses with canonical Truth from
			// the cross-match — the declaration may carry a hint
			// but the PublishedArtifact is the authoritative source.
			rec.Filename = pa.Filename
			rec.IdempotencyKey = pa.IdempotencyKey
			rec.Err = nil
		case finalization.OptionalArtifactStatusFailed:
			// Surface the typed-data envelope. Err is preserved
			// in-memory for errors.Is/As; ErrorMessage is the
			// JSON-persistent string carrier.
			rec.Err = d.Err
			if d.Err != nil {
				rec.ErrorMessage = d.Err.Error()
			}
		case finalization.OptionalArtifactStatusMissing:
			// Silent absent — no Err, no ErrorMessage.
		}
		report = append(report, rec)
		seen[d.ArtifactID] = true
	}

	// Phase 2 — inference fallback: Artifacts filtered by
	// Requirement==Optional, dedup against Phase 1 entries.
	for _, a := range req.Artifacts {
		if seen[a.ArtifactID] {
			continue
		}
		if a.Requirement != finalization.ArtifactRequirementOptional {
			continue
		}
		report = append(report, finalization.OptionalArtifactRecord{
			ArtifactID:     a.ArtifactID,
			Kind:           a.Kind,
			Requirement:    finalization.ArtifactRequirementOptional,
			Status:         finalization.OptionalArtifactStatusFinalized,
			Filename:       a.Filename,
			IdempotencyKey: a.IdempotencyKey,
			RecordedAt:     now,
		})
	}

	return report, nil
}

// ── Helpers ─────────────────────────────────────────────────────────

// hashResult computes a SHA-256 hash of the result data for
// idempotent completion comparison.
func hashResult(data json.RawMessage) string {
	return hashJSONString(string(data))
}

func hashJSONString(s string) string {
	if s == "" || s == "null" {
		s = "{}"
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ── Completion fingerprint (§ 4.5 idempotency) ─────────────────────

// artifactFingerprintEntry is a deterministic per-artifact summary used
// in the completion fingerprint.
type artifactFingerprintEntry struct {
	ArtifactID    string `json:"artifact_id"`
	SHA256        string `json:"sha256"`
	SourceVersion int64  `json:"source_version"`
	FileID        string `json:"file_id"`
}

// computeCompletionFingerprint computes a SHA-256 hash of the result
// manifest data combined with all artifact identifiers (sorted by
// ArtifactID for determinism). Artifact IDs, SHA256 content hashes,
// source versions, and remote asset IDs (FileID) are all included so
// that two completions with the same result JSON but different artifacts
// produce different fingerprints.
func computeCompletionFingerprint(resultData json.RawMessage, artifacts []finalization.PublishedArtifact) string {
	sorted := make([]artifactFingerprintEntry, len(artifacts))
	for i, a := range artifacts {
		sorted[i] = artifactFingerprintEntry{
			ArtifactID:    a.ArtifactID,
			SHA256:        a.SHA256,
			SourceVersion: a.SourceVersion,
			FileID:        a.Location.FileID,
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ArtifactID < sorted[j].ArtifactID
	})

	payload, _ := json.Marshal(struct {
		Result    json.RawMessage           `json:"result"`
		Artifacts []artifactFingerprintEntry `json:"artifacts"`
	}{
		Result:    resultData,
		Artifacts: sorted,
	})

	return hashJSONString(string(payload))
}

// extractCompletionFingerprint attempts to extract the
// completion_fingerprint from a stored result JSON. Returns "" if the
// stored result predates the fingerprint wrapper (legacy format).
func extractCompletionFingerprint(storedJSON string) string {
	var wrapped struct {
		Data                  json.RawMessage `json:"data"`
		CompletionFingerprint string          `json:"completion_fingerprint"`
	}
	if err := json.Unmarshal([]byte(storedJSON), &wrapped); err != nil {
		return ""
	}
	return wrapped.CompletionFingerprint
}

// randomHex returns a random hex string of n bytes (2n characters).
// The output is derived from SHA-256 truncated to n bytes. n must be ≤ 32.
func randomHex(n int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("job_finalizer_%d_%d", time.Now().UnixNano(), n)))
	return hex.EncodeToString(h[:n])
}
