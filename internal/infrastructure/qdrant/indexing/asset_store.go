// Package indexing — asset_store.go owns the SQLiteAssetStore
// concrete + the shared row-scanner + the canonical SELECT (the
// "scan infrastructure" shared by the single and batch fetch flows
// in the sibling files).
//
// Split rationale (operation-flow):
//
//   - asset_store.go              : THIS FILE. SQLiteAssetStore
//     struct + ctor + compile-time
//     AssetStore pin + the
//     assetRowScanner (scanArgs +
//     populate) + canonicalQuery
//
//   - the shared maxTranscriptsPer
//     Asset cap. ~410 LOC (was 556
//     in the pre-split monolith).
//
//   - asset_store_fetch.go        : SINGLE-ASSET flow — FetchAsset
//
//   - ListAllAssetIDs +
//     populateTranscripts. ~110 LOC.
//
//   - asset_store_batch.go        : CURSOR PAGINATION flow —
//     FetchAssetBatch +
//     populateTranscriptsBatch.
//     ~150 LOC.
//
//   - asset_store_reconcile.go    : RECONCILE DRY-RUN —
//     ListAssetsForReconcile (minimum
//     payload for the admin
//     reconcile_qdrant.go CLI).
//     ~90 LOC.
//
// WHY THIS SPLIT (NOT entity-family / clip|voiceover|image):
//   - SQLiteAssetStore methods do NOT discriminate on
//     media_assets.media_type (no "clip-only fetch" /
//     "voiceover-only fetch"). FetchAsset / FetchAssetBatch /
//     ListAllAssetIDs / ListAssetsForReconcile all treat the
//     media_assets row uniformly — entity-family is a column,
//     not an axis of behavior.
//   - AssetData (in index_writer_types.go) is an entity-agnostic
//     projection; it carries ALL fields per row, including a
//     MediaType discriminator field, but the readers do NOT branch
//     on it.
//   - Splitting literally by clip|voiceover|image would yield
//     empty sibling files (no code differs per family). The
//     operation-flow split above has one axis of change per file
//     and matches the actual code structure.
//
// godlike/06 SSOT: this file owns the SQL column-vs-pointer mapping
// (the assetRowScanner and canonicalQuery). The column ORDER is
// canonical; FetchAsset (single row) and FetchAssetBatch (cursor
// loop) call scanArgs → Scan → populate in the exact same order.
// Any new column MUST be threaded through both the SELECT block and
// the assetRowScanner struct + scanArgs + populate IN SYNC (out-of-
// sync would silently misbind lifecycle_state / semantic_hash /
// embedding vectors).
//
// godlike/07: this file's struct + ctor are the only qdrant-package
// seam that touches database/sql + encoding/json. All sibling files
// in the same package reach the persistence layer through the
// SQLiteAssetStore struct field (s.db), never directly.
package indexing

import (
	"database/sql"
	"encoding/json"
)

// indexableAssetWhereClause is the canonical SQLite predicate for rows that
// can be reconstructed into Qdrant points. Eligibility is taxonomy-derived:
// registered audio/document/text assets are not semantic-searchable, and
// legacy media_type=clip rows are excluded until the registry backfill
// normalizes them to media_type=video, asset_kind=clip.
//
// ReindexAll and verifier_counts must stay aligned on the same eligibility
// boundary, so this clause also excludes soft-deleted rows and rows without
// a populated text embedding. Legacy rows with embedding_json = '[]' or '{}'
// are treated the same as empty.
const indexableAssetWhereClause = `
	((media_type = 'video' AND asset_kind IN ('clip','stock_video','generated_video','rendered_video'))
	 OR (media_type = 'image' AND asset_kind IN ('stock_image','web_image','ai_image','graphic')))
	AND COALESCE(namespace, '') != ''
	AND COALESCE(source_type, '') != ''
	AND (deleted_at IS NULL OR deleted_at = '')
	AND COALESCE(embedding_json, '') NOT IN ('', '[]', '{}')
`

// SQLiteAssetStore implements AssetStore backed by the media_assets table.
type SQLiteAssetStore struct {
	db *sql.DB
}

// NewSQLiteAssetStore creates an AssetStore bound to a *sql.DB.
// The db must have the media_assets table with the canonical QDRANT-003 schema.
func NewSQLiteAssetStore(db *sql.DB) *SQLiteAssetStore {
	return &SQLiteAssetStore{db: db}
}

// Compile-time assertion: SQLiteAssetStore satisfies AssetStore.
var _ AssetStore = (*SQLiteAssetStore)(nil)

// ── Shared row scanner ───────────────────────────────────────────────
//
// assetRowScanner bundles every sql.Null* variable needed to scan a
// media_assets row. Both FetchAsset (single row via QueryRowContext)
// and FetchAssetBatch (cursor-based loop via QueryContext) declare the
// same scanner variables and call the same Scan pointer list. The
// populate method extracts the post-Scan parsing logic (tags JSON,
// metadata JSON, embedding vectors, optional fields) so the two
// methods share one canonical parser.
//
// godlike/06 SSOT mapping (asset_store.go is the canonical owner):
//   - The field ORDER in assetRowScanner is informational only; the
//     canonical ORDER is the scanArgs pointer list below. Consumers
//     MUST go through Scan(args...) with the pointer list, never
//     rely on field layout.

