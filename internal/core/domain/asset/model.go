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
// dedicated sub-tables and are accessed through their repository interfaces.
type MediaAsset struct {
	// ── Identity ──────────────────────────────────────────────────
	ID       string `json:"id"`
	Source   string `json:"source"`
	Name     string `json:"name"`
	Filename string `json:"filename"`

	// ── Classification ────────────────────────────────────────────
	MediaType string `json:"media_type"`
	Category  string `json:"category"`
	Group     string `json:"group"`

	// ── URLs ──────────────────────────────────────────────────────
	SourceURL    string `json:"source_url"`
	ClipPageURL  string `json:"clip_page_url"`
	ThumbnailURL string `json:"thumbnail_url"`
	ExternalURL  string `json:"external_url"` // transitional alias for SourceURL

	// ── Content ───────────────────────────────────────────────────
	DurationMs  int64    `json:"duration_ms"`
	Tags        []string `json:"tags"`
	SearchTerms []string `json:"search_terms"`
	SearchText  string   `json:"search_text"`

	// ── Lifecycle ─────────────────────────────────────────────────
	LifecycleState LifecycleState `json:"lifecycle_state"`
	DeletedAt      *time.Time     `json:"deleted_at,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`

	// ── Provider-specific metadata ────────────────────────────────
	// Canonical fields MUST NOT be stored here.
	Metadata map[string]any `json:"metadata"`

	// ── Remaining migration fields ────────────────────────────────
	// These fields are unrelated to physical storage and are removed in
	// later focused migrations. Location and processing fields are no longer
	// allowed on this entity.
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
}

// MetadataJSON returns the Metadata map serialized as a JSON string.
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
	value, ok := m.Metadata[key]
	if !ok {
		return ""
	}
	result, _ := value.(string)
	return result
}

// SetMetadataString sets a string value in the Metadata map.
func (m *MediaAsset) SetMetadataString(key, value string) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = value
}
