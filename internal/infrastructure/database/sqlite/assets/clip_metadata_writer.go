// Package assets — clip_metadata_writer.go: ClipMetadataWriterAdapter
// concrete implementation of the youtubeports.ClipMetadataWriter typed
// port.
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SINGLE canonical owner of the ClipMetadataWriterAdapter CONCRETE
// type, its constructor, the two public entry points, and the
// compile-time port satisfaction pin (Pattern 0). Helper functions
// (buildMetadataPayload, updateMediaAssetsMetadataTx, upsertTextTracksInTx,
// BuildMetadataEventKey, ComputeContentHashWithTextTracks) live in the
// adjacent file-level siblings:
//
//   - clip_metadata_writer_payload.go — payload + tx-bound persistence
//     helpers (buildMetadataPayload, updateMediaAssetsMetadataTx,
//     upsertTextTracksInTx).
//   - clip_metadata_writer_hashes.go — deterministic content/event-key
//     hashing (ComputeContentHashWithTextTracks, BuildMetadataEventKey).
//
// The two entry points (UpdateClipMetadataAndRequestIndex and
// UpdateClipMetadataTextsAndRequestIndex) are the SINGLE canonical
// surface that callers reach — they orchestrate the canonical
// metadata-write + index-request pair (Commit 4/6 of PR-C-YouTube-Cutover,
// June 2026, P1 #15 + #16). A single SQLite transaction performs:
//
//	BEGIN
//	UPDATE media_assets SET metadata_json = json_patch(...) WHERE id = ?
//	INSERT outbox_events (Type='asset.index.requested',
//	                       payload={clip_id, asset_id, source_version, job_id},
//	                       event_key=derived) ON CONFLICT DO NOTHING
//	COMMIT
//
// Why metadata_json (not first-class columns): the canonical
// media_assets schema (per migrations/sqlite/033_media_assets_*)
// stores summary/topics/speakers/mentioned_people/quality_score/
// sponsor_segment under the metadata_json TEXT column as JSON keys.
// The existing pattern (json_set on metadata_json) uses the same
// projection. A future commit may promote these
// fields to first-class columns for query performance; this
// adapter's JSON projection is forward-compatible (the column
// mapping is a single helper).
//
// Why a different event_key from the Step 9 ClipAtomicWriter:
// the Step 9 event uses `reconcile:reindex:<clipID>:<schema>:
// <fileHash>`. The metadata event uses
// `metadata:reindex:<clipID>:<sourceVersion>` so the outbox
// dispatcher (a) does not squelch the metadata event on a re-run
// and (b) the Qdrant re-indexer can read the freshly written
// metadata columns (it pulls the row from SQLite on event
// delivery — the payload only carries identification, not the
// payload itself).
package assets

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ClipMetadataWriterAdapter implements youtubeports.ClipMetadataWriter
// over the canonical SQLite + outbox events schema. The adapter holds
// a *sql.DB (ledger connection) and a *outboxevents.Repository; the
// latter talks into the SAME connection within the same tx per the
// outboxevents.Repository.Enqueue contract.
type ClipMetadataWriterAdapter struct {
	db  *sql.DB
	box *outboxevents.Repository
	log *zap.Logger
	now func() time.Time // injectable clock for tests; production = time.Now
}

// NewClipMetadataWriterAdapter constructs the adapter. Both db AND box
// MUST be non-nil — a nil either side is a fail-closed panic so a
// wiring gap lands at startup, not at first
// UpdateClipMetadataAndRequestIndex call.
func NewClipMetadataWriterAdapter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *ClipMetadataWriterAdapter {
	if db == nil {
		panic("assets.NewClipMetadataWriterAdapter: db is required (composition must pass root.DB.DB)")
	}
	if box == nil {
		panic("assets.NewClipMetadataWriterAdapter: outboxevents.Repository is required (composition must pass root.Outbox.EventsRepo)")
	}
	return &ClipMetadataWriterAdapter{
		db:  db,
		box: box,
		log: log,
		now: time.Now,
	}
}

