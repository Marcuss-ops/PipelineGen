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

// setMetadataSlice/setMetadataBool/setMetadataFloat are nil-safe
// setters shared by the typed accessors below. Setters assign the raw
// typed value so the storage key alphabet is unchanged (godlike/07
// migration-window discipline: no data backfill required).
func setMetadataSlice(m *Asset, key string, v []string) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = v
}

func setMetadataBool(m *Asset, key string, v bool) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = v
}

func setMetadataFloat(m *Asset, key string, v float64) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[key] = v
}

// ── YouTube metadata accessors ───────────────────────────────────────────
// Bare-key migration window (godlike/07): youtube-layer call sites MUST use
// these typed accessors instead of GetMetadataString("youtube_*") literals
// so the archcheck percheck_metadata_registry bare-key-residue bucket stays
// empty for the youtube/autotag surface. Storage keys are unchanged.

func (m *Asset) YouTubeTitle() string           { return m.GetMetadataString("youtube_title") }
func (m *Asset) SetYouTubeTitle(v string)       { m.SetMetadataString("youtube_title", v) }
func (m *Asset) YouTubeDescription() string     { return m.GetMetadataString("youtube_description") }
func (m *Asset) SetYouTubeDescription(v string) { m.SetMetadataString("youtube_description", v) }
func (m *Asset) YouTubeLanguage() string        { return m.GetMetadataString("youtube_language") }
func (m *Asset) SetYouTubeLanguage(v string)    { m.SetMetadataString("youtube_language", v) }
func (m *Asset) YouTubeUploader() string        { return m.GetMetadataString("youtube_uploader") }
func (m *Asset) SetYouTubeUploader(v string)    { m.SetMetadataString("youtube_uploader", v) }
func (m *Asset) YouTubeUploadDate() string      { return m.GetMetadataString("youtube_upload_date") }
func (m *Asset) SetYouTubeUploadDate(v string)  { m.SetMetadataString("youtube_upload_date", v) }
func (m *Asset) YouTubeViewCount() string       { return m.GetMetadataString("youtube_view_count") }
func (m *Asset) SetYouTubeViewCount(v string)   { m.SetMetadataString("youtube_view_count", v) }
func (m *Asset) YouTubeDuration() string        { return m.GetMetadataString("youtube_duration") }
func (m *Asset) SetYouTubeDuration(v string)    { m.SetMetadataString("youtube_duration", v) }
func (m *Asset) YouTubeVideoID() string         { return m.GetMetadataString("youtube_video_id") }
func (m *Asset) SetYouTubeVideoID(v string)     { m.SetMetadataString("youtube_video_id", v) }
func (m *Asset) YouTubeURL() string             { return m.GetMetadataString("youtube_url") }
func (m *Asset) SetYouTubeURL(v string)         { m.SetMetadataString("youtube_url", v) }
func (m *Asset) YouTubeCategories() string      { return m.GetMetadataString("youtube_categories") }
func (m *Asset) SetYouTubeCategories(v string)  { m.SetMetadataString("youtube_categories", v) }
func (m *Asset) YouTubeTags() []string          { return MetadataStringSlice(m.Metadata, "youtube_tags") }
func (m *Asset) SetYouTubeTags(v []string)      { setMetadataSlice(m, "youtube_tags", v) }
func (m *Asset) YouTubeChapters() string        { return m.GetMetadataString("youtube_chapters") }
func (m *Asset) SetYouTubeChapters(v string)    { m.SetMetadataString("youtube_chapters", v) }
func (m *Asset) YouTubeThumbnail() string       { return m.GetMetadataString("youtube_thumbnail") }
func (m *Asset) SetYouTubeThumbnail(v string)   { m.SetMetadataString("youtube_thumbnail", v) }

// ── Semantic enrichment accessors ────────────────────────────────────────

