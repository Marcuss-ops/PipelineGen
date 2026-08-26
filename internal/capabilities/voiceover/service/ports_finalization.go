package voiceover

import (
	"context"
	"database/sql"
	"errors"
)

// ────────────────────────────────────────────────────────────────────────
// Finalization territory — DB transaction + outbox + verification.
// ────────────────────────────────────────────────────────────────────────
//
// The package-level `database/sql` import exists ONLY for this territory.
// Synthesis-territory ports carry ZERO sql/drive/qdrant imports.

// TxOutboxEnqueuer is the canonical narrow port for transactional
// outbox enqueue from inside the voiceover-swap transaction.
//
// Calling sites MUST pass a caller-owned *sql.Tx so the producer
// commit collapses both the voiceovers INSERT and the indexing event
// INSERT into a single atomic visibility boundary. A nil implementation
// is allowed at construction time (ProcessAsset guards nil at the
// call site so the optional behaviour degrades to "skip indexing" —
// same pattern as the previous ClipIndexFunc callback).
type TxOutboxEnqueuer interface {
	// EnqueueIndexEvent emits the canonical asset.index.requested
	// envelope (schema_version="asset.index.requested.v1") inside
	// the caller-owned transaction.
	EnqueueIndexEvent(ctx context.Context, tx *sql.Tx, assetID, source, contentHash string) error

	// EnqueueCleanupEvent emits the canonical
	// voiceover.cleanup.requested envelope inside the caller-owned
	// transaction. P0.7 Wave 21 Step 10/12 (June 2026) replaces
	// the pre-fix fire-and-forget cleanupOrphanVoiceover goroutine
	// with this durable outbox event.
	EnqueueCleanupEvent(ctx context.Context, tx *sql.Tx, voiceoverID, oldDriveFileID, newDriveFileID string, oldLocalPaths []string) error
}

// Azione #9 follow-up (July 2026): DriveUploaderPort interface removed;
// voiceoverDriveAdapter struct also removed from
// internal/app/adapters_voiceover_publisher.go. Post-commit cleanup now
// flows directly through drive.Admin → jobsoutbox.VoiceoverCleanupDriver
// (structural conformance — both declare DeleteFile with identical
// signature; no wrapper needed).

// VoiceoverFinalizer — unified finalization (P0.4 Fase 3a, July 2026).
//
// VoiceoverFinalizer replaces the two divergent finalization paths
// (child pipeline Stage 4 + legacy batch finalizeStage) with a SINGLE
// 6-step atomic commit sequence inside a caller-owned transaction.
// The caller opens the tx, calls Finalize, then commits.
//
// Steps executed by the concrete finalizer:
//  1. Dedupe gate (CountByDriveFileIDTx + DecideDedupe)
//  2. DELETE old row (DeleteByIDTx)
//  3. INSERT new row (InsertTx)
//  4. media_assets projection (UpsertVoiceoverProjectionTx)
//  5. asset.index.requested outbox (EnqueueIndexEvent)
//  6. voiceover.cleanup.requested outbox (EnqueueCleanupEvent)
type VoiceoverFinalizer interface {
	Finalize(ctx context.Context, tx *sql.Tx, cmd *FinalizeCommand) (*FinalizeResult, error)
}

// VoiceoverPostCommitVerifier is the optional narrow port for post-commit
// SQL verification (P0.4 Fase 4a, July 2026). After the tx commits,
// finalizeStage calls Verify(ctx, voiceoverID) to confirm both the
// voiceovers row AND the media_assets projection exist.
//
// Nil-safe: when unwired, finalizeStage skips the verification entirely.
type VoiceoverPostCommitVerifier interface {
	// Verify confirms that the voiceovers row (id) and the
	// media_assets projection (id, source='voiceover') both exist.
	Verify(ctx context.Context, voiceoverID string) error
}

// ErrReconciliationRequired is the typed severity sentinel a
// VoiceoverPostCommitVerifier.Verify implementer wraps around its
// severe-divergence return values (audit P0.5, July 2026).
//
// Severity contract:
//   - err == nil                                          → StateCompleted
//   - errors.Is(err, ErrReconciliationRequired) == true  → StateReconciliationRequired
//   - any other non-nil err                              → StateCompletedUnverified
var ErrReconciliationRequired = errors.New("voiceover post-commit verification: reconciliation required (canonical row missing after commit)")

// VoiceoverItemExecutor is the typed contract for the canonical per-item
// voiceover pipeline (BLOC5.4 cutover, June 2026).
//
// Implements: AGENTS.md Pattern 0 (port abstraction layer).
type VoiceoverItemExecutor interface {
	Execute(ctx context.Context, item *GenerateVoiceoverItemCommand) (*VoiceoverItemResult, error)
}

// Azione #6/#10 (July 2026): TransactionalOutbox type alias removed;
// FilenameBuilder interface removed (zero consumers after Azione #6).
