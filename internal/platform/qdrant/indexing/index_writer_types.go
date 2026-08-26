package indexing

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
)

// ── Asset data types for the mapper ──────────────────────────────────

// TranscriptTrack is one language slice of a clip's multilingual
// transcript. PR-CATALOG-MULTILINGUA step 6 (July 2026): AssetData
// carries a slice of these so the canonical search_document composer
// can emit original-language text bare first and each subsequent
// language as `transcript ({lang}): {text}` — all in ONE Qdrant
// embedding_text (no per-language Qdrant point fanout at v1).
//
// godlike/06 SSOT: this type lives here (alongside AssetData) because
// it is the airlock's canonical input shape; the domain type
// (internal/domain/asset.TextTrackResolvedBundle) is the SSOT for the
// ROW shape, but the SLICE projection needed by the composer is a
// distinct concern and this file is the canonical owner.
//
// IsOriginal is computed at SQL-fetch time (AssetStore) by comparing
// `language_code = media_assets.language` (the canonical
// post-migration-152 original-language signal). The composer reads
// ONLY IsOriginal for the bare-text slot; the Lang/Text carries every
// row so the skip-language rows assemble into `transcript ({lang}): …`
// sequels in deterministic order (Lang-ASC tiebreaker).
type TranscriptTrack struct {
	Lang       string // BCP-47 language code, e.g. "en", "it", "es", "pt-BR"
	Text       string // verbatim row text
	IsOriginal bool   // true when this row's Lang matches the clip's original language
}