// UpdateClipMetadataAndRequestIndex performs the canonical atomic
// metadata write + index-request emission. Returns:
//
//   - nil on commit success (both UPDATE + INSERT landed).
//   - fmt.Errorf wrapping the underlying SQLite error on tx failure.
//   - A typed *MissingClipError when no media_assets row matches
//     clipID (the UPDATE matches zero rows → tx rolls back).
//
// The function is fail-closed: a partial commit is impossible because
// the UPDATE and the INSERT live in the same tx (tx.Commit() either
// commits both or rolls back both). Empty clipID or empty
// m.ClipID returns an explicit error before opening a tx.
func (w *ClipMetadataWriterAdapter) UpdateClipMetadataAndRequestIndex(
	ctx context.Context,
	clipID string,
	m youtubetypes.CanonicalClipMetadata,
) error {
	if w == nil || w.db == nil || w.box == nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: adapter not wired")
	}
	if clipID == "" {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: clipID is required")
	}
	if m.ClipID == "" {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: CanonicalClipMetadata.ClipID is required (mismatched writer call — caller bug)")
	}
	if m.ClipID != clipID {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: clipID %q != CanonicalClipMetadata.ClipID %q (mismatched writer call — caller bug)",
			clipID, m.ClipID)
	}

	// 1) Begin tx
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 2) UPDATE media_assets.metadata_json via json_patch — builds
	// the full JSON object in Go (PR-YT-DOD-7: 18 canonical keys
	// including the 9 new DoD 7 fields) and merges it with the
	// existing metadata_json via SQLite's json_patch.
	nowStr := w.now().UTC().Format(time.RFC3339)
	if err := updateMediaAssetsMetadataTx(ctx, tx, clipID, m, nowStr); err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: update media_assets: %w", err)
	}

	// 3) Build outbox event_key + payload. The event_key is
	// "metadata:reindex:<clipID>:<sourceVersion>" so a re-write
	// with the same content collapses via ON CONFLICT DO NOTHING
	// (idempotent) and a write with different content produces a
	// fresh outbox row (re-index).
	eventKey := BuildMetadataEventKey(clipID, m.SourceVersion)
	payload, err := buildMetadataPayload(clipID, m, nowStr)
	if err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: build payload: %w", err)
	}

	// 4) INSERT outbox_events (tx-bound).
	enqResult, err := w.box.Enqueue(
		ctx,
		tx,
		outboxevents.EventAssetIndexRequested,
		clipID,
		"media_asset",
		payload,
		eventKey,
	)
	if err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: outbox enqueue: %w", err)
	}

	// 5) Commit
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: commit: %w", err)
	}
	committed = true

	if w.log != nil {
		// Blocco 2.1: surface ON CONFLICT suppression by an existing
		// terminal row (dead_letter/superseded). A new
		// metadata_enrichment write was committed, but its
		// re-index event was silently squelched — the writer
		// MUST raise a warning so operator/cron can spot the
		// dead_letter pinned row.
		if !enqResult.Inserted && isTerminalOutboxStatus(enqResult.ExistingStatus) {
			w.log.Warn("ClipMetadataWriterAdapter: metadata outbox event suppressed by existing terminal row",
				zap.String("clip_id", clipID),
				zap.String("event_key", eventKey),
				zap.Int64("existing_event_id", enqResult.EventID),
				zap.String("existing_status", enqResult.ExistingStatus))
		} else {
			w.log.Debug("ClipMetadataWriterAdapter: metadata + index event committed",
				zap.String("clip_id", clipID),
				zap.String("event_key", eventKey),
				zap.String("source_version", m.SourceVersion),
				zap.Float64("quality_score", m.QualityScore),
				zap.Bool("sponsor_segment", m.SponsorSegment),
				zap.Bool("outbox_inserted", enqResult.Inserted))
		}
	}
	return nil
}

