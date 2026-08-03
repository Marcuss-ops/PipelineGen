// Package assets — clip_metadata_writer_payload.go: payload + tx-bound
// persistence helpers extracted from clip_metadata_writer.go as part
// of the file split (July 2026, PostgREST extract-helpers discipline).
//
// godlike/06 SSOT: the three helper functions here are the SINGLE
// canonical surface for metadata payload construction and tx-bound
// media_assets / asset_text_tracks writes driven by the
// ClipMetadataWriterAdapter entry points in clip_metadata_writer.go.
//
// Companion files:
//   - clip_metadata_writer.go (canonical) — owns the
//     ClipMetadataWriterAdapter CONCRETE type + constructor +
//     the two public entry points + the Pattern 0 compile-time
//     pin (var _ youtubeports.ClipMetadataWriter = ...).
//   - clip_metadata_writer_hashes.go — owns
//     ComputeContentHashWithTextTracks + BuildMetadataEventKey.
//
// NOTE on user-requested "localizeTextTracks helper": that function
// does NOT exist in the current source (verified by rg search on
// `localizeTextTracks` — zero matches). A future commit that
// introduces localizeTextTracks should land it here per the same
// payload-layer grouping; for now this file owns the three helpers
// that DO exist (updateMediaAssetsMetadataTx, buildMetadataPayload,
// upsertTextTracksInTx) and the gap is documented in the commit
// message as a "spec-vs-source" note rather than fabricating a
// placeholder.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	texttracks "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/texttracks"

	"github.com/google/uuid"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

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
		"normalized_group": m.NormalizedGroup,
	}
	// Do not let a sparse enrichment result erase canonical provenance
	// written during the atomic clip commit.
	if m.SourceURL != "" {
		meta["source_url"] = m.SourceURL
	}
	if m.SourceProvider != "" {
		meta["source_provider"] = m.SourceProvider
	}
	if m.VideoID != "" {
		// source_video_id is the canonical provenance key; video_id is
		// retained as a legacy alias for older consumers (e.g.
		// texttracks backfill_acquire.go).
		meta["source_video_id"] = m.VideoID
		meta["video_id"] = m.VideoID
	}
	if m.Title != "" {
		meta["title"] = m.Title
	}
	if m.SourceTitle != "" {
		meta["source_title"] = m.SourceTitle
	}
	if m.SourceChannel != "" {
		meta["source_channel"] = m.SourceChannel
	}
	// start_sec / end_sec are the canonical float-seconds keys read by
	// asset.StartSec()/EndSec(); clip_start_sec/clip_end_sec are legacy
	// aliases retained for history.
	if m.ClipStartSec != 0 {
		meta["start_sec"] = float64(m.ClipStartSec)
		meta["clip_start_sec"] = m.ClipStartSec
	}
	if m.ClipEndSec != 0 {
		meta["end_sec"] = float64(m.ClipEndSec)
		meta["clip_end_sec"] = m.ClipEndSec
	}
	if m.ClipDurationSec != 0 {
		meta["clip_duration_sec"] = m.ClipDurationSec
	}
	if m.PolicyVersion != "" {
		meta["policy_version"] = m.PolicyVersion
	}
	if m.DrivePath != "" {
		meta["drive_path"] = m.DrivePath
	}
	if m.ContentHash != "" {
		meta["content_hash"] = m.ContentHash
	}
	if m.Hook != "" {
		meta["hook"] = m.Hook
	}
	if m.SearchVisibility != "" {
		meta["search_visibility"] = m.SearchVisibility
	}
	// ── Text track projection (lightweight, no full transcripts) ──
	if m.OriginalLanguage != "" {
		meta["original_language"] = m.OriginalLanguage
	}
	if len(m.AvailableLanguages) > 0 {
		meta["available_languages"] = m.AvailableLanguages
	}
	if m.TranscriptAvailable {
		meta["transcript_available"] = true
	}
	if m.TextTracksVersion != "" {
		meta["text_tracks_version"] = m.TextTracksVersion
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
		"idempotency_key":  BuildMetadataEventKey(clipID, m.SourceVersion),
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

// upsertTextTracksInTx persists a batch of text tracks inside the
// caller's transaction. Uses INSERT ON CONFLICT DO UPDATE on the
// UNIQUE(asset_id, language_code, text_kind) constraint.
//
// `texttracks.UpsertTextTrackSQL` is the canonical SQL constant declared in
// text_track_repository.go (same `assets` package — package-level
// symbol sharing makes it visible without an explicit import).
func upsertTextTracksInTx(ctx context.Context, tx *sql.Tx, tracks []asset.TextTrack, nowStr string) error {
	if len(tracks) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, texttracks.UpsertTextTrackSQL)
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