func (m *Asset) ClipSummary() string          { return m.GetMetadataString("clip_summary") }
func (m *Asset) SetClipSummary(v string)      { m.SetMetadataString("clip_summary", v) }
func (m *Asset) Hook() string                 { return m.GetMetadataString("hook") }
func (m *Asset) SetHook(v string)             { m.SetMetadataString("hook", v) }
func (m *Asset) CleanTitle() string           { return m.GetMetadataString("clean_title") }
func (m *Asset) SetCleanTitle(v string)       { m.SetMetadataString("clean_title", v) }
func (m *Asset) ShortTitle() string           { return m.GetMetadataString("short_title") }
func (m *Asset) SetShortTitle(v string)       { m.SetMetadataString("short_title", v) }
func (m *Asset) EmbeddingText() string        { return m.GetMetadataString("embedding_text") }
func (m *Asset) SetEmbeddingText(v string)    { m.SetMetadataString("embedding_text", v) }
func (m *Asset) CleanTranscript() string      { return m.GetMetadataString("clean_transcript") }
func (m *Asset) SetCleanTranscript(v string)  { m.SetMetadataString("clean_transcript", v) }
func (m *Asset) RawTranscript() string        { return m.GetMetadataString("raw_transcript") }
func (m *Asset) SetRawTranscript(v string)    { m.SetMetadataString("raw_transcript", v) }
func (m *Asset) SearchVisibility() string     { return m.GetMetadataString("search_visibility") }
func (m *Asset) SetSearchVisibility(v string) { m.SetMetadataString("search_visibility", v) }
func (m *Asset) QualityTier() string          { return m.GetMetadataString("quality_tier") }
func (m *Asset) SetQualityTier(v string)      { m.SetMetadataString("quality_tier", v) }
func (m *Asset) Language() string             { return m.GetMetadataString("language") }
func (m *Asset) SetLanguage(v string)         { m.SetMetadataString("language", v) }
func (m *Asset) Topics() []string             { return MetadataStringSlice(m.Metadata, "topics") }
func (m *Asset) SetTopics(v []string)         { setMetadataSlice(m, "topics", v) }
func (m *Asset) Speakers() []string           { return MetadataStringSlice(m.Metadata, "speakers") }
func (m *Asset) SetSpeakers(v []string)       { setMetadataSlice(m, "speakers", v) }
func (m *Asset) MentionedPeople() []string {
	return MetadataStringSlice(m.Metadata, "mentioned_people")
}
func (m *Asset) SetMentionedPeople(v []string) { setMetadataSlice(m, "mentioned_people", v) }
func (m *Asset) People() []string              { return MetadataStringSlice(m.Metadata, "people") }
func (m *Asset) SetPeople(v []string)          { setMetadataSlice(m, "people", v) }
func (m *Asset) SourceTags() []string          { return MetadataStringSlice(m.Metadata, "source_tags") }
func (m *Asset) SetSourceTags(v []string)      { setMetadataSlice(m, "source_tags", v) }
func (m *Asset) ClipTags() []string            { return MetadataStringSlice(m.Metadata, "clip_tags") }
func (m *Asset) SetClipTags(v []string)        { setMetadataSlice(m, "clip_tags", v) }
func (m *Asset) SearchKeywords() []string      { return MetadataStringSlice(m.Metadata, "search_keywords") }
func (m *Asset) SetSearchKeywords(v []string)  { setMetadataSlice(m, "search_keywords", v) }
func (m *Asset) SemanticTags() []string        { return MetadataStringSlice(m.Metadata, "semantic_tags") }
func (m *Asset) SetSemanticTags(v []string)    { setMetadataSlice(m, "semantic_tags", v) }
func (m *Asset) IsSponsorSegment() bool        { return MetadataBool(m.Metadata, "is_sponsor_segment") }
func (m *Asset) SetIsSponsorSegment(v bool)    { setMetadataBool(m, "is_sponsor_segment", v) }
func (m *Asset) SponsorConfidence() string     { return m.GetMetadataString("sponsor_confidence") }
func (m *Asset) SetSponsorConfidence(v string) { m.SetMetadataString("sponsor_confidence", v) }

// ── Duplicate/topic-cluster accessors ────────────────────────────────────