// AssetData is the canonical asset representation used by PayloadMapper.
// It mirrors the media_assets table columns needed for Qdrant points.
type AssetData struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Source          string `json:"source"`
	MediaType       string `json:"media_type"`
	AssetRole       string `json:"asset_role,omitempty"`
	NormalizedGroup string `json:"normalized_group,omitempty"`
	// Canonical taxonomy dimensions (migration 195). These are the SSOT
	// classification values written by the MediaCommitter; the payload
	// mapper emits them under the canonical payload keys namespace /
	// asset_kind / source_type / semantic_role. Empty for legacy rows
	// that predate the taxonomy columns (keys omitted fail-closed).
	Namespace    string `json:"namespace,omitempty"`
	AssetKind    string `json:"asset_kind,omitempty"`
	SourceType   string `json:"source_type,omitempty"`
	SemanticRole string `json:"semantic_role,omitempty"`
	HasDialogue  *bool  `json:"has_dialogue,omitempty"`
	AudioProfile string `json:"audio_profile,omitempty"`
	Status       string `json:"status"`
	// LifecycleState is the canonical search-filter payload key. Asset
	// store populates from media_assets.lifecycle_state when the column
	// exists; legacy rows fall back to Status-derived values so the
	// search adapter's filter key (lifecycle_state) is never empty.
	// See payload_mapper.canonicalLifecycleState for the prefer/fall-back
	// hierarchy used at write time.
	LifecycleState  string   `json:"lifecycle_state,omitempty"`
	Language        string   `json:"language,omitempty"`
	Category        string   `json:"category,omitempty"`
	Style           string   `json:"style,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	SearchText      string   `json:"search_text,omitempty"`
	SemanticSummary string   `json:"semantic_summary,omitempty"`
	// DriveFileID is the canonical Drive identity retained for projection
	// coherence and repair diagnostics. It is never exposed by API DTOs.
	DriveFileID string `json:"drive_file_id,omitempty"`
	// DriveLink is the canonical Drive web-view URL for the asset
	// (e.g. "https://drive.google.com/file/d/abc123/view").
	// PR-CATALOG-MULTILINGUA step 6 (July 2026): now EMITTED in the
	// Qdrant payload as the `drive_link` payload key so search results
	// can offer an open-in-Drive affordance without an extra signed-URL
	// round-trip (the legacy QDRANT-001 rule is REPLACED).
	// Forward-prevention: drive_link belongs ONLY in payload, NEVER in
	// embedding_text (pinned by payload_builder_test.go).
	DriveLink string `json:"drive_link,omitempty"`
	// FolderPath is the canonical Drive folder path / logical folder
	// label for the asset (e.g. "Manny Pacquiao vs Adrien Broner").
	// Emitted in the Qdrant payload as `folder_path` for folder-aware
	// filtering/navigation.
	FolderPath string `json:"folder_path,omitempty"`
	// FolderID is the canonical Drive folder ID for the asset's
	// destination folder. Emitted in the Qdrant payload as `folder_id`
	// so search and repair flows can recover the exact Drive target.
	FolderID string `json:"folder_id,omitempty"`
	// LocalPath is the absolute filesystem path for non-Qdrant
	// legacy callers. QDRANT-001 (June 2026): intentionally NOT
	// emitted by payload_mapper.BuildPayload — the canonical search
	// index is locator-free. Populated by asset_store.go from
	// media_assets.local_path for ingest-time tracking only; never
	// shipped to the vector index. NOTE: future readers, please do
	// NOT remove this field on a cleanup pass; it is required by
	// `internal/application/{assets|clips}/ingest/*.go` flow
	// diagnostics, and removing it would silently break ingest
	// crash-trace logs.
	LocalPath      string `json:"local_path,omitempty"`
	YouTubeVideoID string `json:"youtube_video_id,omitempty"`
	YouTubeURL     string `json:"youtube_url,omitempty"`
	StartTime      string `json:"start_time,omitempty"`
	EndTime        string `json:"end_time,omitempty"`
	DurationMs     int64  `json:"duration_ms,omitempty"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	ChannelID      string `json:"channel_id,omitempty"`
	License        string `json:"license,omitempty"`
	IndexVersion   string `json:"index_version,omitempty"`
	SourceVersion  string `json:"source_version,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	DeletedAt      string `json:"deleted_at,omitempty"`

	// PR 6 (July 2026, refactor/assetdata-semantic-fields) — first-class
	// semantic metadata fields. Previously accessible only via MetadataJSON;
	// the PayloadMapper airlock now reads these top-level fields FIRST
	// (fall-back to MetadataJSON for backward compat with old callers that
	// haven't yet migrated to top-level). 19 new fields:
	// semantic block (Title/Description/Summary/SourceURL/SourceVideoID/
	// SourceProvider/Origin/Destination) + LLM enrichment block (Event/
	// Round/Scene/Subject/Entities) + workflow/provenance block
	// (WorkflowID/RunFingerprint/ChunkIndex/TotalChunks/PolicyVersion/JobID).
	Title          string         `json:"title,omitempty"`
	Description    string         `json:"description,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	SourceURL      string         `json:"source_url,omitempty"`
	SourceVideoID  string         `json:"source_video_id,omitempty"`
	SourceProvider string         `json:"source_provider,omitempty"`
	Origin         string         `json:"origin,omitempty"`
	Destination    string         `json:"destination,omitempty"`
	Event          string         `json:"event,omitempty"`
	Round          int            `json:"round,omitempty"`
	Scene          string         `json:"scene,omitempty"`
	Subject        string         `json:"subject,omitempty"`
	Entities       []string       `json:"entities,omitempty"`
	WorkflowID     string         `json:"workflow_id,omitempty"`
	RunFingerprint string         `json:"run_fingerprint,omitempty"`
	ChunkIndex     int            `json:"chunk_index,omitempty"`
	TotalChunks    int            `json:"total_chunks,omitempty"`
	PolicyVersion  string         `json:"policy_version,omitempty"`
	JobID          string         `json:"job_id,omitempty"`
	MetadataJSON   string         `json:"-"`
	Metadata       map[string]any `json:"-"`
	// Embeddings are populated by the mapper from DB columns.
	TextVector       []float32 `json:"-"`
	TranscriptVector []float32 `json:"-"`
	VisualVector     []float32 `json:"-"`
	AudioVector      []float32 `json:"-"`
	ContentHash      string    `json:"content_hash,omitempty"`
	// SemanticHash is the canonical projection of
	// media_assets.semantic_hash (added by migration 152) — the
	// SHA-256 fingerprint of the semantic block. Distinct from
	// ContentHash (which is byte-level content fingerprint). The
	// airlock wires this into IndexedMetadata.CurrentSemanticHash
	// which is emitted as payload key `current_semantic_hash`. Empty
	// (and the payload key is omitted) when the underlying row has
	// no semantic_hash yet. PR-CATALOG-MULTILINGUA step 6 (July
	// 2026). The asset_store reads
	// `SELECT semantic_hash FROM media_assets WHERE id = ?` to
	// populate this.
	SemanticHash string `json:"semantic_hash,omitempty"`
	// Transcripts is the multilingual transcript slice populated by
	// AssetStore from asset_text_tracks rows where text_kind='transcript'
	// AND is_current=1 (PR-CATALOG-MULTILINGUA step 6, July 2026).
	// The first slice entry whose IsOriginal == true is rendered as
	// bare text in embedding_text; every other entry is rendered as
	// `transcript ({Lang}): {Text}` on a new line. Empty when no
	// is_current=1 transcript rows exist. godlike/07
	// NO-FAKE-AVAILABILITY: nil vs empty-slice are equivalent ("no
	// transcript available yet").
	Transcripts []TranscriptTrack `json:"transcripts,omitempty"`
	// PR-TIMESTAMP-FOLDER-LINK (July 2026): parent timestamp Drive
	// folder metadata. Propagated from ChunkState → metadata.json →
	// AssetData → Qdrant payload. Per-run scalar (all chunks in the
	// same timestamp block share the same parent folder).
	TimestampDriveFolderLink string `json:"timestamp_drive_folder_link,omitempty"`
	TimestampFolderID        string `json:"timestamp_folder_id,omitempty"`

	// ── VLM visual summary block (FASE-9 + visual-summary reindex) ────
	// Populated by the visual-summary reindex service
	// (internal/application/indexing/visual_summary.go) at
	// cmd/admin/reindex_visual_summary.go time. Six fields map 1:1 to
	// IndexedMetadata; the airlock in index_airlock.go copies them
	// verbatim with omitempty on the wire. All six are required for
	// the godlike/06 SSOT "version the projection with preprocessing
	// + model versions" rule; a row missing these fields surfaces
	// the "no VLM pass yet" sentinel and is OMITTED from the
	// payload entirely (godlike/07 NO-FAKE-AVAILABILITY).
	VisualSummary              string   `json:"visual_summary,omitempty"`
	VisibleActions             []string `json:"visible_actions,omitempty"`
	VisibleEntities            []string `json:"visible_entities,omitempty"`
	VisualPreprocessingVersion string   `json:"visual_preprocessing_version,omitempty"`
	VisualModelName            string   `json:"visual_model_name,omitempty"`
	VisualModelVersion         string   `json:"visual_model_version,omitempty"`
}

// AssetStore is the interface the PayloadMapper needs to fetch asset data.
type AssetStore interface {
	FetchAsset(ctx context.Context, assetID string) (*AssetData, error)
	ListAllAssetIDs(ctx context.Context) ([]string, error)
	// FetchAssetBatch returns a page of assets where id > afterID,
	// ordered by id ASC, limited to limit rows. Returns an empty
	// slice when no more rows exist. Used by ReindexAll for cursor-
	// based paginated scanning instead of loading all IDs into memory
	// and N+1 FetchAsset calls (HIGH #8, July 2026).
	FetchAssetBatch(ctx context.Context, afterID string, limit int) ([]*AssetData, error)
}
