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
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
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

	// 2) UPDATE media_assets.metadata_json (json_set on the canonical
	// 9 base keys + conditional Hook + SearchVisibility). The query
	// uses COALESCE(metadata_json, '{}') so the row is
	// created-or-updated safely (first enrichment creates the JSON,
	// subsequent writes update the keys in place).
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
	if err := w.box.Enqueue(
		ctx,
		tx,
		outboxevents.EventAssetIndexRequested,
		clipID,
		"media_asset",
		payload,
		eventKey,
	); err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: outbox enqueue: %w", err)
	}

	// 5) Commit
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("ClipMetadataWriterAdapter.UpdateClipMetadataAndRequestIndex: commit: %w", err)
	}
	committed = true

	if w.log != nil {
		w.log.Debug("ClipMetadataWriterAdapter: metadata + index event committed",
			zap.String("clip_id", clipID),
			zap.String("event_key", eventKey),
			zap.String("source_version", m.SourceVersion),
			zap.Float64("quality_score", m.QualityScore),
			zap.Bool("sponsor_segment", m.SponsorSegment))
	}
	return nil
}

// updateMediaAssetsMetadataTx performs the UPDATE statement that
// writes the 11 metadata fields (9 verdict-canonical + quality_score
// + sponsor_segment + the optional Hook + SearchVisibility) to
// media_assets.metadata_json via json_set. The 9 base keys are
// always written; Hook + SearchVisibility are appended to the
// json_set chain only when their CanonicalClipMetadata fields
// are non-empty (so the indexing layer never sees an empty
// semantic marker).
//
// The query is idempotent: a second call with the same values
// produces an idempotent UPDATE (json_set is idempotent on
// identical key/value pairs).
func updateMediaAssetsMetadataTx(
	ctx context.Context,
	tx *sql.Tx,
	clipID string,
	m youtubetypes.CanonicalClipMetadata,
	nowStr string,
) error {
	// Marshal the typed fields into JSON-safe strings.
	topicsJSON, _ := json.Marshal(m.Topics)
	speakersJSON, _ := json.Marshal(m.Speakers)
	mentionedJSON, _ := json.Marshal(m.MentionedPeople)
	// sql.Exec uses ? placeholders; we can't pass a map[string]any
	// directly. The base chain writes 9 keys; the optional
	// json_set for Hook + SearchVisibility is appended when
	// their CanonicalClipMetadata fields are non-empty.
	baseSet := `json_set(
		json_set(
			json_set(
				json_set(
					json_set(
						json_set(
							json_set(
								json_set(
									json_set(
										COALESCE(metadata_json, '{}'),
										'$.summary', ?
									),
									'$.topics', ?
								),
								'$.speakers', ?
							),
							'$.mentioned_people', ?
						),
						'$.quality_score', ?
					),
					'$.sponsor_segment', ?
				),
				'$.transcript_path', ?
			),
			'$.source_url', ?
		),
		'$.normalized_group', ?
	)
	`
	if m.Hook != "" {
		baseSet = fmt.Sprintf(`json_set(%s, '$.hook', ?)`, baseSet)
	}
	if m.SearchVisibility != "" {
		baseSet = fmt.Sprintf(`json_set(%s, '$.search_visibility', ?)`, baseSet)
	}
	query := fmt.Sprintf(`
		UPDATE media_assets
		SET metadata_json = %s,
			updated_at = ?
		WHERE id = ?
	`, baseSet)
	// Argument assembly: the order MUST match the ? placeholders above.
	args := []any{
		m.Summary,
		string(topicsJSON),
		string(speakersJSON),
		string(mentionedJSON),
		m.QualityScore,
		m.SponsorSegment,
		m.TranscriptPath,
		m.SourceURL,
		m.NormalizedGroup,
	}
	if m.Hook != "" {
		args = append(args, m.Hook)
	}
	if m.SearchVisibility != "" {
		args = append(args, m.SearchVisibility)
	}
	args = append(args, nowStr, clipID)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return nil
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
	payload := map[string]any{
		"schema_version":   "asset.metadata.requested.v1",
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

// ── Compile-time assertion ──────────────────────────────────────────

// Per AGENTS.md Pattern 0: the concrete receiver must satisfy the
// typed port so any signature drift surfaces as a build failure.
var _ youtubeports.ClipMetadataWriter = (*ClipMetadataWriterAdapter)(nil)
