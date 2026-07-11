// Package assets — clip_metadata_writer.go: ClipMetadataWriterAdapter
// concrete implementation of the youtubeports.ClipMetadataWriter typed
// port.
//
// Commit 4/6 (PR-C-YouTube-Cutover, June 2026, P1 #15 + #16): the
// canonical metadata-write + index-request pair. A single SQLite
// transaction performs:
//
//	BEGIN
//	UPDATE media_assets SET metadata_json = json_set(...) WHERE id = ?
//	INSERT outbox_events (Type='asset.index.requested',
//	                       payload={clip_id, asset_id, source_version, job_id},
//	                       event_key=derived) ON CONFLICT DO NOTHING
//	COMMIT
//
// Why metadata_json (not first-class columns): the canonical
// media_assets schema (per migrations/sqlite/033_media_assets_*)
// stores summary/topics/speakers/mentioned_people/quality_score/
// sponsor_segment under the metadata_json TEXT column as JSON keys.
// The existing pattern (e.g.
// media_assets_discovery_repository.go::json_set on metadata_json)
// uses the same projection. A future commit may promote these
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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
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
	eventKey := buildMetadataEventKey(clipID, m.SourceVersion)
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

// updateMediaAssetsMetadataTx builds the full metadata JSON object
// in Go (18 canonical keys — 9 from the original PR-C commit + 9
// from PR-YT-DOD-7) and merges it into the existing metadata_json
// via SQLite's json_patch. The idempotency contract is preserved:
// json_patch of identical keys is idempotent on the JSON value.
//
// Conditional keys (Hook, SearchVisibility) are included only when
// their CanonicalClipMetadata fields are non-empty.
func updateMediaAssetsMetadataTx(
	ctx context.Context,
	tx *sql.Tx,
	clipID string,
	m youtubetypes.CanonicalClipMetadata,
	nowStr string,
) error {
	// Build the metadata JSON in Go — avoids the fragile deeply-nested
	// json_set chain that was error-prone at 18+ keys (PR-YT-DOD-7).
	meta := map[string]any{
		"summary":          m.Summary,
		"topics":           m.Topics,
		"speakers":         m.Speakers,
		"mentioned_people": m.MentionedPeople,
		"quality_score":    m.QualityScore,
		"sponsor_segment":  m.SponsorSegment,
		"transcript_path":  m.TranscriptPath,
		"source_url":       m.SourceURL,
		"normalized_group": m.NormalizedGroup,
		// ── PR-YT-DOD-7: 9 new canonical fields ──
		"source_provider":   m.SourceProvider,
		"video_id":          m.VideoID,
		"title":             m.Title,
		"clip_start_sec":    m.ClipStartSec,
		"clip_end_sec":      m.ClipEndSec,
		"clip_duration_sec": m.ClipDurationSec,
		"policy_version":    m.PolicyVersion,
		"drive_path":        m.DrivePath,
		"content_hash":      m.ContentHash,
	}
	if m.Hook != "" {
		meta["hook"] = m.Hook
	}
	if m.SearchVisibility != "" {
		meta["search_visibility"] = m.SearchVisibility
	}

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata JSON: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE media_assets
		SET metadata_json = json_patch(COALESCE(metadata_json, '{}'), ?),
		    updated_at = ?
		WHERE id = ?
	`, string(metaJSON), nowStr, clipID)
	return err
}

// buildMetadataEventKey returns the canonical event_key for the
// metadata write. Shape:
//
//	metadata:reindex:<clipID>:<sourceVersion>
//
// Different from the Step 9 ClipAtomicWriter's event_key
// (`reconcile:reindex:<clipID>:<schema>:<sourceVersion>`) so the
// outbox dispatcher treats the two events as distinct (the
// metadata event triggers a Qdrant re-index, the clip event
// triggers a fresh insert).
func buildMetadataEventKey(clipID, sourceVersion string) string {
	if sourceVersion == "" {
		// Empty sourceVersion is fail-closed at the builder
		// level; the writer surfaces an empty event_key as
		// a defensive marker so the dispatcher can detect
		// the bug rather than silently produce a
		// deterministic-but-misleading key.
		return fmt.Sprintf("metadata:reindex:%s:nosource", clipID)
	}
	return fmt.Sprintf("metadata:reindex:%s:%s", clipID, sourceVersion)
}

// buildMetadataPayload builds the JSON payload the outbox event
// carries. The payload carries identification (clip_id, asset_id,
// source_version, job_id) PLUS the metadata fields themselves for
// operator audit (visible in the dispatcher log + dashboard). The
// re-indexer reads the freshly written metadata_json from SQLite
// when the event is delivered (no second query needed).
//
// Hook + SearchVisibility are included ONLY when their
// CanonicalClipMetadata fields are non-empty (no empty keys
// emitted to the payload).
func buildMetadataPayload(
	clipID string,
	m youtubetypes.CanonicalClipMetadata,
	nowStr string,
) (string, error) {
	// Blocco 1.2: schema_version aligned to the canonical
	// outboxevents.ReindexEnvelopeV1Schema ("asset.index.requested.v1")
	// so the IndexingHandler consumer matches it against the event_type
	// "asset.index.requested". The previous literal
	// "asset.metadata.requested.v1" caused every metadata event to be
	// classified terminal (dead_letter). Also added the required
	// event_id field (UUID) that the handler validates.
	eventID := uuid.NewString()
	payload := map[string]any{
		"schema_version":   outboxevents.ReindexEnvelopeV1Schema,
		"event_id":         eventID,
		"clip_id":          clipID,
		"asset_id":         m.AssetID,
		"source_version":   m.SourceVersion,
		"job_id":           m.JobID,
		"quality_score":    m.QualityScore,
		"sponsor_segment":  m.SponsorSegment,
		"normalized_group": m.NormalizedGroup,
		"summary":          m.Summary,
		"topics":           m.Topics,
		"speakers":         m.Speakers,
		"mentioned_people": m.MentionedPeople,
		"transcript_path":  m.TranscriptPath,
		"source_url":       m.SourceURL,
		"requested_at":     nowStr,
		"idempotency_key":  buildMetadataEventKey(clipID, m.SourceVersion),
	}
	if m.Hook != "" {
		payload["hook"] = m.Hook
	}
	if m.SearchVisibility != "" {
		payload["search_visibility"] = m.SearchVisibility
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	return string(out), nil
}

// ComputeContentHashWithTextTracks computes a deterministic content
// hash that includes text track hashes. This ensures Qdrant re-indexes
// when a translation is added or corrected, even when the MP4 file
// hasn't changed.
//
// Formula: SHA256(file_hash + "|" + sorted(text_track_hashes))
// where sorted means ascending by (language_code, text_kind).
// When no text tracks exist, the hash is just SHA256(file_hash)
// which matches the existing content_hash behavior.
func ComputeContentHashWithTextTracks(fileHash string, textTracks []asset.TextTrack) string {
	if len(textTracks) == 0 {
		return fileHash
	}

	// Sort by (language_code, text_kind) for determinism.
	sorted := make([]asset.TextTrack, len(textTracks))
	copy(sorted, textTracks)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].LanguageCode != sorted[j].LanguageCode {
			return sorted[i].LanguageCode < sorted[j].LanguageCode
		}
		return sorted[i].TextKind < sorted[j].TextKind
	})

	var b strings.Builder
	b.WriteString(fileHash)
	b.WriteString("|")
	for _, t := range sorted {
		if t.TextHash != "" {
			b.WriteString(t.TextHash)
			b.WriteString(";")
		}
	}

	h := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(h[:])
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

	// 1) Compute combined content hash including text tracks.
	// This ensures the source_version changes when translations
	// are added, triggering Qdrant re-index via the outbox event.
	if m.ContentHash != "" {
		m.ContentHash = ComputeContentHashWithTextTracks(m.ContentHash, textTracks)
		m.SourceVersion = m.ContentHash
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
	eventKey := buildMetadataEventKey(clipID, m.SourceVersion)
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

// upsertTextTracksInTx persists a batch of text tracks inside the
// caller's transaction. Uses INSERT ON CONFLICT DO UPDATE on the
// UNIQUE(asset_id, language_code, text_kind) constraint.
func upsertTextTracksInTx(ctx context.Context, tx *sql.Tx, tracks []asset.TextTrack, nowStr string) error {
	if len(tracks) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, upsertTextTrackSQL)
	if err != nil {
		return fmt.Errorf("upsertTextTracksInTx: prepare: %w", err)
	}
	defer stmt.Close()

	for _, t := range tracks {
		var confidence sql.NullFloat64
		if t.Confidence != nil {
			confidence = sql.NullFloat64{Float64: *t.Confidence, Valid: true}
		}
		isOriginal := 0
		if t.IsOriginal {
			isOriginal = 1
		}
		status := string(t.Status)
		if status == "" {
			status = string(asset.TextTrackReady)
		}

		if _, err := stmt.ExecContext(ctx,
			t.AssetID,
			t.LanguageCode,
			string(t.TextKind),
			t.TextContent,
			string(t.SourceType),
			t.SourceLanguageCode,
			isOriginal,
			t.Provider,
			t.ModelName,
			t.ModelVersion,
			t.TextHash,
			t.SourceVersion,
			confidence,
			status,
		); err != nil {
			return fmt.Errorf("upsertTextTracksInTx: exec (asset=%s lang=%s kind=%s): %w",
				t.AssetID, t.LanguageCode, t.TextKind, err)
		}
	}
	return nil
}

// ── Compile-time assertion ──────────────────────────────────────────

// Per AGENTS.md Pattern 0: the concrete receiver must satisfy the
// typed port so any signature drift surfaces as a build failure.
var _ youtubeports.ClipMetadataWriter = (*ClipMetadataWriterAdapter)(nil)
