// internal/infrastructure/database/sqlite/assets/media_assets_discovery_repository.go
// ──────────────────────────────────────────────────────────────────────────────
// Per-video discovery state repository.
//
// Wave CONFORMANCE-001 / id-24 (June 2026). Replaces the deprecated
// MonitorsRepository (file removed by the CONTRACT phase; see
// migrations/sqlite/110_drop_monitored_sources.sql + git rm sequence).
//
// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX.
//
// Discovery rows are STUBS — they carry the external_id + URL + minimal
// metadata for a YouTube/Artlist/Drive source video but they DO NOT
// carry video files, transcripts, or embeddings. The Qdrant projection
// is therefore logically N/A for these stubs; the canonical
// `outbox.Dispatcher → media_assets.UPSERT → index_state transition`
// pathway is intentionally bypassed and the repository writes directly
// to `media_assets`. This is the documented exception to the
// QDRANT-002 invariant:
//
//   - `// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX` — the godoc
//     comment is the canonical marker; future readers grepping for
//     direct media_assets writers will see it.
//
// Discovery rows become full assets only after a `youtube.clip.extract`
// job hydrates them with embeddings + transcripts; at that point the
// normal outbox pathway takes over via the canonical mutations.
//
// See `architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md` (QDRANT
// projection rules) for the canonical projection contract.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// MediaAssetDiscoveryRepository owns the per-video discovery state on
// the `media_assets` table. Rows are identified by the partial natural
// key `(external_id, discovered_via)` where both are non-empty
// (canonical unique index `idx_media_assets_ext_discovered`,
// migration 109). Rows where either column is empty are LEGACY rows
// (pre-CUTOVER) and are NOT touched by this repository.
type MediaAssetDiscoveryRepository struct {
	db *sql.DB
}

// NewMediaAssetDiscoveryRepository constructs the discovery repository
// on the canonical SQLiteDB. The `db` arg is the shared
// `*storage.SQLiteDB.DB` handle from composition-root; pass `nil` to
// surface a typed-nil error during composition (per
// portutil.IsNilPort-style construction safety).
func NewMediaAssetDiscoveryRepository(db *sql.DB) *MediaAssetDiscoveryRepository {
	return &MediaAssetDiscoveryRepository{db: db}
}

// DB returns the underlying database connection (matches the
// `*assets.ClipsRepository.DB()` accessor used by the YouTube
// composition-root for caching).
func (r *MediaAssetDiscoveryRepository) DB() *sql.DB {
	return r.db
}

