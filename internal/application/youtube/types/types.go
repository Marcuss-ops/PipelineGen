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
//
// PR5 Phase 3 (June 2026): extraction DTOs (ExtractRequest, ExtractResponse,
// ExtractItem, FolderInfo, ExtractStats, DestinationRequest) moved here so
// the new youtube/extraction/ capability service can import them without
// creating an import cycle with the parent youtube package.
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

// Segment represents a time-bounded clip segment with metadata
// extracted from the segment analysis pipeline.
type Segment struct {
	Start            string   `json:"start"`
	End              string   `json:"end"`
	Name             string   `json:"name"`
	Tags             []string `json:"tags,omitempty"`
	Summary          string   `json:"summary,omitempty"`
	Topics           []string `json:"topics,omitempty"`
	Speakers         []string `json:"speakers,omitempty"`
	MentionedPeople  []string `json:"mentioned_people,omitempty"`
	Hook             string   `json:"hook,omitempty"`
	QualityScore     float64  `json:"quality_score,omitempty"`
	SearchVisibility string   `json:"search_visibility,omitempty"`
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

// ── PR5 Phase 3: Extraction DTOs moved from parent package ──────────────

// ExtractRequest is the payload for a YouTube clip extraction request.
type ExtractRequest struct {
	URL            string              `json:"url"`
	Segments       []Segment           `json:"segments"`
	ForceKeyframes bool                `json:"force_keyframes"`
	Normalize      *bool               `json:"normalize,omitempty"`
	KeepAudio      bool                `json:"keep_audio"`
	WriteSummary   *bool               `json:"write_summary,omitempty"`
	Strategy       string              `json:"strategy,omitempty"`
	Concurrency    int                 `json:"concurrency,omitempty"`
	Destination    *DestinationRequest `json:"destination,omitempty"`
	Shuffle        bool                `json:"shuffle,omitempty"`
}

// DestinationRequest specifies the target folder for extraction output.
type DestinationRequest struct {
	Group           string `json:"group,omitempty"`
	FolderID        string `json:"folder_id,omitempty"`
	FolderPath      string `json:"folder_path,omitempty"`
	SubfolderName   string `json:"subfolder_name,omitempty"`
	CreateSubfolder bool   `json:"create_subfolder,omitempty"`
}

// ExtractResponse is the result of a clip extraction operation.
type ExtractResponse struct {
	OK              bool          `json:"ok"`
	SourceURL       string        `json:"source_url"`
	VideoID         string        `json:"video_id,omitempty"`
	Folder          *FolderInfo   `json:"folder,omitempty"`
	Stats           *ExtractStats `json:"stats,omitempty"`
	Items           []ExtractItem `json:"items"`
	Error           string        `json:"error,omitempty"`
	DriveFolderID   string        `json:"drive_folder_id,omitempty"`
	DriveFolderPath string        `json:"drive_folder_path,omitempty"`
}

// FolderInfo holds resolved folder metadata for an extraction run.
type FolderInfo struct {
	ID               string `json:"id"`
	LocalFolderPath  string `json:"local_folder_path"`
	DriveFolderID    string `json:"drive_folder_id,omitempty"`
	DriveFolderPath  string `json:"drive_folder_path,omitempty"`
	ManifestTXTPath  string `json:"manifest_txt_path,omitempty"`
	ManifestJSONPath string `json:"manifest_json_path,omitempty"`
}

// ExtractStats tracks the outcome counts for an extraction run.
type ExtractStats struct {
	Requested int `json:"requested"`
	Processed int `json:"processed"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
}

// ── PR4 Phase 1 (June 2026): TopicSearchRequest moved here from the
//     alias file types.go so the external HTTP handlers can import the
//     canonical struct via `yttypes.TopicSearchRequest` instead of
//     `youtube.TopicSearchRequest` (which was an inline-defined struct
//     in the now-deprecated shim file). After PR4-B finalisation
//     (internal sweep + ports.go/types.go deletion), this is the canonical
//     home and the alias file's struct definition is removed. ──

// TopicSearchRequest is the payload for the YouTube topic-search endpoint
// (POST /api/media/clips/search, GET .../search).
type TopicSearchRequest struct {
	Q     string `form:"q" json:"q" binding:"required"`
	Limit int    `form:"limit" json:"limit"`
	Sort  string `form:"sort" json:"sort"`
}

// ExtractItem represents a single processed clip from an extraction run.
type ExtractItem struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name"`
	Start           string `json:"start"`
	End             string `json:"end"`
	StartSeconds    int    `json:"start_seconds,omitempty"`
	EndSeconds      int    `json:"end_seconds,omitempty"`
	Duration        int    `json:"duration_seconds,omitempty"`
	Filename        string `json:"filename,omitempty"`
	FileHash        string `json:"file_hash,omitempty"`
	LocalPath       string `json:"local_path,omitempty"`
	DriveLink       string `json:"drive_link,omitempty"`
	DriveFileID     string `json:"drive_file_id,omitempty"`
	DownloadLink    string `json:"download_link,omitempty"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
	DriveFolderID   string `json:"drive_folder_id,omitempty"`
	DriveFolderPath string `json:"drive_folder_path,omitempty"`
}