// UpdateClipMetadataTextsAndRequestIndex extends the metadata write
// to also persist text tracks in the same atomic transaction. When
// textTracks is non-empty, each track is upserted on the
// UNIQUE(asset_id, language_code, text_kind) constraint so the
// Qdrant re-indexer always sees the latest transcripts.
//
// When textTracks is empty, the method delegates directly to
// UpdateClipMetadataAndRequestIndex (no extra work).
func (w *ClipMetadataWriterAdapter) UpdateClipMetadataTextsAndRequestIndex(
	ctx context.Context,
	clipID string,
	m youtubetypes.CanonicalClipMetadata,
	textTracks []asset.TextTrack,
) error {
	if len(textTracks) == 0 {
		return w.UpdateClipMetadataAndRequestIndex(ctx, clipID, m)
	}

	if w == nil || w.db == nil || w.box == nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: adapter not wired")
	}
	if clipID == "" {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: clipID is required")
	}
	if m.ClipID == "" {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: CanonicalClipMetadata.ClipID is required")
	}
	if m.ClipID != clipID {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: clipID %q != CanonicalClipMetadata.ClipID %q",
			clipID, m.ClipID)
	}

	// 1) Derive the index revision. content_hash stays BYTE identity
	// (immutable across metadata/text-track changes); the index
	// revision is a SEPARATE fingerprint that folds text-track
	// content so the supersede gate + outbox event_key change when
	// a translation is added/corrected — WITHOUT corrupting byte
	// identity (godlike/06: content_sha256 vs index_revision).
	if m.ContentHash != "" {
		m.SourceVersion = ComputeIndexRevision(m.ContentHash, textTracks)
	}

	// 2) Begin tx
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// 3) UPDATE media_assets.metadata_json
	nowStr := w.now().UTC().Format(time.RFC3339)
	if err := updateMediaAssetsMetadataTx(ctx, tx, clipID, m, nowStr); err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: update media_assets: %w", err)
	}

	// 4) UPSERT asset_text_tracks
	if err := upsertTextTracksInTx(ctx, tx, textTracks, nowStr); err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: upsert text tracks: %w", err)
	}

	// 5) Build outbox event
	eventKey := BuildMetadataEventKey(clipID, m.SourceVersion)
	payload, err := buildMetadataPayload(clipID, m, nowStr)
	if err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: build payload: %w", err)
	}

	// 6) INSERT outbox_events (tx-bound)
	enqResult, err := w.box.Enqueue(
		ctx,
		tx,
		outboxevents.EventAssetIndexRequested,
		clipID,
		"media_asset",
		payload,
		eventKey,
	)
	if err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: outbox enqueue: %w", err)
	}

	// 7) Commit
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataTextsAndRequestIndex: commit: %w", err)
	}
	committed = true

	if w.log != nil {
		if !enqResult.Inserted && isTerminalOutboxStatus(enqResult.ExistingStatus) {
			w.log.Warn("ClipMetadataWriterAdapter: metadata+texts outbox event suppressed by existing terminal row",
				zap.String("clip_id", clipID),
				zap.String("event_key", eventKey),
				zap.Int64("existing_event_id", enqResult.EventID),
				zap.String("existing_status", enqResult.ExistingStatus))
		} else {
			w.log.Debug("ClipMetadataWriterAdapter: metadata + text tracks + index event committed",
				zap.String("clip_id", clipID),
				zap.String("event_key", eventKey),
				zap.Int("text_track_count", len(textTracks)),
				zap.Bool("outbox_inserted", enqResult.Inserted))
		}
	}
	return nil
}

// ── Compile-time assertion ──────────────────────────────────────────

// Per AGENTS.md Pattern 0: the concrete receiver must satisfy the
// typed port so any signature drift surfaces as a build failure.
// This pin lives in the canonical file (godlike/06 SSOT — the
// port-adapter pairing must be visible at the file that owns the
// adapter type, NOT in the helper siblings).
var _ youtubeports.ClipMetadataWriter = (*ClipMetadataWriterAdapter)(nil)
