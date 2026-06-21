// Package types holds shared YouTube domain types extracted from the
// internal/application/youtube mega-package during PR3 Phase 2 (June 2026).
//
// These types are used across multiple files in the parent youtube package
// (metadata_enrich.go, manifest.go, intelligence_sync.go, enrichment.go,
// tag_utils.go, extractor_clean.go) and have been extracted here per
// AGENTS.md Pattern 5 to reduce the parent package's file count.
//
// The parent package re-exports these via zero-copy type aliases
// (type ClipMetadataFile = types.ClipMetadataFile) so existing callers
// compile without rename churn.
package types

// ClipMetadataFile is the human-readable metadata saved alongside each clip.
// It is serialized as JSON (metadata_<clip_id>.json) next to the clip MP4 and
// uploaded to Drive alongside the video file.
type ClipMetadataFile struct {
	ClipID            string   `json:"clip_id"`
	ClipTitle         string   `json:"clip_title"`
	RawTitle          string   `json:"raw_title,omitempty"`
	CleanTitle        string   `json:"clean_title,omitempty"`
	ShortTitle        string   `json:"short_title,omitempty"`
	EmbeddingText     string   `json:"embedding_text,omitempty"`
	VideoTitle        string   `json:"video_title"`
	Channel           string   `json:"channel"`
	Description       string   `json:"description"`
	RawTranscript     string   `json:"raw_transcript,omitempty"`
	Transcript        string   `json:"transcript,omitempty"`
	CleanTranscript   string   `json:"clean_transcript,omitempty"`
	ClipSummary       string   `json:"clip_summary,omitempty"`
	Hook              string   `json:"hook,omitempty"`
	Topics            []string `json:"topics,omitempty"`
	Speakers          []string `json:"speakers,omitempty"`
	MentionedPeople   []string `json:"mentioned_people,omitempty"`
	People            []string `json:"people,omitempty"`
	SourceTags        []string `json:"source_tags,omitempty"`
	ClipTags          []string `json:"clip_tags,omitempty"`
	SearchKeywords    []string `json:"search_keywords,omitempty"`
	DuplicateGroupID  string   `json:"duplicate_group_id,omitempty"`
	DuplicateOf       string   `json:"duplicate_of,omitempty"`
	IsDuplicate       bool     `json:"is_duplicate,omitempty"`
	IsBestVersion     bool     `json:"is_best_version,omitempty"`
	DuplicateReason   string   `json:"duplicate_reason,omitempty"`
	DuplicateScore    float64  `json:"duplicate_score,omitempty"`
	TopicClusterID    string   `json:"topic_cluster_id,omitempty"`
	TopicClusterLabel string   `json:"topic_cluster_label,omitempty"`
	TopicClusterSize  int      `json:"topic_cluster_size,omitempty"`
	TopicClusterRank  int      `json:"topic_cluster_rank,omitempty"`
	Language          string   `json:"language,omitempty"`
	DurationSec       int      `json:"duration_seconds"`
	StartSec          int      `json:"start_seconds"`
	EndSec            int      `json:"end_seconds"`
	Tags              []string `json:"tags,omitempty"`
	Categories        []string `json:"categories,omitempty"`
	QualityScore      float64  `json:"quality_score,omitempty"`
	SearchVisibility  string   `json:"search_visibility,omitempty"`
	YouTubeURL        string   `json:"youtube_url"`
	ThumbnailURL      string   `json:"thumbnail_url,omitempty"`
	UploadDate        string   `json:"upload_date,omitempty"`
	ViewCount         int64    `json:"view_count,omitempty"`
	LastEnriched      string   `json:"last_enriched"`
}

// ClipRichMetadata is the structured result from Ollama metadata generation.
// Used by generateClipMetadata (ollama_calls.go), enrichment.go,
// tag_utils.go, and extractor_clean.go for quality scoring.
type ClipRichMetadata struct {
	ClipSummary      string   `json:"clip_summary"`
	Topics           []string `json:"topics"`
	Speakers         []string `json:"speakers"`
	MentionedPeople  []string `json:"mentioned_people"`
	SourceTags       []string `json:"source_tags"`
	ClipTags         []string `json:"clip_tags"`
	SearchKeywords   []string `json:"search_keywords"`
	People           []string `json:"people"`
	Hook             string   `json:"hook"`
	CleanTitle       string   `json:"clean_title"`
	ShortTitle       string   `json:"short_title"`
	CleanTranscript  string   `json:"clean_transcript"`
	EmbeddingText    string   `json:"embedding_text"`
	Tags             []string `json:"tags"`
	QualityScore     float64  `json:"quality_score"`
	SearchVisibility string   `json:"search_visibility"`
}
