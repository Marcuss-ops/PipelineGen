package asset

import (
	"encoding/json"
)

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
	case []any:
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
	case []any:
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
func (m *Asset) SetVisualEmbeddingJSON(v string) { m.SetMetadataString("visual_embedding_json", v) } // Source-specific tag accessors.
// These read from and write to the typed struct fields. The fields are
// mirrored to metadata_json by SyncTagFieldsToMetadata before persistence.
func (m *Asset) GetProviderTags() []string    { return m.ProviderTags }
func (m *Asset) SetProviderTags(v []string)   { m.ProviderTags = v }
func (m *Asset) GetVLMTags() []string         { return m.VLMTags }
func (m *Asset) SetVLMTags(v []string)        { m.VLMTags = v }
func (m *Asset) GetManualTags() []string      { return m.ManualTags }
func (m *Asset) SetManualTags(v []string)     { m.ManualTags = v }
func (m *Asset) GetTranscriptTags() []string  { return m.TranscriptTags }
func (m *Asset) SetTranscriptTags(v []string) { m.TranscriptTags = v }
