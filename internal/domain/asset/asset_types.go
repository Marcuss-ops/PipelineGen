package asset

import (
	"encoding/json"
	"time"
)

// Source identifies where an asset originated.
type Source string

// MediaType classifies the content type of an asset. The canonical type
// declaration and const set live in media_type.go; this comment block is
// left here only as a forward pointer for readers scanning asset_types.go
// for the Asset struct. See media_type.go for the full history and
// rationale (Phase 1 local decl → Phase 3 alias of media.MediaType →
// Wave-14 native decl after internal/domain/media is deleted).

// Metadata is an open-ended key-value store for asset properties
// that don't have dedicated columns.
type Metadata map[string]any

// LifecycleState tracks where an asset is in its lifecycle.
type LifecycleState string

const (
	StateStaging    LifecycleState = "STAGING"
	StateProcessing LifecycleState = "PROCESSING"
	StateActive     LifecycleState = "ACTIVE"
	StateDeleted    LifecycleState = "DELETED"

	// Legacy compatibility values.
	StateReady   LifecycleState = "ready"
	StatePending LifecycleState = "pending"
)

// Valid returns true if s is a known lifecycle state.
func (s LifecycleState) Valid() bool {
	switch s {
	case StateStaging, StateProcessing, StateActive, StateDeleted, StateReady, StatePending:
		return true
	}
	return false
}

