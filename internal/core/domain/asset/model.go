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
// dedicated sub-tables (asset_locations, asset_processing) and are
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
	// ExternalURL removed — use ExternalURL() getter which delegates to SourceURL

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

// GetMetadataInt retrieves an int from the Metadata map.
func (m *MediaAsset) GetMetadataInt(key string) int {
	if m.Metadata == nil {
		return 0
	}
	v, ok := m.Metadata[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case json.Number:
		i, _ := val.Int64()
		return int(i)
	default:
		return 0
	}
}

// SetMetadataInt sets an int value in the Metadata map.
func (m *MediaAsset) SetMetadataInt(key string, value int) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = value
}

// ── Typed getters/setters for deprecated struct fields ────────────────
// These replace the former struct fields (DriveFileID, DriveLink, etc.)
// with type-safe accessors backed by the Metadata map.

// ExternalURL returns the external URL (delegates to SourceURL).
func (m *MediaAsset) ExternalURL() string { return m.SourceURL }

// SetExternalURL sets the external URL (delegates to SourceURL).
func (m *MediaAsset) SetExternalURL(v string) { m.SourceURL = v }

// DriveFileID returns the Drive file ID from metadata.
func (m *MediaAsset) DriveFileID() string { return m.GetMetadataString("drive_file_id") }
func (m *MediaAsset) SetDriveFileID(v string) { m.SetMetadataString("drive_file_id", v) }

// DriveLink returns the Drive web link from metadata.
func (m *MediaAsset) DriveLink() string { return m.GetMetadataString("drive_link") }
func (m *MediaAsset) SetDriveLink(v string) { m.SetMetadataString("drive_link", v) }

// DownloadLink returns the download link from metadata.
func (m *MediaAsset) DownloadLink() string { return m.GetMetadataString("download_link") }
func (m *MediaAsset) SetDownloadLink(v string) { m.SetMetadataString("download_link", v) }

// LocalPath returns the local filesystem path from metadata.
func (m *MediaAsset) LocalPath() string { return m.GetMetadataString("local_path") }
func (m *MediaAsset) SetLocalPath(v string) { m.SetMetadataString("local_path", v) }

// FileHash returns the file hash from metadata.
func (m *MediaAsset) FileHash() string { return m.GetMetadataString("file_hash") }
func (m *MediaAsset) SetFileHash(v string) { m.SetMetadataString("file_hash", v) }

// FolderID returns the Drive folder ID from metadata.
func (m *MediaAsset) FolderID() string { return m.GetMetadataString("folder_id") }
func (m *MediaAsset) SetFolderID(v string) { m.SetMetadataString("folder_id", v) }

// FolderPath returns the folder path from metadata.
func (m *MediaAsset) FolderPath() string { return m.GetMetadataString("folder_path") }
func (m *MediaAsset) SetFolderPath(v string) { m.SetMetadataString("folder_path", v) }

// ParentFolderID returns the parent folder ID from metadata.
func (m *MediaAsset) ParentFolderID() string { return m.GetMetadataString("parent_folder_id") }
func (m *MediaAsset) SetParentFolderID(v string) { m.SetMetadataString("parent_folder_id", v) }

// Depth returns the tree depth from metadata.
func (m *MediaAsset) Depth() int { return m.GetMetadataInt("depth") }
func (m *MediaAsset) SetDepth(v int) { m.SetMetadataInt("depth", v) }

// IsFolder returns whether this is a folder node from metadata.
func (m *MediaAsset) IsFolder() bool {
	if m.Metadata == nil { return false }
	v, ok := m.Metadata["is_folder"]
	if !ok { return false }
	b, _ := v.(bool)
	return b
}
func (m *MediaAsset) SetIsFolder(v bool) {
	if m.Metadata == nil { m.Metadata = make(map[string]any) }
	m.Metadata["is_folder"] = v
}

// ChildCount returns the child count from metadata.
func (m *MediaAsset) ChildCount() int { return m.GetMetadataInt("child_count") }
func (m *MediaAsset) SetChildCount(v int) { m.SetMetadataInt("child_count", v) }

