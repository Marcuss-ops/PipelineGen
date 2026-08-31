// Package finalization — canonical interfaces for the transactional
// job-finalization spine (Spina Dorsale, Fase 1, July 2026).
//
// Three ports are defined here, following AGENTS.md Pattern 0:
//
//   - JobFinalizer — the single writer of SUCCEEDED. All capabilities
//     converge on this interface for atomic job completion.
//
//   - ArtifactPreparationService — validates, hashes, and publishes
//     a VerifiedArtifact, producing a PublishedArtifact. Owns the
//     external side-effects (Drive upload, etc.) that cannot be inside
//     the SQLite transaction.
//
//   - AssetFinalizerTx — writes the canonical asset, version, and
//     location records inside the JobFinalizer's transaction. Produces
//     ArtifactRef and OutboxEvent(s) for downstream consumers.
//
//   - Transaction — a narrow write-only surface consumed by
//     AssetFinalizerTx to participate in the JobFinalizer's
//     transaction. Production concrete: *sql.Tx.
//
// Production concretes live in:
//
//	internal/capabilities/jobs/policy/   (JobFinalizer)
//	internal/capabilities/assets/finalizer/ (ArtifactPreparationService, AssetFinalizerTx)
//
// Canonical reference: Piano d'Azione Completo § 4.2–5.2.
package finalization

import "context"

// ── JobFinalizer ─────────────────────────────────────────────────────

// JobFinalizer is the SINGLE writer of the terminal SUCCEEDED state.
// Every capability that completes a job MUST route through
// CompleteWithArtifacts — never set SUCCEEDED directly.
//
// The implementation must:
//
//  1. Validate the lease (exists, not expired, belongs to the calling
//     worker, attempt matches the current job attempt).
//  2. Verify all required artifacts are present and published.
//  3. Verify checksums and sizes are coherent.
//  4. Verify idempotency keys are non-empty and non-duplicate.
//  5. Open a SQLite transaction (BEGIN IMMEDIATE).
//  6. Re-validate lease and attempt inside the transaction (defence
//     against race between validation and commit).
//  7. Check for prior terminal result: if already SUCCEEDED with the
//     same result hash → idempotent success; if different result hash
//     → ErrCompletionConflict; if attempt is stale → ErrStaleAttempt.
//  8. Write canonical asset records via AssetFinalizerTx.
//  9. Write result manifest, job artifacts, outbox events.
//  10. UPDATE jobs SET status = 'SUCCEEDED'.
//  11. COMMIT.
//
// Idempotency contract:
//
//   - Same result hash + same artifacts → idempotent success (no-op).
//   - Different result hash on already-SUCCEEDED job → ErrCompletionConflict.
//   - Stale attempt (request.Attempt < current.Attempt) → ErrStaleAttempt.
type JobFinalizer interface {
	// CompleteWithArtifacts finalises a job atomically with its
	// published artifacts.
	//
	// Returns ErrStaleAttempt if the lease attempt is behind the
	// current job attempt. Returns ErrCompletionConflict if the job
	// is already SUCCEEDED with a different result. Returns
	// ErrRequiredArtifactMissing if a required artifact is not present
	// in the request.
	CompleteWithArtifacts(
		ctx context.Context,
		req FinalizationRequest,
	) (*FinalizationResult, error)
}

// ── ArtifactPreparationService ──────────────────────────────────────

// ── PublisherPort ───────────────────────────────────────────────────

// PublisherPort is the canonical domain port for publishing a verified
// artifact to a remote storage backend (Drive, S3, object storage).
//
// It is the narrow publish-only seam consumed by ArtifactPreparation.
// The concrete implementation lives in
// internal/platform/drive/artifact_publisher_adapter.go and
// wraps delivery.Publisher.
//
// Drive cutover P0.4 (July 2026): extracted from the local Publisher
// interface in internal/capabilities/assets/finalizer/. The port lives
// at the domain boundary (Pattern 0) so the infrastructure adapter
// can implement it without importing the application layer.
type PublisherPort interface {
	// Publish uploads the artifact's content (from LocalPath) to the
	// remote storage backend and returns the canonical AssetLocation.
	//
	// The returned location MUST set Provider, FileID, WebViewLink,
	// DownloadLink, Checksum, FolderID, FolderPath, and Action.
	//
	// Idempotency: the implementation MUST use the artifact's
	// IdempotencyKey to avoid duplicate publications. Same content
	// → same key → same remote file → PublishSkipped.
	Publish(ctx context.Context, artifact VerifiedArtifact) (AssetLocation, error)
}

