// Package asset defines the canonical domain model for media assets.
//
// MediaAsset is the single source of truth for an asset's identity.
// Physical locations (local path, Drive, object storage) live in
// asset_locations. Processing state lives in asset_processing.
// Provider-specific metadata lives in the Metadata map.
//
// Services MUST depend on this package — never on internal/media/models.
package asset

import (
	"encoding/json"
	"time"
)

// MediaAsset is the canonical domain representation of a media_assets row.
//
// Fields map directly to typed columns in media_assets. Fields that are
// NOT here (local_path, drive_link, file_hash, status, error) belong in
// dedicated sub-tables (asset_locations, asset_processing) and must be
// accessed through the corresponding repository interfaces.
type MediaAsset struct {
	// ── Identity ──────────────────────────────────────────────────
	ID       string `json:"id"`
	Source   string `json:"source"`    // "youtube", "artlist", "stock", "image", "local"
	Name     string `json:"name"`
	Filename string `json:"filename"`

	// ── Classification ────────────────────────────────────────────
	MediaType string `json:"media_type"` // "video", "audio", "image"
	Category  string `json:"category"`   // "nature", "tech", "people", etc.
	Group     string `json:"group"`

	// ── URLs ──────────────────────────────────────────────────────
	SourceURL    string `json:"source_url"`    // original source URL
	ClipPageURL  string `json:"clip_page_url"` // Artlist clip page URL
	ThumbnailURL string `json:"thumbnail_url"` // thumbnail image URL
	ExternalURL  string `json:"external_url"`  // legacy alias for SourceURL

	// ── Content ───────────────────────────────────────────────────
	DurationMs  int64    `json:"duration_ms"`
	Tags        []string `json:"tags"`
	SearchTerms []string `json:"search_terms"` // queries that led to download
	SearchText  string   `json:"search_text"`  // concatenated searchable text

	// ── Lifecycle ─────────────────────────────────────────────────
	LifecycleState LifecycleState `json:"lifecycle_state"`
	DeletedAt      *time.Time     `json:"deleted_at,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`

	// ── Provider-specific metadata ────────────────────────────────
	// This map holds variable, provider-specific fields that don't
	// warrant a dedicated column (e.g. youtube_video_id, clean_title,
	// clip_summary, hook, topics, speakers, language, style).
	// Canonical fields MUST NOT be stored here — use typed columns.
	Metadata map[string]any `json:"metadata"`

	// ── Legacy fields (to be removed) ────────────────────────────
	// These fields exist in the struct temporarily for backward
	// compatibility during the migration. They will be removed from
	// the struct once all consumers are migrated.
	// DO NOT add new consumers of these fields.
	EmbeddingJSON       string   `json:"embedding_json,omitempty"`
	VisualEmbedding     string   `json:"visual_embedding,omitempty"`
	TranscriptEmbedding string   `json:"transcript_embedding,omitempty"`
	VisualEmbeddingJSON string   `json:"visual_embedding_json,omitempty"`
	FolderID            string   `json:"folder_id,omitempty"`
	ParentFolderID      string   `json:"parent_folder_id,omitempty"`
	FolderPath          string   `json:"folder_path,omitempty"`
	Depth               int      `json:"depth,omitempty"`
	IsFolder            bool     `json:"is_folder,omitempty"`
	SceneType           string   `json:"scene_type,omitempty"`
	QualityScore        float64  `json:"quality_score,omitempty"`
	ReuseCount          int      `json:"reuse_count,omitempty"`
	LastUsedAt          string   `json:"last_used_at,omitempty"`
	UsableFor           []string `json:"usable_for,omitempty"`
	AvoidFor            []string `json:"avoid_for,omitempty"`
	PHash               string   `json:"phash,omitempty"`
	ChildCount          int      `json:"child_count,omitempty"`

	// ── Deprecated fields (will be removed in PR12) ───────────────
	// These are kept only for struct-compat during the transition.
	// New code MUST NOT read or write them.
	Status      string `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	DownloadLink string `json:"download_link,omitempty"`
	LocalPath   string `json:"local_path,omitempty"`
	FileHash    string `json:"file_hash,omitempty"`
}

// MetadataJSON returns the Metadata map serialized as a JSON string.
// Returns "{}" if Metadata is nil or empty.
func (m *MediaAsset) MetadataJSON() string {
	if m.Metadata == nil {
		return "{}"
	}
	b, _ := json.Marshal(m.Metadata)
	if len(b) == 0 {
		return "{}"
	}
	return string(b)
}

// SetMetadataJSON parses a JSON string into the Metadata map.
func (m *MediaAsset) SetMetadataJSON(jsonStr string) {
	if jsonStr == "" || jsonStr == "{}" || jsonStr == "null" {
		m.Metadata = make(map[string]any)
		return
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &meta); err != nil {
		m.Metadata = make(map[string]any)
		return
	}
	m.Metadata = meta
}

// GetMetadataString retrieves a string value from the Metadata map.
func (m *MediaAsset) GetMetadataString(key string) string {
	if m.Metadata == nil {
		return ""
	}
	v, ok := m.Metadata[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// SetMetadataString sets a string value in the Metadata map.
func (m *MediaAsset) SetMetadataString(key, value string) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = value
}
