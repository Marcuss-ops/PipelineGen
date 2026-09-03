// Package media — clip_metadata_writer.go is the canonical YouTube clip
// metadata writer on the PostgreSQL media SSOT. Ported during the media
// demolition (September 2026) from internal/platform/sqlite/assets/
// imagesregistry/clip_metadata_writer.go (+ _payload/_hashes): the atomic
// metadata + text-tracks + outbox write assembles over the SAME
// PostgresMediaCommitter primitives (PatchMetadataJSONTx, tx-scoped
// text-track upserts, tx-bound outbox enqueue) — one engine, one
// transaction, zero SQLite media writes.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// ── Event-key + index-revision fingerprints (port of clip_metadata_writer_hashes.go) ──

// ComputeIndexRevision folds the byte-identity content hash with the
// sorted text-track text hashes. content_sha256 stays pure BYTE identity
// and never folds text tracks (godlike/06).
func ComputeIndexRevision(contentHash string, textTracks []detail.TextTrack) string {
	if len(textTracks) == 0 {
		return contentHash
	}

	sorted := make([]detail.TextTrack, len(textTracks))
	copy(sorted, textTracks)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].LanguageCode != sorted[j].LanguageCode {
			return sorted[i].LanguageCode < sorted[j].LanguageCode
		}
		return sorted[i].TextKind < sorted[j].TextKind
	})

	var b strings.Builder
	b.WriteString(contentHash)
	b.WriteString("|")
	for _, t := range sorted {
		if t.TextHash != "" {
			b.WriteString(t.TextHash)
			b.WriteString(";")
		}
	}

	h := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", h)
}

// BuildMetadataEventKey returns the canonical event_key for the metadata
// write: "metadata:reindex:<clipID>:<sourceVersion>".
func BuildMetadataEventKey(clipID, sourceVersion string) string {
	if sourceVersion == "" {
		return fmt.Sprintf("metadata:reindex:%s:nosource", clipID)
	}
	return fmt.Sprintf("metadata:reindex:%s:%s", clipID, sourceVersion)
}

// ── Metadata patch builder (port of clip_metadata_writer_payload.go) ─────

// metadataPatchFromCanonical builds the complete typed metadata snapshot
// map for the enrichment write. Conditional keys are included only when
// their source fields are non-empty — a sparse enrichment result must not
// erase canonical provenance written during the atomic clip commit.
func metadataPatchFromCanonical(m youtubetypes.CanonicalClipMetadata, out map[string]any) {
	out["summary"] = m.Summary
	out["topics"] = m.Topics
	out["speakers"] = m.Speakers
	out["mentioned_people"] = m.MentionedPeople
	out["quality_score"] = m.QualityScore
	out["sponsor_segment"] = m.SponsorSegment
	out["transcript_path"] = m.TranscriptPath
	out["normalized_group"] = m.NormalizedGroup
	if m.SourceURL != "" {
		out["source_url"] = m.SourceURL
	}
	if m.SourceProvider != "" {
		out["source_provider"] = m.SourceProvider
	}
	if m.VideoID != "" {
		// source_video_id is the canonical provenance key; video_id is
		// retained as a legacy alias for older consumers (e.g.
		// texttracks backfill_acquire.go).
		out["source_video_id"] = m.VideoID
		out["video_id"] = m.VideoID
	}
	if m.Title != "" {
		out["title"] = m.Title
	}
	if m.Description != "" {
		out["description"] = m.Description
	}
	if len(m.Tags) > 0 {
		out["tags"] = m.Tags
	}
	if m.SourceTitle != "" {
		out["source_title"] = m.SourceTitle
	}
	if m.SourceChannel != "" {
		out["source_channel"] = m.SourceChannel
	}
	// start_sec / end_sec are the canonical float-seconds keys; the
	// clip_*_sec names are legacy aliases retained for history.
	if m.ClipStartSec != 0 {
		out["start_sec"] = float64(m.ClipStartSec)
		out["clip_start_sec"] = m.ClipStartSec
	}
	if m.ClipEndSec != 0 {
		out["end_sec"] = float64(m.ClipEndSec)
		out["clip_end_sec"] = m.ClipEndSec
	}
	if m.ClipDurationSec != 0 {
		out["clip_duration_sec"] = m.ClipDurationSec
	}
	if m.PolicyVersion != "" {
		out["policy_version"] = m.PolicyVersion
	}
	if m.DrivePath != "" {
		out["drive_path"] = m.DrivePath
	}
	if m.ContentHash != "" {
		out["content_hash"] = m.ContentHash
	}
	// index_revision is the SEPARATE indexable-snapshot fingerprint
	// (folds byte identity + text tracks + metadata). content_hash above
	// stays pure BYTE identity (godlike/06).
	if m.SourceVersion != "" {
		out["index_revision"] = m.SourceVersion
	}
	if m.Hook != "" {
		out["hook"] = m.Hook
	}
	if m.SearchVisibility != "" {
		out["search_visibility"] = m.SearchVisibility
	}
	if m.OriginalLanguage != "" {
		out["original_language"] = m.OriginalLanguage
	}
	if len(m.AvailableLanguages) > 0 {
		out["available_languages"] = m.AvailableLanguages
	}
}

