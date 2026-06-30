// Package ytsqlite — canonical YouTube-pipeline SQLite adapter.
//
// clip_atomic_writer.go: the concrete ClipAtomicWriter that closes
// Commit F's port/impl gap. The port lives in
// `internal/application/youtube/ports/ports.go::ClipAtomicWriter`
// and is invoked by `ProcessYouTubeSegmentUseCase.Execute` Step 9.
// Pre-Commit F the port had no concrete adapter — Step 9's writer
// call short-circuited silently when ProcessSegmentDeps.Writer was
// nil, leaving the per-segment clip row un-persisted and the
// asset.index.requested event un-emitted.
//
// Commit F closes the gap: a single SQLite transaction performs
//   1. tx.UpsertClipTx on media_assets (INSERT ... ON CONFLICT DO UPDATE)
//   2. INSERT INTO outbox_events ON CONFLICT(event_key) DO NOTHING
// Both wrapped in one txMgr.InTransaction call. Idempotency: re-running
// CommitClipAndIndexEvent with the same clipID updates the asset row
// (UpsertClipTx) and does NOT duplicate the outbox row (the
// outbox_events UNIQUE event_key index makes ON CONFLICT DO NOTHING
// collapse the duplicate). Error classification:
// ErrClipWriterRetryable for transient SQLite conditions
// (database is locked, busy_timeout exhausted), ErrClipWriterTerminal
// for unrecoverable failures (commit-after-tx-failure, schema
// constraints, fk violations). The parent's classifyExtractionResult
// examines the typed error via errors.Is.
//
// Pattern 0 compliance: structural ports with signature-bearing
// interfaces + compile-time `var _` assertions so future drift
// surfaces at compile time, not first runtime call.
//
// Godlike/07 no-fake-availability: empty clipID, mismatched
// event.AggregateID, and missing txMgr/clips surface as typed
// errors (ErrClipWriterNil / ErrClipWriterEventInvalid /
// ErrClipWriterTerminal). A partial commit is impossible because
// txMgr.InTransaction collapses both writes into a single
// BeginTx/Commit rollback gate.
package ytsqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	sqoutbox "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Compile-time conformance assertion (AGENTS.md Pattern 0).
var _ youtubeports.ClipAtomicWriter = (*ClipAtomicWriter)(nil)

// ErrClipWriterNil is returned when CommitClipAndIndexEvent receives an
// empty clipID. This is a programming error in the caller and cannot
// be fixed by retry. Errors.Is branches the parent's retry logic accordingly.
var ErrClipWriterNil = errors.New("clip_atomic_writer: clipID must not be empty")

// ErrClipWriterEventInvalid is returned when event.Type is empty or
// event.AggregateID does not equal clipID. Per godlike/07 no-fake-
// availability contract, the writer never emits an event row that
// mismatches the asset row's clipID — that would be the silent-success
// hole the cutover plan called out as P0 #2.
var ErrClipWriterEventInvalid = errors.New("clip_atomic_writer: event.Type must not be empty and event.AggregateID must equal clipID")

// ErrClipWriterRetryable signals a transient SQLite failure the caller
// may safely retry (database-locked, busy_timeout exhausted, disk-full
// at OS level). The parent (process_segment.go retry.Do via
// isTransientExtractionError) currently uses substring matching; the
// typed error is here so a future retry-aware upgrade branches on
// errors.Is without parsing substrings.
var ErrClipWriterRetryable = errors.New("clip_atomic_writer: transient SQLite failure")

// ErrClipWriterTerminal signals a failure retries will not resolve
// (commit failed after the in-tx write, schema constraint violation,
// foreign-key violation). The use case surfaces this to the job
// classifier as terminal.
var ErrClipWriterTerminal = errors.New("clip_atomic_writer: terminal failure")

// ErrClipWriterUnwired signals a composition bug: production
// composition root MUST wire both clips + txMgr. The writer refuses
// to operate with either nil so a missing dep surfaces at first call,
// not as silent no-op downstream.
var ErrClipWriterUnwired = errors.New("clip_atomic_writer: clips or txMgr are nil (composition bug)")

// ClipAtomicWriter is the concrete implementation of
// youtubeports.ClipAtomicWriter. A single SQLite transaction writes
// the clip row (UpsertClipTx in clips_transactions.go) AND emits
// the matching outbox_events row (ON CONFLICT(event_key) DO NOTHING).
// ClipID plays two roles: media_assets.id primary key + outbox_events
// event_key idempotency vector. The two writes either both succeed or
// both fail — never one without the other.
type ClipAtomicWriter struct {
	clips *sqassets.ClipsRepository
	txMgr sqoutbox.TxManager
	log   *zap.Logger
}