// UpsertByMonitoredSource upserts a discovery stash for a single
// external source video (YouTube video ID, Artlist asset, etc.).
// Returns errors uncached so callers can branch on errors.Is. The
// natural key is `(external_id, discovered_via)`; the ON CONFLICT
// clause targets the partial unique index from migration 109.
//
// ON CONFLICT predicate constraint: the ON CONFLICT WHERE clause MUST
// match the partial-index predicate for idempotency to survive future
// index extensions. The partial unique index from migration 109 is
// declared `WHERE external_id != ” AND discovered_via != ”`; the
// ON CONFLICT below repeats the same predicate verbatim. A future
// edit that adds a column to the partial index without updating the
// ON CONFLICT WHERE WILL silently break idempotency — keep them in
// sync.
//
// Mapping rules (canonical):
//   - id               ← src.ID (conventionally the video ID).
//   - source           ← src.Source ("youtube" / "artlist" / "drive";
//     enforced by the caller).
//   - external_id      ← src.ExternalID
//   - discovered_via   ← "monitor:monitored:" + src.ID (preserves
//     the provenance link to the
//     legacy monitored_sources row
//     during migration 110's BACKFILL
//     phase).
//   - discovered_at    ← src.LastSeenAt if non-empty, else RFC3339 now().
//   - monitored_source_id ← src.ID (legacy link; cleared by CONTRACT).
//   - url              ← src.ExternalURL
//   - name             ← src.Title
//   - tags             ← pipe-joined (Keyword, GroupName, Category)
//     plus a "discovery" sentinel so
//     the asset search path can pick
//     the pieces back.
//   - lifecycle_state  ← 'STAGING' (discovery stubs are not yet
//     fully hydrated).
//   - metadata_json    ← minimal snapshot of the remaining
//     MonitoredSource fields
//     (status, last_checked_at,
//     processed_count, etc.) so
//     legacy readers can pick them up
//     via json_extract.
//
// QDRANT-002: bypasses the outbox by design (see file header).
func (r *MediaAssetDiscoveryRepository) UpsertByMonitoredSource(ctx context.Context, src *asset.MonitoredSource) error {
	if src == nil {
		return nil
	}
	if src.ExternalID == "" {
		// Defensive guard: a discovery row without external_id is a
		// legacy read-back path; the partial unique index
		// (external_id, discovered_via) WHERE non-empty would reject
		// it anyway. Surfacing as a no-op keeps the call idempotent
		// during the migration 110 BACKFILL phase when some legacy
		// rows have empty external_id (which the per-row filter
		// below would also drop cleanly).
		return nil
	}
	now := timeutil.FormatRFC3339(time.Now())
	if src.LastSeenAt == "" {
		src.LastSeenAt = now
	}
	discoveredAt := src.LastSeenAt
	if discoveredAt == "" {
		discoveredAt = now
	}

	discoveredVia := "monitor:monitored:" + src.ID

	tags := src.Keyword
	if src.GroupName != "" {
		if tags != "" {
			tags = tags + "|" + src.GroupName
		} else {
			tags = src.GroupName
		}
	}
	if src.Category != "" {
		if tags != "" {
			tags = tags + "|" + src.Category
		} else {
			tags = src.Category
		}
	}
	if tags == "" {
		tags = "discovery"
	}

	snapshot := map[string]any{
		"status":            src.Status,
		"last_checked_at":   src.LastCheckedAt,
		"processed_count":   src.ProcessedCount,
		"channel_id":        src.ChannelID,
		"channel_url":       src.ChannelURL,
		"legacy_metadata":   src.MetadataJSON,
		"created_at_legacy": src.CreatedAt,
		"updated_at_legacy": src.UpdatedAt,
	}
	metaBytes, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	meta := string(metaBytes)

	_, err = r.db.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, external_id, discovered_via, discovered_at, monitored_source_id,
			name, url, tags, media_type, lifecycle_state, metadata_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'youtube_clip', 'STAGING', ?, ?, ?)
		ON CONFLICT(external_id, discovered_via) WHERE external_id != '' AND discovered_via != '' DO UPDATE SET
			id = excluded.id,
			source = excluded.source,
			discovered_at = excluded.discovered_at,
			name = excluded.name,
			url = excluded.url,
			tags = excluded.tags,
			metadata_json = excluded.metadata_json,
			updated_at = excluded.updated_at
	`, src.ID, src.Source, src.ExternalID, discoveredVia, discoveredAt, src.ID,
		src.Title, src.ExternalURL, tags, meta, now, now)
	return err
}

// IncrementProcessed bumps the per-source processed_count for the
// discovery row identified by the canonical media_assets PK.
//
// Contract: the `id` arg is the `media_assets.id` written by
// `UpsertByMonitoredSource` (which sets `id = src.ID`). The WHERE
// filters on `id = ?` (primary key) AND `discovered_via != ”` (filter
// out legacy rows where `discovered_via` is empty). Using PK + the
// non-empty marker is unambiguous and avoids the contract ambiguity
// flagged by the code-reviewer-minimax-m3 review of the initial draft
// (where `WHERE external_id = ?` would not have matched `ms.ID`
// callers from `manifest_mgr.go::updateMonitoredSourceStatus`).
//
// Stored under `metadata_json.processed_count` so the discovery-state
// writer doesn't depend on a separate counter column (the column
// footprint is minimal per migration 109).
//
// QDRANT-002: bypasses the outbox by design (see file header).
func (r *MediaAssetDiscoveryRepository) IncrementProcessed(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := r.db.ExecContext(ctx, `
		UPDATE media_assets
		SET metadata_json = json_set(
			COALESCE(metadata_json, '{}'),
			'$.processed_count',
			COALESCE(json_extract(metadata_json, '$.processed_count'), 0) + 1
		),
		updated_at = ?
		WHERE id = ?
		  AND discovered_via != ''
	`, now, id)
	return err
}