// SceneType returns the scene type from metadata.
func (m *MediaAsset) SceneType() string { return m.GetMetadataString("scene_type") }
func (m *MediaAsset) SetSceneType(v string) { m.SetMetadataString("scene_type", v) }

// QualityScore returns the quality score from metadata.
func (m *MediaAsset) QualityScore() float64 {
	if m.Metadata == nil { return 0 }
	v, ok := m.Metadata["quality_score"]
	if !ok { return 0 }
	switch val := v.(type) {
	case float64: return val
	case json.Number: f, _ := val.Float64(); return f
	default: return 0
	}
}
func (m *MediaAsset) SetQualityScore(v float64) {
	if m.Metadata == nil { m.Metadata = make(map[string]any) }
	m.Metadata["quality_score"] = v
}

// ReuseCount returns the reuse count from metadata.
func (m *MediaAsset) ReuseCount() int { return m.GetMetadataInt("reuse_count") }
func (m *MediaAsset) SetReuseCount(v int) { m.SetMetadataInt("reuse_count", v) }

// LastUsedAt returns the last-used timestamp from metadata.
func (m *MediaAsset) LastUsedAt() string { return m.GetMetadataString("last_used_at") }
func (m *MediaAsset) SetLastUsedAt(v string) { m.SetMetadataString("last_used_at", v) }

// UsableFor returns the usable-for tags from metadata.
func (m *MediaAsset) UsableFor() []string {
	if m.Metadata == nil { return nil }
	v, ok := m.Metadata["usable_for"]
	if !ok { return nil }
	switch arr := v.(type) {
	case []string: return arr
	case []interface{}:
		result := make([]string, len(arr))
		for i, item := range arr {
			if s, ok := item.(string); ok { result[i] = s }
		}
		return result
	}
	return nil
}
func (m *MediaAsset) SetUsableFor(v []string) {
	if m.Metadata == nil { m.Metadata = make(map[string]any) }
	m.Metadata["usable_for"] = v
}

// AvoidFor returns the avoid-for tags from metadata.
func (m *MediaAsset) AvoidFor() []string {
	if m.Metadata == nil { return nil }
	v, ok := m.Metadata["avoid_for"]
	if !ok { return nil }
	switch arr := v.(type) {
	case []string: return arr
	case []interface{}:
		result := make([]string, len(arr))
		for i, item := range arr {
			if s, ok := item.(string); ok { result[i] = s }
		}
		return result
	}
	return nil
}
func (m *MediaAsset) SetAvoidFor(v []string) {
	if m.Metadata == nil { m.Metadata = make(map[string]any) }
	m.Metadata["avoid_for"] = v
}

// PHash returns the perceptual hash from metadata.
func (m *MediaAsset) PHash() string { return m.GetMetadataString("phash") }
func (m *MediaAsset) SetPHash(v string) { m.SetMetadataString("phash", v) }

// EmbeddingJSON returns the embedding JSON from metadata.
func (m *MediaAsset) EmbeddingJSON() string { return m.GetMetadataString("embedding_json") }
func (m *MediaAsset) SetEmbeddingJSON(v string) { m.SetMetadataString("embedding_json", v) }

// VisualEmbedding returns the visual embedding from metadata.
func (m *MediaAsset) VisualEmbedding() string { return m.GetMetadataString("visual_embedding") }
func (m *MediaAsset) SetVisualEmbedding(v string) { m.SetMetadataString("visual_embedding", v) }

// TranscriptEmbedding returns the transcript embedding from metadata.
func (m *MediaAsset) TranscriptEmbedding() string { return m.GetMetadataString("transcript_embedding") }
func (m *MediaAsset) SetTranscriptEmbedding(v string) { m.SetMetadataString("transcript_embedding", v) }

// VisualEmbeddingJSON returns the visual embedding JSON from metadata.
func (m *MediaAsset) VisualEmbeddingJSON() string { return m.GetMetadataString("visual_embedding_json") }
func (m *MediaAsset) SetVisualEmbeddingJSON(v string) { m.SetMetadataString("visual_embedding_json", v) }