// buildMetadataPayload builds the ReindexEnvelopeV1 payload for a metadata
// re-index event (port of buildMetadataPayload).
func buildMetadataPayload(clipID string, m youtubetypes.CanonicalClipMetadata, nowStr string) (string, error) {
	payload := map[string]any{
		"schema_version":   ReindexEnvelopeV1Schema,
		"event_id":         uuid.NewString(),
		"clip_id":          clipID,
		"asset_id":         m.AssetID,
		"source_version":   m.SourceVersion,
		"index_revision":   m.SourceVersion,
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

// ── Canonical clip metadata writer (PG) ──────────────────────────────────

// UpdateClipMetadataAndRequestIndex performs the canonical atomic metadata
// write + index-request emission on the PostgreSQL media SSOT:
// metadata patch + outbox event in ONE transaction. Fail-closed: a partial
// commit is impossible (tx.Commit() either commits both or rolls back
// both). A missing media_assets row surfaces as a typed error (the UPDATE
// matches zero rows → tx rolls back).
func (c *PostgresMediaCommitter) UpdateClipMetadataAndRequestIndex(
	ctx context.Context,
	clipID string,
	m youtubetypes.CanonicalClipMetadata,
) error {
	return c.updateClipMetadataAndRequestIndex(ctx, clipID, m, nil)
}

// UpdateClipMetadataTextsAndRequestIndex extends the metadata write to also
// persist text tracks in the same atomic transaction (so the pgvector
// re-indexer always sees the latest transcripts). When textTracks is empty
// it behaves identically to UpdateClipMetadataAndRequestIndex.
func (c *PostgresMediaCommitter) UpdateClipMetadataTextsAndRequestIndex(
	ctx context.Context,
	clipID string,
	m youtubetypes.CanonicalClipMetadata,
	textTracks []detail.TextTrack,
) error {
	if len(textTracks) == 0 {
		return c.updateClipMetadataAndRequestIndex(ctx, clipID, m, nil)
	}
	return c.updateClipMetadataAndRequestIndex(ctx, clipID, m, textTracks)
}

func (c *PostgresMediaCommitter) updateClipMetadataAndRequestIndex(
	ctx context.Context,
	clipID string,
	m youtubetypes.CanonicalClipMetadata,
	textTracks []detail.TextTrack,
) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: committer not wired")
	}
	if clipID == "" {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: clipID is required")
	}
	if m.ClipID == "" {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: CanonicalClipMetadata.ClipID is required (mismatched writer call — caller bug)")
	}
	if m.ClipID != clipID {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: clipID %q != CanonicalClipMetadata.ClipID %q (mismatched writer call — caller bug)",
			clipID, m.ClipID)
	}

	// Text-track index revision: content_hash stays BYTE identity; the
	// index revision folds text-track content so the supersede gate +
	// event_key change when a translation is added/corrected — WITHOUT
	// corrupting byte identity (godlike/06).
	if len(textTracks) > 0 && m.ContentHash != "" {
		m.SourceVersion = ComputeIndexRevision(m.ContentHash, textTracks)
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	nowStr := time.Now().UTC().Format(time.RFC3339)

	// 1) Metadata patch through the canonical committer boundary.
	meta := map[string]any{}
	metadataPatchFromCanonical(m, meta)
	patchJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: marshal patch: %w", err)
	}
	if err := c.assets.PatchMetadataJSONTx(ctx, tx, clipID, string(patchJSON), nowStr); err != nil {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: update media_assets: %w", err)
	}

	// 2) Text-track upsert (optional path).
	if len(textTracks) > 0 {
		if err := upsertTextTracksInTxPG(ctx, tx, textTracks, nowStr); err != nil {
			return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: upsert text tracks: %w", err)
		}
	}

	// 3) Outbox event (tx-bound, idempotent event_key). The event_key is
	// "metadata:reindex:<clipID>:<sourceVersion>" so a re-write with the
	// same content collapses via ON CONFLICT (idempotent) and a write
	// with different content produces a fresh outbox row (re-index).
	eventKey := BuildMetadataEventKey(clipID, m.SourceVersion)
	payload, err := buildMetadataPayload(clipID, m, nowStr)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: build payload: %w", err)
	}
	enqResult, err := c.box.Enqueue(ctx, tx, EventAssetIndexRequested, clipID, "media_asset", payload, eventKey)
	if err != nil {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: outbox enqueue: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("PostgresMediaCommitter.updateClipMetadata: commit: %w", err)
	}
	committed = true

	if c.log != nil {
		// Surface ON CONFLICT suppression by an existing terminal row
		// (dead_letter/superseded): the write committed but its re-index
		// event was squelched — operators must be able to spot the
		// dead_letter pinned row.
		if !enqResult.Inserted && isTerminalOutboxStatus(enqResult.ExistingStatus) {
			c.log.Warn("canonical clip metadata writer: outbox event suppressed by existing terminal row",
				zap.String("clip_id", clipID),
				zap.String("event_key", eventKey),
				zap.Int64("existing_event_id", enqResult.EventID),
				zap.String("existing_status", enqResult.ExistingStatus))
		} else {
			c.log.Debug("canonical clip metadata writer: metadata + index event committed",
				zap.String("clip_id", clipID),
				zap.String("event_key", eventKey),
				zap.String("source_version", m.SourceVersion),
				zap.Int("text_track_count", len(textTracks)),
				zap.Bool("outbox_inserted", enqResult.Inserted))
		}
	}
	return nil
}

// Compile-time assertion: PostgresMediaCommitter satisfies the canonical
// ClipMetadataWriter port directly (SQLite mirror: ClipMetadataWriterAdapter).
var _ youtubeports.ClipMetadataWriter = (*PostgresMediaCommitter)(nil)