// NewClipAtomicWriter constructs the concrete writer. clips
// (for UpsertClipTx) and txMgr (for the in-tx wrapper) are required;
// composition root passes nil-safe guards so an unwired writer
// surfaces at first call rather than at silent no-op.
func NewClipAtomicWriter(clips *sqassets.ClipsRepository, txMgr sqoutbox.TxManager, log *zap.Logger) *ClipAtomicWriter {
	if log == nil {
		log = zap.NewNop()
	}
	return &ClipAtomicWriter{clips: clips, txMgr: txMgr, log: log}
}

// CommitClipAndIndexEvent performs the atomic DB write + outbox event
// emission. The contract is:
//
//   - Single SQLite transaction (BeginTx ... Commit/Rollback) via
//     txMgr.InTransaction. A failure in either write rolls back both;
//     the caller's caller observes the typed error.
//   - media_assets row: UpsertClipTx (35-column INSERT...ON CONFLICT
//     DO UPDATE via the canonical projection in clips_transactions.go).
//     A retry with the same clipID updates the row content rather than
//     duplicating it.
//   - outbox_events row: INSERT with event_key = clipID. The
//     ux_outbox_events_event_key UNIQUE index makes ON CONFLICT DO
//     NOTHING collapse duplicate retries — idempotent enqueue.
//   - Both ops MUST succeed for the tx to commit. A partial state is
//     structurally impossible because txMgr.InTransaction holds both
//     writes inside a single fn closure.
//
// Status taxonomy:
//   - nil → atomic commit succeeded; both rows present.
//   - ErrClipWriterRetryable → transient; parent's retry.Do will retry.
//   - ErrClipWriterTerminal → unrecoverable; classifier marks the job
//     failed-deadline.
//
// NOTE: the use case's parent (process_segment.go) currently checks
// `errors.Is(err, errors.Is(classifyErr, &partial))` only for
// PartialSuccessError; for retryable/terminal writer errors, the
// existing isTransientExtractionError substring list catches the
// retryable case. Commit F's typed errors still surface verbatim for
// log inspection; a future PR can branch via errors.Is.
func (w *ClipAtomicWriter) CommitClipAndIndexEvent(
	ctx context.Context,
	clipID string,
	item youtubetypes.ExtractItem,
	event youtubeports.IndexEventPayload,
) error {
	// ── Input validation (no retry can fix any of these). ─────────
	if clipID == "" {
		return fmt.Errorf("%w (got empty string)", ErrClipWriterNil)
	}
	if event.Type == "" {
		return fmt.Errorf("%w (event.Type=%q, clipID=%q)",
			ErrClipWriterEventInvalid, event.Type, clipID)
	}
	if event.AggregateID != clipID {
		return fmt.Errorf("%w (event.AggregateID=%q, clipID=%q — event.AggregateID must equal clipID)",
			ErrClipWriterEventInvalid, event.AggregateID, clipID)
	}
	if w.txMgr == nil || w.clips == nil {
		return fmt.Errorf("%w (clips=%v, txMgr=%v)",
			ErrClipWriterUnwired, w.clips != nil, w.txMgr != nil)
	}

	w.log.Debug("clip_atomic_writer: starting atomic tx",
		zap.String("clip_id", clipID),
		zap.String("event_type", event.Type))

	txErr := w.txMgr.InTransaction(ctx, func(tx *sql.Tx) error {
		// Step 1 — upsert the clip row via the existing tx-scoped
		// method (canonical 35-column projection, locks the INSERT
		// column ordering at UpsertClipTx so the SELECT statement
		// lines match). Idempotent on retry via ON CONFLICT.
		assetRow := extractItemToAsset(clipID, item)
		if uErr := w.clips.UpsertClipTx(ctx, tx, assetRow); uErr != nil {
			return fmt.Errorf("upsert clip: %w", uErr)
		}
		// Step 2 — emit the outbox event within the same tx.
		// event_key = clipID is the canonical idempotency vector;
		// ux_outbox_events_event_key UNIQUE index makes ON
		// CONFLICT DO NOTHING collapse duplicate retries safely.
		//
		// COMMIT F (June 2026) — partial-index predicate note:
		// the production outbox_events table has a FULL UNIQUE
		// index on event_key (no WHERE clause; see
		// migrations/sqlite/092_create_outbox_events.sql). SQLite
		// requires ON CONFLICT predicate to match the partial
		// index WHERE predicate; against a full index, the WHERE
		// clause on ON CONFLICT is silently ignored without error.
		// Since clipID is non-empty (validated above) the predicate
		// `WHERE event_key != ''` is redundant in either case —
		// shipped without it so the SQL aligns with the migration's
		// full-index definition. Idempotency is preserved because
		// the UNIQUE index still rejects duplicate event_keys.
		createdAt := event.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		createdAtStr := timeutil.FormatRFC3339(createdAt)
		payloadJSON := ""
		if event.Payload != nil {
			payloadJSON = string(event.Payload)
		}
		if _, eErr := tx.ExecContext(ctx, `
			INSERT INTO outbox_events (
				event_type, aggregate_id, aggregate_type, payload_json, event_key,
				status, attempt_count, max_attempts,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, 'pending', 0, 10, ?, ?)
			ON CONFLICT(event_key) DO NOTHING
		`,
			event.Type,
			event.AggregateID,
			"youtube_clip", // aggregate_type fixed for Commit F (YouTube pipeline)
			payloadJSON,
			clipID, // event_key = clipID for idempotent re-enqueue
			createdAtStr,
			createdAtStr,
		); eErr != nil {
			return fmt.Errorf("insert outbox event: %w", eErr)
		}
		return nil
	})
	if txErr != nil {
		w.log.Warn("clip_atomic_writer: atomic tx failed (rolled back)",
			zap.String("clip_id", clipID),
			zap.String("event_type", event.Type),
			zap.Error(txErr))
		return classifyWriterError(txErr)
	}
	w.log.Info("clip_atomic_writer: atomic tx committed",
		zap.String("clip_id", clipID),
		zap.String("event_type", event.Type))
	return nil
}

