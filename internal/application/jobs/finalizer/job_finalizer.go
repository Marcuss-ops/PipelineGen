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
	db       *sql.DB
	outbox   *outboxevents.Repository
	assetTx  *assetfinalizer.AssetTxFinalizer
	log      *zap.Logger
}

// New creates a Finalizer with the given database, outbox repository,
// and asset finalizer.
func New(db *sql.DB, outbox *outboxevents.Repository, assetTx *assetfinalizer.AssetTxFinalizer, log *zap.Logger) *Finalizer {
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

	// 5. Adapt *sql.Tx to finalization.Transaction for AssetFinalizerTx.
	domainTx := assetfinalizer.WrapTx(tx)

	// 6. Delegate artifact writes to AssetFinalizerTx.
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

	// 7. Write outbox events (from request + AssetFinalizerTx).
	if err := f.writeOutboxEvents(ctx, tx, allEvents); err != nil {
		return nil, err
	}

	// 8. Write result manifest + mark job SUCCEEDED.
	if err := f.markSucceeded(ctx, tx, &req); err != nil {
		return nil, err
	}

	// 9. Commit.
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("finalizer: commit: %w", err)
	}

	now := time.Now().UTC()
	f.log.Info("job finalised with artifacts",
		zap.String("job_id", req.Result.JobID),
		zap.Int("attempt", req.Result.Attempt),
		zap.Int("artifact_count", len(refs)),
		zap.Int("outbox_events", len(allEvents)),
	)

	return &finalization.FinalizationResult{
		JobID:        req.Result.JobID,
		Status:       "SUCCEEDED",
		CompletedAt:  now,
		ArtifactRefs: refs,
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
		if a.Required {
			hasRequired = true
		}
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
		 FROM jobs WHERE id = ?`,
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
			JobID:       req.Result.JobID,
			Status:      "SUCCEEDED",
			CompletedAt: time.Now().UTC(),
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
// fingerprint for artifact-aware idempotency), inserts a job event, and
// updates the job status to SUCCEEDED — all inside the transaction.
func (f *Finalizer) markSucceeded(
	ctx context.Context,
	tx *sql.Tx,
	req *finalization.FinalizationRequest,
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

	return nil
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