func (m *Asset) DuplicateGroupID() string      { return m.GetMetadataString("duplicate_group_id") }
func (m *Asset) SetDuplicateGroupID(v string)  { m.SetMetadataString("duplicate_group_id", v) }
func (m *Asset) DuplicateOf() string           { return m.GetMetadataString("duplicate_of") }
func (m *Asset) SetDuplicateOf(v string)       { m.SetMetadataString("duplicate_of", v) }
func (m *Asset) IsDuplicate() bool             { return MetadataBool(m.Metadata, "is_duplicate") }
func (m *Asset) SetIsDuplicate(v bool)         { setMetadataBool(m, "is_duplicate", v) }
func (m *Asset) IsBestVersion() bool           { return MetadataBool(m.Metadata, "is_best_version") }
func (m *Asset) SetIsBestVersion(v bool)       { setMetadataBool(m, "is_best_version", v) }
func (m *Asset) DuplicateReason() string       { return m.GetMetadataString("duplicate_reason") }
func (m *Asset) SetDuplicateReason(v string)   { m.SetMetadataString("duplicate_reason", v) }
func (m *Asset) DuplicateScore() float64       { return MetadataFloat(m.Metadata, "duplicate_score") }
func (m *Asset) SetDuplicateScore(v float64)   { setMetadataFloat(m, "duplicate_score", v) }
func (m *Asset) TopicClusterID() string        { return m.GetMetadataString("topic_cluster_id") }
func (m *Asset) SetTopicClusterID(v string)    { m.SetMetadataString("topic_cluster_id", v) }
func (m *Asset) TopicClusterLabel() string     { return m.GetMetadataString("topic_cluster_label") }
func (m *Asset) SetTopicClusterLabel(v string) { m.SetMetadataString("topic_cluster_label", v) }
func (m *Asset) TopicClusterSize() int         { return MetadataInt(m.Metadata, "topic_cluster_size") }
func (m *Asset) SetTopicClusterSize(v int)     { m.SetMetadataInt("topic_cluster_size", v) }
func (m *Asset) TopicClusterRank() int         { return MetadataInt(m.Metadata, "topic_cluster_rank") }
func (m *Asset) SetTopicClusterRank(v int)     { m.SetMetadataInt("topic_cluster_rank", v) }

// ── VLM autotag accessors ────────────────────────────────────────────────

func (m *Asset) VLMTagged() string           { return m.GetMetadataString("vlm_tagged") }
func (m *Asset) SetVLMTagged(v string)       { m.SetMetadataString("vlm_tagged", v) }
func (m *Asset) VLMTagError() string         { return m.GetMetadataString("vlm_tag_error") }
func (m *Asset) SetVLMTagError(v string)     { m.SetMetadataString("vlm_tag_error", v) }
func (m *Asset) VLMModel() string            { return m.GetMetadataString("vlm_model") }
func (m *Asset) SetVLMModel(v string)        { m.SetMetadataString("vlm_model", v) }
func (m *Asset) VLMModelVersion() string     { return m.GetMetadataString("vlm_model_version") }
func (m *Asset) SetVLMModelVersion(v string) { m.SetMetadataString("vlm_model_version", v) }
func (m *Asset) VLMAnalysisDurationMs() int {
	return MetadataInt(m.Metadata, "vlm_analysis_duration_ms")
}
func (m *Asset) SetVLMAnalysisDurationMs(v int) { m.SetMetadataInt("vlm_analysis_duration_ms", v) }
func (m *Asset) VLMFramesAnalyzed() int         { return MetadataInt(m.Metadata, "vlm_frames_analyzed") }
func (m *Asset) SetVLMFramesAnalyzed(v int)     { m.SetMetadataInt("vlm_frames_analyzed", v) }
func (m *Asset) VLMSceneTypes() string          { return m.GetMetadataString("vlm_scene_types") }
func (m *Asset) SetVLMSceneTypes(v string)      { m.SetMetadataString("vlm_scene_types", v) }
func (m *Asset) VLMMoods() string               { return m.GetMetadataString("vlm_moods") }
func (m *Asset) SetVLMMoods(v string)           { m.SetMetadataString("vlm_moods", v) }
func (m *Asset) VLMVisualObjects() string       { return m.GetMetadataString("vlm_visual_objects") }
func (m *Asset) SetVLMVisualObjects(v string)   { m.SetMetadataString("vlm_visual_objects", v) }
func (m *Asset) VLMOCRText() string             { return m.GetMetadataString("vlm_ocr_text") }
func (m *Asset) SetVLMOCRText(v string)         { m.SetMetadataString("vlm_ocr_text", v) }
func (m *Asset) VLMAggregateDescription() string {
	return m.GetMetadataString("vlm_aggregate_description")
}
func (m *Asset) SetVLMAggregateDescription(v string) {
	m.SetMetadataString("vlm_aggregate_description", v)
}
func (m *Asset) TextOnScreen() string       { return m.GetMetadataString("text_on_screen") }
func (m *Asset) SetTextOnScreen(v string)   { m.SetMetadataString("text_on_screen", v) }
func (m *Asset) Lighting() string           { return m.GetMetadataString("lighting") }
func (m *Asset) SetLighting(v string)       { m.SetMetadataString("lighting", v) }
func (m *Asset) Composition() string        { return m.GetMetadataString("composition") }
func (m *Asset) SetComposition(v string)    { m.SetMetadataString("composition", v) }
func (m *Asset) DominantColors() string     { return m.GetMetadataString("dominant_colors") }
func (m *Asset) SetDominantColors(v string) { m.SetMetadataString("dominant_colors", v) }