type assetRowScanner struct {
	tagsJSON, metaJSON, textEmbJSON, transcriptEmbJSON, visualEmbJSON, audioEmbJSON sql.NullString
	durationMs                                                                      sql.NullInt64
	sourceVersionStr                                                                sql.NullString
	language, category, style, channelID, lic                                       sql.NullString
	workspaceID, youtubeVideoID, youtubeURL, startTime, endTime, folderID           sql.NullString
	createdAt, updatedAt, deletedAt                                                 sql.NullString
	lifecycleState                                                                  sql.NullString
	// semanticHash is the resolved semantic fingerprint:
	//   asset_visual_summaries.source_hash (when a real VLM pass has run)
	//   ∪ media_assets.semantic_hash (fallback)
	// godlike/06 SSOT precedence — see canonicalQuery's resolved_semantic_hash
	// subquery for the canonical pre-resolve at SQL-fetch time.
	resolvedSemanticHash sql.NullString
}

// scanArgs returns the pointer list passed to rows.Scan / QueryRowContext.Scan.
// Order MUST match the SELECT column order in the canonical query below.
func (r *assetRowScanner) scanArgs(a *AssetData) []any {
	return []any{
		&a.ID, &a.Name, &a.Source, &a.MediaType,
		&a.Namespace, &a.AssetKind, &a.SourceType, &a.SemanticRole,
		&r.lifecycleState,
		&r.tagsJSON,
		&a.SearchText,
		&a.DriveFileID,
		&a.DriveLink,
		&a.LocalPath,
		&a.FolderID,
		&a.FolderPath,
		&r.metaJSON,
		&r.textEmbJSON,
		&r.transcriptEmbJSON,
		&r.visualEmbJSON,
		&r.audioEmbJSON,
		&r.language, &r.category, &r.style,
		&r.youtubeVideoID, &r.youtubeURL,
		&r.startTime, &r.endTime,
		&r.durationMs,
		&r.workspaceID, &r.channelID, &r.lic,
		&r.sourceVersionStr,
		&r.createdAt, &r.updatedAt, &r.deletedAt,
		&r.resolvedSemanticHash,
	}
}

// populate fills the derived fields on a from the scanned row values.
// Call AFTER a successful Scan.
func (r *assetRowScanner) populate(a *AssetData) {
	if r.lifecycleState.Valid {
		a.LifecycleState = r.lifecycleState.String
	}

	// Parse tags JSON.
	if r.tagsJSON.Valid && r.tagsJSON.String != "" && r.tagsJSON.String != "[]" {
		json.Unmarshal([]byte(r.tagsJSON.String), &a.Tags)
	}

	// Parse metadata JSON.
	a.MetadataJSON = r.metaJSON.String
	if a.MetadataJSON != "" && a.MetadataJSON != "{}" {
		var m map[string]any
		if err := json.Unmarshal([]byte(a.MetadataJSON), &m); err == nil {
			a.Metadata = m
		}
	}

	// Parse embedding vectors.
	if r.textEmbJSON.Valid && r.textEmbJSON.String != "" && r.textEmbJSON.String != "[]" && r.textEmbJSON.String != "{}" {
		json.Unmarshal([]byte(r.textEmbJSON.String), &a.TextVector)
	}
	if r.transcriptEmbJSON.Valid && r.transcriptEmbJSON.String != "" && r.transcriptEmbJSON.String != "[]" && r.transcriptEmbJSON.String != "{}" {
		json.Unmarshal([]byte(r.transcriptEmbJSON.String), &a.TranscriptVector)
	}
	if r.visualEmbJSON.Valid && r.visualEmbJSON.String != "" && r.visualEmbJSON.String != "[]" && r.visualEmbJSON.String != "{}" {
		json.Unmarshal([]byte(r.visualEmbJSON.String), &a.VisualVector)
	}
	if r.audioEmbJSON.Valid && r.audioEmbJSON.String != "" && r.audioEmbJSON.String != "[]" && r.audioEmbJSON.String != "{}" {
		json.Unmarshal([]byte(r.audioEmbJSON.String), &a.AudioVector)
	}

	// Optional fields.
	if r.language.Valid {
		a.Language = r.language.String
	}
	if r.category.Valid {
		a.Category = r.category.String
	}
	if r.style.Valid {
		a.Style = r.style.String
	}
	if r.channelID.Valid {
		a.ChannelID = r.channelID.String
	}
	if r.lic.Valid {
		a.License = r.lic.String
	}
	if r.youtubeVideoID.Valid {
		a.YouTubeVideoID = r.youtubeVideoID.String
	}
	if r.youtubeURL.Valid {
		a.YouTubeURL = r.youtubeURL.String
	}
	if r.folderID.Valid {
		a.FolderID = r.folderID.String
	}
	if r.startTime.Valid {
		a.StartTime = r.startTime.String
	}
	if r.endTime.Valid {
		a.EndTime = r.endTime.String
	}
	if r.durationMs.Valid {
		a.DurationMs = r.durationMs.Int64
	}
	if r.workspaceID.Valid {
		a.WorkspaceID = r.workspaceID.String
	}
	if r.sourceVersionStr.Valid {
		a.SourceVersion = r.sourceVersionStr.String
	}
	if r.createdAt.Valid {
		a.CreatedAt = r.createdAt.String
	}
	if r.updatedAt.Valid {
		a.UpdatedAt = r.updatedAt.String
	}
	if r.deletedAt.Valid {
		a.DeletedAt = r.deletedAt.String
	}
	// Resolved semantic hash (godlike/06 SSOT precedence pinned by the
	// canonicalQuery's scalar subquery — see comment above).
	if r.resolvedSemanticHash.Valid {
		a.SemanticHash = r.resolvedSemanticHash.String
	}
}