// Asset is the canonical domain model for a media asset in PipelineGen.
//
// Extended properties (drive IDs, paths, quality scores, embeddings, etc.)
// are stored in the Metadata map and accessed via typed getter/setter
// methods. This keeps the core struct stable while allowing schema evolution.
type Asset struct {
	ID             string         `json:"id"`
	Source         Source         `json:"source"`
	Name           string         `json:"name"`
	Filename       string         `json:"filename"`
	MediaType      MediaType      `json:"media_type"`
	Category       string         `json:"category"`
	Group          string         `json:"group"`
	SourceURL      string         `json:"source_url"`
	ClipPageURL    string         `json:"clip_page_url"`
	ThumbnailURL   string         `json:"thumbnail_url"`
	Duration       time.Duration  `json:"duration"`
	Tags           []string       `json:"tags"`
	SearchTerms    []string       `json:"search_terms"`
	SearchText     string         `json:"search_text"`
	LifecycleState LifecycleState `json:"lifecycle_state"`
	Metadata       Metadata       `json:"metadata"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      *time.Time     `json:"deleted_at,omitempty"`
}

// ── Metadata helpers ────────────────────────────────────────────────

// MetadataJSON returns the Metadata map serialized as a JSON string.
// Returns "{}" if Metadata is nil or empty.
func (m *Asset) MetadataJSON() string {
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
func (m *Asset) SetMetadataJSON(jsonStr string) {
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
func (m *Asset) GetMetadataString(key string) string {
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
func (m *Asset) SetMetadataString(key, value string) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = value
}

// GetMetadataInt retrieves an int from the Metadata map.
func (m *Asset) GetMetadataInt(key string) int {
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
func (m *Asset) SetMetadataInt(key string, value int) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = value
}

// ── Typed accessors (domain-level properties stored in Metadata) ────

func (m *Asset) ExternalURL() string        { return m.SourceURL }
func (m *Asset) SetExternalURL(v string)    { m.SourceURL = v }
func (m *Asset) DriveFileID() string        { return m.GetMetadataString("drive_file_id") }
func (m *Asset) SetDriveFileID(v string)    { m.SetMetadataString("drive_file_id", v) }
func (m *Asset) DriveLink() string          { return m.GetMetadataString("drive_link") }
func (m *Asset) SetDriveLink(v string)      { m.SetMetadataString("drive_link", v) }
func (m *Asset) DownloadLink() string       { return m.GetMetadataString("download_link") }
func (m *Asset) SetDownloadLink(v string)   { m.SetMetadataString("download_link", v) }
func (m *Asset) LocalPath() string          { return m.GetMetadataString("local_path") }
func (m *Asset) SetLocalPath(v string)      { m.SetMetadataString("local_path", v) }
func (m *Asset) FileHash() string           { return m.GetMetadataString("file_hash") }
func (m *Asset) SetFileHash(v string)       { m.SetMetadataString("file_hash", v) }
func (m *Asset) FolderID() string           { return m.GetMetadataString("folder_id") }
func (m *Asset) SetFolderID(v string)       { m.SetMetadataString("folder_id", v) }
func (m *Asset) FolderPath() string         { return m.GetMetadataString("folder_path") }
func (m *Asset) SetFolderPath(v string)     { m.SetMetadataString("folder_path", v) }
func (m *Asset) ParentFolderID() string     { return m.GetMetadataString("parent_folder_id") }
func (m *Asset) SetParentFolderID(v string) { m.SetMetadataString("parent_folder_id", v) }
func (m *Asset) Depth() int                 { return m.GetMetadataInt("depth") }
func (m *Asset) SetDepth(v int)             { m.SetMetadataInt("depth", v) }

func (m *Asset) IsFolder() bool {
	if m.Metadata == nil {
		return false
	}
	v, ok := m.Metadata["is_folder"]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func (m *Asset) SetIsFolder(v bool) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata["is_folder"] = v
}

func (m *Asset) ChildCount() int       { return m.GetMetadataInt("child_count") }
func (m *Asset) SetChildCount(v int)   { m.SetMetadataInt("child_count", v) }
func (m *Asset) SceneType() string     { return m.GetMetadataString("scene_type") }
func (m *Asset) SetSceneType(v string) { m.SetMetadataString("scene_type", v) }

func (m *Asset) QualityScore() float64 {
	if m.Metadata == nil {
		return 0
	}
	v, ok := m.Metadata["quality_score"]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return 0
	}
}

func (m *Asset) SetQualityScore(v float64) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata["quality_score"] = v
}

func (m *Asset) ReuseCount() int        { return m.GetMetadataInt("reuse_count") }
func (m *Asset) SetReuseCount(v int)    { m.SetMetadataInt("reuse_count", v) }
func (m *Asset) LastUsedAt() string     { return m.GetMetadataString("last_used_at") }
func (m *Asset) SetLastUsedAt(v string) { m.SetMetadataString("last_used_at", v) }

func (m *Asset) UsableFor() []string {
	if m.Metadata == nil {
		return nil
	}
	v, ok := m.Metadata["usable_for"]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []interface{}:
		result := make([]string, len(arr))
		for i, item := range arr {
			if s, ok := item.(string); ok {
				result[i] = s
			}
		}
		return result
	}
	return nil
}

func (m *Asset) SetUsableFor(v []string) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata["usable_for"] = v
}

func (m *Asset) AvoidFor() []string {
	if m.Metadata == nil {
		return nil
	}
	v, ok := m.Metadata["avoid_for"]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []interface{}:
		result := make([]string, len(arr))
		for i, item := range arr {
			if s, ok := item.(string); ok {
				result[i] = s
			}
		}
		return result
	}
	return nil
}

func (m *Asset) SetAvoidFor(v []string) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata["avoid_for"] = v
}

func (m *Asset) PHash() string                   { return m.GetMetadataString("phash") }
func (m *Asset) SetPHash(v string)               { m.SetMetadataString("phash", v) }
func (m *Asset) EmbeddingJSON() string           { return m.GetMetadataString("embedding_json") }
func (m *Asset) SetEmbeddingJSON(v string)       { m.SetMetadataString("embedding_json", v) }
func (m *Asset) VisualEmbedding() string         { return m.GetMetadataString("visual_embedding") }
func (m *Asset) SetVisualEmbedding(v string)     { m.SetMetadataString("visual_embedding", v) }
func (m *Asset) TranscriptEmbedding() string     { return m.GetMetadataString("transcript_embedding") }
func (m *Asset) SetTranscriptEmbedding(v string) { m.SetMetadataString("transcript_embedding", v) }
func (m *Asset) VisualEmbeddingJSON() string     { return m.GetMetadataString("visual_embedding_json") }
func (m *Asset) SetVisualEmbeddingJSON(v string) { m.SetMetadataString("visual_embedding_json", v) }