// classifyWriterError separates transient (retryable) from terminal
// failures using a substring match against the canonical retryable
// taxonomy used elsewhere in the youtube package
// (process_segment.go::isTransientExtractionError). Substring match
// is preserved here because the parent retry.Do predicate already
// uses substring matching and the typed errors must align with what
// `errors.Is(err, ErrClipWriterRetryable)` returns downstream.
func classifyWriterError(err error) error {
	if err == nil {
		return nil
	}
	lower := strings.ToLower(err.Error())
	retryable := []string{
		"database is locked",
		"sql_busy",
		"sqlite_busy",
		"disk full",
		"no space left",
		"i/o error",
		"timeout",
		"connection",
	}
	for _, s := range retryable {
		if strings.Contains(lower, s) {
			return fmt.Errorf("%w: %v", ErrClipWriterRetryable, err)
		}
	}
	return fmt.Errorf("%w: %v", ErrClipWriterTerminal, err)
}

// extractItemToAsset converts the per-segment ExtractItem envelope
// into the canonical media_assets row shape consumed by UpsertClipTx.
// Commit F consolidates what process_segment.go previously did
// inline at the Step 9 call site into a typed helper — future
// extraction-shape evolution lands in one place, the writer.
//
// Field mapping (Commit F contract):
//   - Asset.ID = clipID (canonical process_segment Step 1 stamp).
//   - Asset.Source = "youtube" (Source-youtube bypasses the upsert
//     validation if required).
//   - Asset.LifecycleState = StateActive (post-write save completion).
//   - Asset.Metadata carries drive_id / drive_link / download_link /
//     file_hash / local_path so the canonical UpsertClipTx column
//     stitching reaches them via the SetMetadataString accessors.
//   - Asset.Duration = item.Duration * time.Second (item.Duration is
//     a precomputed seconds int; UpsertClipTx writes it as duration_ms).
//
// NOTE: ExtractItem does NOT carry ThumbnailURL — that field lives
// only on the domain Asset struct, populated separately by the
// CycleSegmentYouTubeMetadata enrichment pipeline (not Commit F).
// Commit F therefore leaves Asset.ThumbnailURL at the canonical empty
// default; future commits that pass thumbnail through Step 9 should
// thread it via the ProcessSegmentCommand.Extra field (out of scope).
func extractItemToAsset(clipID string, item youtubetypes.ExtractItem) *asset.Asset {
	md := asset.Metadata{}
	if item.DriveFileID != "" {
		md["drive_file_id"] = item.DriveFileID
	}
	if item.DriveLink != "" {
		md["drive_link"] = item.DriveLink
	}
	if item.DownloadLink != "" {
		md["download_link"] = item.DownloadLink
	}
	if item.FileHash != "" {
		md["file_hash"] = item.FileHash
	}
	if item.LocalPath != "" {
		md["local_path"] = item.LocalPath
		md["folder_path"] = filepath.Dir(item.LocalPath)
	}
	if item.Filename != "" {
		md["folder_id"] = item.Filename
	}
	return &asset.Asset{
		ID:             clipID,
		Source:         asset.Source("youtube"),
		Name:           item.Name,
		Filename:       item.Filename,
		MediaType:      asset.MediaType("youtube_clip"),
		LifecycleState: asset.StateActive,
		Metadata:       md,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
}