// canonicalQuery is the SELECT column list shared by FetchAsset and
// FetchAssetBatch. Column order MUST match assetRowScanner.scanArgs.
//
// godlike/06 SSOT: the trailing `resolved_semantic_hash` scalar
// subquery pins the canonical pre-resolved value of current_semantic_hash
// with the precedence rule dictated by the Italian plan / step 6:
//
//	(a) asset_visual_summaries.source_hash (migration 151) — when a
//	    real VLM pass has run. Per migration 151, the table is 1:1
//	    with media_assets.id (PRIMARY KEY) — no language_code or
//	    is_current columns — so the lookup reduces to the canonical
//	    PK query. NULLIF around the inner SELECT distinguishes
//	    "no row" / "row.source_hash is empty default" from
//	    "row.source_hash is a real VLM fingerprint".
//	(b) media_assets.semantic_hash (migration 152) — fallback
//	    (also empty-string gated by NULLIF for symmetry).
//	(c) "" — neither populated.
//
// The forward-prevention invariant: COALESCE falls through on NULL
// only — passing through an empty string ('""') is treated as a
// populated sentinel. Per migration 151, source_hash is
// `TEXT NOT NULL DEFAULT ”`; an early insert with no real VLM
// pass populates source_hash with the empty default. Without
// NULLIF, the empty ” would block the semantic_hash fallback and
// the airlock would emit an empty current_semantic_hash — a
// silent regression against the user-spec (b) precedence rule.
//
// The airlock in index_airlock.go just trusts AssetData.SemanticHash;
// it does NOT re-derive precedence. AssetStore is the canonical owner
// of the precedence resolution (godlike/06 ONE canonical owner per
// fact).
const canonicalQuery = `
		id, COALESCE(name, ''), COALESCE(source, ''), COALESCE(media_type, ''),
		COALESCE(namespace, ''), COALESCE(asset_kind, ''), COALESCE(source_type, ''), COALESCE(semantic_role, ''),
		COALESCE(lifecycle_state, 'ACTIVE'),
		COALESCE(tags, '[]'),
		COALESCE(search_text, ''),
		COALESCE(drive_file_id, ''),
		COALESCE(drive_link, ''),
		COALESCE(local_path, ''),
		COALESCE(folder_id, ''),
		COALESCE(folder_path, ''),
		COALESCE(metadata_json, '{}'),
		COALESCE(embedding_json, '[]'),
		COALESCE(transcript_embedding, '[]'),
		COALESCE(visual_embedding, '[]'),
		COALESCE(audio_embedding, '[]'),
		language, category, style,
		youtube_video_id, youtube_url,
		start_time, end_time,
		duration_ms,
		workspace_id, channel_id, license,
		source_version,
		created_at, updated_at, deleted_at,
		COALESCE(
			NULLIF(
				(SELECT source_hash FROM asset_visual_summaries
				 WHERE asset_id = media_assets.id
				 LIMIT 1),
				''
			),
			NULLIF(semantic_hash, ''),
			''
		) AS resolved_semantic_hash
	FROM media_assets`

// maxTranscriptsPerAsset caps the per-asset transcript-row count
// fetched by `populateTranscripts` (single-asset flow) AND enforced
// in code by `populateTranscriptsBatch` (batch flow). At 200
// languages per asset the slice is a generous load — typical
// multilingual catalogs sit at ≤20 langs; the cap is defensive.
//
// Single-fetch path (asset_store_fetch.go): SQL LIMIT ? enforces
// the cap server-side.
// Batch-fetch path (asset_store_batch.go): SQL IN-list doesn't
// carry LIMIT, so the cap is enforced in code per-asset.
//
// Lives in the facciata so both fetch flows share one canonical cap;
// bumping the cap (godlike/06 SSOT version bump) is a single edit
// here.
const maxTranscriptsPerAsset = 200