// ── Source provenance accessors (source_url convergence) ───────────────
// The typed field Asset.SourceURL (column url) is the canonical owner of
// the source URL (godlike/06). The legacy metadata key "source_url" is a
// provenance mirror: readers MUST prefer the field and fall back to this
// key only for legacy rows that predate the url column. Go forbids a
// method named after the struct field, so the metadata-key accessor
// carries the Metadata prefix. Storage key is unchanged ("source_url").
func (m *Asset) MetadataSourceURL() string      { return m.GetMetadataString("source_url") }
func (m *Asset) SetMetadataSourceURL(v string)  { m.SetMetadataString("source_url", v) }
func (m *Asset) MetadataSourceProvider() string { return m.GetMetadataString("source_provider") }
func (m *Asset) SetMetadataSourceProvider(v string) {
	m.SetMetadataString("source_provider", v)
}
func (m *Asset) MetadataSourceVideoID() string { return m.GetMetadataString("source_video_id") }
func (m *Asset) SetMetadataSourceVideoID(v string) {
	m.SetMetadataString("source_video_id", v)
}
func (m *Asset) StartSec() float64 { return MetadataFloat(m.Metadata, "start_sec") }
func (m *Asset) SetStartSec(v float64) {
	setMetadataFloat(m, "start_sec", v)
}
func (m *Asset) EndSec() float64 { return MetadataFloat(m.Metadata, "end_sec") }
func (m *Asset) SetEndSec(v float64) {
	setMetadataFloat(m, "end_sec", v)
}

// ── Clip/content accessors shared by stockpipeline + admin backfills ──
func (m *Asset) Title() string       { return m.GetMetadataString("title") }
func (m *Asset) SetTitle(v string)   { m.SetMetadataString("title", v) }
func (m *Asset) Description() string { return m.GetMetadataString("description") }
func (m *Asset) SetDescription(v string) {
	m.SetMetadataString("description", v)
}
func (m *Asset) Round() int         { return MetadataInt(m.Metadata, "round") }
func (m *Asset) SetRound(v int)     { m.SetMetadataInt("round", v) }
func (m *Asset) Slug() string       { return m.GetMetadataString("slug") }
func (m *Asset) SetSlug(v string)   { m.SetMetadataString("slug", v) }
func (m *Asset) Sha256() string     { return m.GetMetadataString("sha256") }
func (m *Asset) SetSha256(v string) { m.SetMetadataString("sha256", v) }
