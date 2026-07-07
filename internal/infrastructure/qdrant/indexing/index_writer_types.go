package indexing

import (
	"context"
)

// ── Asset data types for the mapper ──────────────────────────────────

// AssetData is the canonical asset representation used by PayloadMapper.
// It mirrors the media_assets table columns needed for Qdrant points.
type AssetData struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	MediaType string `json:"media_type"`
	Status    string `json:"status"`
	// LifecycleState is the canonical search-filter payload key. Asset
	// store populates from media_assets.lifecycle_state when the column
	// exists; legacy rows fall back to Status-derived values so the
	// search adapter's filter key (lifecycle_state) is never empty.
	// See payload_mapper.canonicalLifecycleState for the prefer/fall-back
	// hierarchy used at write time.
	LifecycleState string   `json:"lifecycle_state,omitempty"`
	Language       string   `json:"language,omitempty"`
	Category       string   `json:"category,omitempty"`
	Style          string   `json:"style,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	SearchText     string   `json:"search_text,omitempty"`
	// DriveLink is the Drive web-view link for non-Qdrant legacy
	// callers. QDRANT-001 (June 2026): intentionally NOT emitted by
	// payload_mapper.BuildPayload — clients obtain a short-TTL
	// signed URL via delivery.Signer.BuildAuthorizedURL per asset.
	// Populated by asset_store.go from media_assets.drive_link for
	// ingest-path tracking / reconstruct-from-SQL flows; never
	// shipped to the vector index.
	DriveLink string `json:"drive_link,omitempty"`
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
	Title          string                 `json:"title,omitempty"`
	Description    string                 `json:"description,omitempty"`
	Summary        string                 `json:"summary,omitempty"`
	SourceURL      string                 `json:"source_url,omitempty"`
	SourceVideoID  string                 `json:"source_video_id,omitempty"`
	SourceProvider string                 `json:"source_provider,omitempty"`
	Origin         string                 `json:"origin,omitempty"`
	Destination    string                 `json:"destination,omitempty"`
	Event          string                 `json:"event,omitempty"`
	Round          int                    `json:"round,omitempty"`
	Scene          string                 `json:"scene,omitempty"`
	Subject        string                 `json:"subject,omitempty"`
	Entities       []string               `json:"entities,omitempty"`
	WorkflowID     string                 `json:"workflow_id,omitempty"`
	RunFingerprint string                 `json:"run_fingerprint,omitempty"`
	ChunkIndex     int                    `json:"chunk_index,omitempty"`
	TotalChunks    int                    `json:"total_chunks,omitempty"`
	PolicyVersion  string                 `json:"policy_version,omitempty"`
	JobID          string                 `json:"job_id,omitempty"`
	MetadataJSON   string                 `json:"-"`
	Metadata       map[string]interface{} `json:"-"`
	// Embeddings are populated by the mapper from DB columns.
	TextVector       []float32 `json:"-"`
	TranscriptVector []float32 `json:"-"`
	VisualVector     []float32 `json:"-"`
	AudioVector      []float32 `json:"-"`
	ContentHash      string    `json:"content_hash,omitempty"`
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