// ── ArtifactPreparationService ──────────────────────────────────────

// ArtifactPreparationService owns the external side-effects of
// preparing an artifact for finalisation: validation, hashing, and
// publication to a remote location.
//
// It transforms a VerifiedArtifact (local, validated) into a
// PublishedArtifact (remote, with canonical location).
//
// The service is called BEFORE the JobFinalizer's transaction because
// external side-effects (Drive upload, object storage PUT) cannot be
// included in a SQLite transaction.
//
// Idempotency: the implementation MUST use the artifact's
// IdempotencyKey to avoid duplicate publications. Same content →
// same key → same remote file → PublishSkipped.
type ArtifactPreparationService interface {
	// Prepare validates, hashes, and publishes a verified artifact,
	// returning the published artifact with its canonical location.
	Prepare(
		ctx context.Context,
		artifact VerifiedArtifact,
	) (PublishedArtifact, error)
}

// ── ArtifactFolderResolver ─────────────────────────────────────────

// ArtifactFolderResolver resolves the already-resolved Drive folder ID for a
// sidecar artifact's parent video. RenderingGen overlays publish BELOW the
// parent video's Drive folder (via the artifact's DriveSubpath), so the
// broker resolves the video's folder and pins it as the overlay's
// DestinationFolderID before publication.
//
// Nil-safe by design: when a broker is not wired with a resolver, sidecar
// artifacts fall back to the destination path builder (legacy behaviour).
type ArtifactFolderResolver interface {
	// ResolveArtifactFolder returns the Drive folder ID for the parent
	// video identified by parentVideoID (a media_assets.id). An empty
	// return means "not resolved" and the caller keeps the legacy path.
	ResolveArtifactFolder(ctx context.Context, parentVideoID string) (string, error)
}

// ── AssetFinalizerTx ────────────────────────────────────────────────

// AssetFinalizerTx writes the canonical asset, version, and location
// records INSIDE the JobFinalizer's transaction. It must NOT open its
// own transaction — it participates in the caller's transaction via
// the Transaction parameter.
//
// For each PublishedArtifact, it produces:
//
//   - ArtifactRef (lightweight reference for downstream consumers)
//   - []OutboxEvent (indexing requests, workflow steps)
//
// The implementation must NOT:
//
//   - Open a new transaction (uses the provided tx)
//   - Make external network calls (Drive, Qdrant)
//   - Write directly to media_assets outside this interface
type AssetFinalizerTx interface {
	// FinalizeAsset writes the canonical asset, version, and location
	// for a published artifact inside the caller's transaction. It
	// returns a lightweight reference and any outbox events to emit.
	FinalizeAsset(
		ctx context.Context,
		tx Transaction,
		artifact PublishedArtifact,
	) (ArtifactRef, []OutboxEvent, error)
}

// ── Transaction ─────────────────────────────────────────────────────

// Transaction is a write-primary surface that AssetFinalizerTx
// consumes to participate in the JobFinalizer's transaction. The
// production concrete is *sql.Tx (via a thin adapter).
//
// AssetFinalizerTx must NOT be able to Commit or Rollback — only the
// JobFinalizer controls the transaction lifecycle.
type Transaction interface {
	// ExecContext executes a query that does not return rows.
	// Implements database/sql/driver.ExecerContext.
	ExecContext(ctx context.Context, query string, args ...any) (Result, error)

	// QueryRowContext executes a query that returns at most one row.
	// Used by AssetFinalizerTx to compute the next version_number
	// (SELECT MAX(version_number)) inside the same transaction.
	QueryRowContext(ctx context.Context, query string, args ...any) Row
}

// ── Transaction Result ──────────────────────────────────────────────

// Result is the narrow result surface from Transaction.ExecContext.
// Mirrors sql.Result; satisfied structurally by sql.Result.
type Result interface {
	// LastInsertId returns the integer generated by the database in
	// response to a command.
	LastInsertId() (int64, error)

	// RowsAffected returns the number of rows affected by the command.
	RowsAffected() (int64, error)
}

// Row is the narrow result surface from Transaction.QueryRowContext.
// Mirrors *sql.Row; satisfied structurally by *sql.Row.
type Row interface {
	// Scan copies the columns from the matched row into the values
	// pointed at by dest.
	Scan(dest ...any) error
}
