// Package media defines canonical domain types for media assets, clip
// folders, images, and source providers.
//
// This package is the source of truth: field tags, method semantics, and
// JSON wire format are defined here. The sibling package
// internal/media/models is a thin re-export shim (type aliases + const
// re-declarations) that preserves the legacy import path for the 40+
// existing callers; new code MUST import this package directly.
package media

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ── Enums ───────────────────────────────────────────────────────────

// SourceType identifies the origin of a media asset.
type SourceType string

const (
	// SourceStock indicates stock footage media.
	SourceStock SourceType = "stock"
	// SourceArtlist indicates media sourced from Artlist.
	SourceArtlist SourceType = "artlist"
	// SourceYoutubeClip indicates a clip from YouTube.
	SourceYoutubeClip SourceType = "youtube_clip"
	// SourceClipDrive indicates a clip sourced from Google Drive.
	SourceClipDrive SourceType = "clip_drive"
	// SourceImage indicates an image asset.
	SourceImage SourceType = "image"
	// SourceGenerated indicates generated content (script, voiceover).
	SourceGenerated SourceType = "generated"
)

// IsValid reports whether the SourceType matches a known constant.
func (s SourceType) IsValid() bool {
	switch s {
	case SourceStock, SourceArtlist, SourceYoutubeClip, SourceClipDrive, SourceImage, SourceGenerated:
		return true
	}
	return false
}

// MediaType classifies the content kind of a media asset.
type MediaType string

const (
	// MediaTypeStock refers to stock footage.
	MediaTypeStock MediaType = "stock"
	// MediaTypeClip refers to a video clip.
	MediaTypeClip MediaType = "clip"
	// MediaTypeImage refers to an image.
	MediaTypeImage MediaType = "image"
	// MediaTypeAudio refers to an audio file (voiceover).
	MediaTypeAudio MediaType = "audio"
	// MediaTypeDocument refers to a document (Google Doc).
	MediaTypeDocument MediaType = "document"
)

// IsValid reports whether the MediaType matches a known constant.
func (m MediaType) IsValid() bool {
	switch m {
	case MediaTypeStock, MediaTypeClip, MediaTypeImage, MediaTypeAudio, MediaTypeDocument:
		return true
	}
	return false
}

// AssetStatus tracks the lifecycle of a media asset.
type AssetStatus string

const (
	// AssetStatusActive indicates an active and available asset.
	AssetStatusActive AssetStatus = "active"
	// AssetStatusArchived indicates an archived asset.
	AssetStatusArchived AssetStatus = "archived"
	// AssetStatusDeleted indicates a soft-deleted asset.
	AssetStatusDeleted AssetStatus = "deleted"
	// AssetStatusProcessing indicates an asset currently being processed.
	AssetStatusProcessing AssetStatus = "processing"
	// AssetStatusFailed indicates an asset with a processing error.
	AssetStatusFailed AssetStatus = "failed"
)

// IsValid reports whether the AssetStatus matches a known constant.
func (s AssetStatus) IsValid() bool {
	switch s {
	case AssetStatusActive, AssetStatusArchived, AssetStatusDeleted, AssetStatusProcessing, AssetStatusFailed:
		return true
	}
	return false
}

// ── Tree / Hierarchy ────────────────────────────────────────────────

// AssetNode represents a node in the asset tree hierarchy for API responses.
type AssetNode struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	AssetID     string `json:"asset_id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	ParentID    string `json:"parent_id"`
	RootID      string `json:"root_id"`
	Path        string `json:"path"`
	Depth       int    `json:"depth"`
	IsFolder    bool   `json:"is_folder"`
	DriveFileID string `json:"drive_file_id"`
	DriveLink   string `json:"drive_link"`
	Metadata    string `json:"metadata"`
	ChildCount  int    `json:"child_count,omitempty"`
}

// ── Processing Result ───────────────────────────────────────────────

// AssetExecutionResult is a unified result type for asset processing across
// all modules. It combines fields from BatchItem, RunTagItem, ExtractItem,
// AssetResult, FinalizeResult.
type AssetExecutionResult struct {
	ID           string `json:"id,omitempty"`
	Source       string `json:"source,omitempty"`     // e.g. "youtube", "artlist", "voiceover"
	MediaType    string `json:"media_type,omitempty"` // e.g. "video", "audio", "image"
	Filename     string `json:"filename,omitempty"`
	LocalPath    string `json:"local_path,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DownloadLink string `json:"download_link,omitempty"`
	FileHash     string `json:"file_hash,omitempty"`
	Status       string `json:"status,omitempty"` // e.g. "processed", "skipped_existing", "failed"
	Error        string `json:"error,omitempty"`
}

// ── Clip Folders ────────────────────────────────────────────────────

// ClipFolder represents a folder containing multiple clips from the same source.
type ClipFolder struct {
	ID               string    `json:"id"`
	Source           string    `json:"source"` // youtube, stock, etc.
	SourceURL        string    `json:"source_url"`
	VideoID          string    `json:"video_id,omitempty"`
	FolderID         string    `json:"folder_id"`         // Drive folder ID
	FolderPath       string    `json:"folder_path"`       // Drive folder path
	LocalFolderPath  string    `json:"local_folder_path"` // Local folder path
	Group            string    `json:"group"`
	ManifestTXTPath  string    `json:"manifest_txt_path"`  // Path to clip_manifest.txt
	ManifestJSONPath string    `json:"manifest_json_path"` // Path to clip_manifest.json
	ClipCount        int       `json:"clip_count"`
	ProcessedCount   int       `json:"processed_count"`
	FailedCount      int       `json:"failed_count"`
	SkippedCount     int       `json:"skipped_count"`
	LastError        string    `json:"last_error,omitempty"`
	Metadata         string    `json:"metadata,omitempty"` // JSON metadata
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// ClipManifest represents the JSON manifest for a clip folder.
type ClipManifest struct {
	ID              string             `json:"id"`
	FolderID        string             `json:"folder_id"`
	FolderPath      string             `json:"folder_path"`
	Source          string             `json:"source"`
	SourceURL       string             `json:"source_url"`
	VideoID         string             `json:"video_id,omitempty"`
	FolderSlug      string             `json:"folder_slug,omitempty"`
	LocalFolderPath string             `json:"local_folder_path"`
	UpdatedAt       time.Time          `json:"updated_at"`
	Stats           ClipFolderStats    `json:"stats"`
	Clips           []ClipManifestItem `json:"clips"`
}

// ClipFolderStats represents aggregated statistics for the folder.
type ClipFolderStats struct {
	ClipCount      int `json:"clip_count"`
	ProcessedCount int `json:"processed_count"`
	FailedCount    int `json:"failed_count"`
	SkippedCount   int `json:"skipped_count"`
}

// ClipManifestItem represents a clip entry in the manifest.
type ClipManifestItem struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	RawName           string   `json:"raw_name,omitempty"`
	CleanTitle        string   `json:"clean_title,omitempty"`
	ShortTitle        string   `json:"short_title,omitempty"`
	Start             string   `json:"start"`
	End               string   `json:"end"`
	StartSeconds      int      `json:"start_seconds"`
	EndSeconds        int      `json:"end_seconds"`
	DurationSeconds   int      `json:"duration_seconds"`
	Filename          string   `json:"filename,omitempty"`
	LocalPath         string   `json:"local_path,omitempty"`
	DriveLink         string   `json:"drive_link,omitempty"`
	FileHash          string   `json:"file_hash,omitempty"`
	Status            string   `json:"status"`
	Tags              []string `json:"tags,omitempty"`
	SourceTags        []string `json:"source_tags,omitempty"`
	ClipTags          []string `json:"clip_tags,omitempty"`
	SearchKeywords    []string `json:"search_keywords,omitempty"`
	EmbeddingText     string   `json:"embedding_text,omitempty"`
	VideoTitle        string   `json:"video_title,omitempty"`
	Channel           string   `json:"channel,omitempty"`
	Description       string   `json:"description,omitempty"`
	RawTranscript     string   `json:"raw_transcript,omitempty"`
	Transcript        string   `json:"transcript,omitempty"`
	CleanTranscript   string   `json:"clean_transcript,omitempty"`
	ClipSummary       string   `json:"clip_summary,omitempty"`
	Hook              string   `json:"hook,omitempty"`
	Topics            []string `json:"topics,omitempty"`
	Speakers          []string `json:"speakers,omitempty"`
	People            []string `json:"people,omitempty"`
	MentionedPeople   []string `json:"mentioned_people,omitempty"`
	QualityScore      float64  `json:"quality_score,omitempty"`
	SearchVisibility  string   `json:"search_visibility,omitempty"`
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
	YouTubeURL        string   `json:"youtube_url,omitempty"`
}

// UnmarshalJSON accepts both the legacy string-encoded JSON array and the new
// array form for tags. This keeps existing manifests readable after the schema
// switch.
func (c *ClipManifestItem) UnmarshalJSON(data []byte) error {
	type alias struct {
		ID                string          `json:"id"`
		Name              string          `json:"name"`
		RawName           string          `json:"raw_name,omitempty"`
		CleanTitle        string          `json:"clean_title,omitempty"`
		ShortTitle        string          `json:"short_title,omitempty"`
		Start             string          `json:"start"`
		End               string          `json:"end"`
		StartSeconds      int             `json:"start_seconds"`
		EndSeconds        int             `json:"end_seconds"`
		DurationSeconds   int             `json:"duration_seconds"`
		Filename          string          `json:"filename,omitempty"`
		LocalPath         string          `json:"local_path,omitempty"`
		DriveLink         string          `json:"drive_link,omitempty"`
		FileHash          string          `json:"file_hash,omitempty"`
		Status            string          `json:"status"`
		Tags              json.RawMessage `json:"tags,omitempty"`
		SourceTags        json.RawMessage `json:"source_tags,omitempty"`
		ClipTags          json.RawMessage `json:"clip_tags,omitempty"`
		SearchKeywords    json.RawMessage `json:"search_keywords,omitempty"`
		EmbeddingText     string          `json:"embedding_text,omitempty"`
		VideoTitle        string          `json:"video_title,omitempty"`
		Channel           string          `json:"channel,omitempty"`
		Description       string          `json:"description,omitempty"`
		RawTranscript     string          `json:"raw_transcript,omitempty"`
		Transcript        string          `json:"transcript,omitempty"`
		CleanTranscript   string          `json:"clean_transcript,omitempty"`
		ClipSummary       string          `json:"clip_summary,omitempty"`
		Hook              string          `json:"hook,omitempty"`
		Topics            json.RawMessage `json:"topics,omitempty"`
		Speakers          json.RawMessage `json:"speakers,omitempty"`
		People            json.RawMessage `json:"people,omitempty"`
		MentionedPeople   json.RawMessage `json:"mentioned_people,omitempty"`
		QualityScore      float64         `json:"quality_score,omitempty"`
		SearchVisibility  string          `json:"search_visibility,omitempty"`
		DuplicateGroupID  string          `json:"duplicate_group_id,omitempty"`
		DuplicateOf       string          `json:"duplicate_of,omitempty"`
		IsDuplicate       bool            `json:"is_duplicate,omitempty"`
		IsBestVersion     bool            `json:"is_best_version,omitempty"`
		DuplicateReason   string          `json:"duplicate_reason,omitempty"`
		DuplicateScore    float64         `json:"duplicate_score,omitempty"`
		TopicClusterID    string          `json:"topic_cluster_id,omitempty"`
		TopicClusterLabel string          `json:"topic_cluster_label,omitempty"`
		TopicClusterSize  int             `json:"topic_cluster_size,omitempty"`
		TopicClusterRank  int             `json:"topic_cluster_rank,omitempty"`
		YouTubeURL        string          `json:"youtube_url,omitempty"`
	}

	var aux alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	*c = ClipManifestItem{
		ID:                aux.ID,
		Name:              aux.Name,
		RawName:           aux.RawName,
		CleanTitle:        aux.CleanTitle,
		ShortTitle:        aux.ShortTitle,
		Start:             aux.Start,
		End:               aux.End,
		StartSeconds:      aux.StartSeconds,
		EndSeconds:        aux.EndSeconds,
		DurationSeconds:   aux.DurationSeconds,
		Filename:          aux.Filename,
		LocalPath:         aux.LocalPath,
		DriveLink:         aux.DriveLink,
		FileHash:          aux.FileHash,
		Status:            aux.Status,
		VideoTitle:        aux.VideoTitle,
		Channel:           aux.Channel,
		Description:       aux.Description,
		RawTranscript:     aux.RawTranscript,
		Transcript:        aux.Transcript,
		CleanTranscript:   aux.CleanTranscript,
		ClipSummary:       aux.ClipSummary,
		Hook:              aux.Hook,
		QualityScore:      aux.QualityScore,
		SearchVisibility:  aux.SearchVisibility,
		DuplicateGroupID:  aux.DuplicateGroupID,
		DuplicateOf:       aux.DuplicateOf,
		IsDuplicate:       aux.IsDuplicate,
		IsBestVersion:     aux.IsBestVersion,
		DuplicateReason:   aux.DuplicateReason,
		DuplicateScore:    aux.DuplicateScore,
		TopicClusterID:    aux.TopicClusterID,
		TopicClusterLabel: aux.TopicClusterLabel,
		TopicClusterSize:  aux.TopicClusterSize,
		TopicClusterRank:  aux.TopicClusterRank,
		YouTubeURL:        aux.YouTubeURL,
	}

	if len(aux.Tags) > 0 && string(aux.Tags) != "null" {
		var tags []string
		if aux.Tags[0] == '"' {
			var legacy string
			if err := json.Unmarshal(aux.Tags, &legacy); err != nil {
				return fmt.Errorf("invalid legacy tags encoding: %w", err)
			}
			legacy = strings.TrimSpace(legacy)
			if legacy != "" && legacy != "[]" {
				if err := json.Unmarshal([]byte(legacy), &tags); err != nil {
					return fmt.Errorf("invalid legacy tags payload: %w", err)
				}
				c.Tags = tags
			}
		} else if err := json.Unmarshal(aux.Tags, &tags); err != nil {
			return fmt.Errorf("invalid tags payload: %w", err)
		} else {
			c.Tags = tags
		}
	}
	if len(aux.SourceTags) > 0 && string(aux.SourceTags) != "null" {
		var sourceTags []string
		if err := json.Unmarshal(aux.SourceTags, &sourceTags); err == nil {
			c.SourceTags = sourceTags
		}
	}
	if len(aux.ClipTags) > 0 && string(aux.ClipTags) != "null" {
		var clipTags []string
		if err := json.Unmarshal(aux.ClipTags, &clipTags); err == nil {
			c.ClipTags = clipTags
		}
	}
	if len(aux.SearchKeywords) > 0 && string(aux.SearchKeywords) != "null" {
		var keywords []string
		if err := json.Unmarshal(aux.SearchKeywords, &keywords); err == nil {
			c.SearchKeywords = keywords
		}
	}
	if aux.EmbeddingText != "" {
		c.EmbeddingText = aux.EmbeddingText
	}

	if len(aux.Topics) > 0 && string(aux.Topics) != "null" {
		var topics []string
		if err := json.Unmarshal(aux.Topics, &topics); err == nil {
			c.Topics = topics
		}
	}
	if len(aux.Speakers) > 0 && string(aux.Speakers) != "null" {
		var speakers []string
		if err := json.Unmarshal(aux.Speakers, &speakers); err == nil {
			c.Speakers = speakers
		}
	}
	if len(aux.People) > 0 && string(aux.People) != "null" {
		var people []string
		if err := json.Unmarshal(aux.People, &people); err == nil {
			c.People = people
		}
	}
	if len(aux.MentionedPeople) > 0 && string(aux.MentionedPeople) != "null" {
		var mentioned []string
		if err := json.Unmarshal(aux.MentionedPeople, &mentioned); err == nil {
			c.MentionedPeople = mentioned
		}
	}
	if len(c.MentionedPeople) == 0 && len(c.People) > 0 {
		c.MentionedPeople = append([]string(nil), c.People...)
	}
	if len(c.People) == 0 && len(c.MentionedPeople) > 0 {
		c.People = append([]string(nil), c.MentionedPeople...)
	}
	return nil
}

// ── Indexing ────────────────────────────────────────────────────────

// IndexingCheckpoint represents a checkpoint for the indexing process.
type IndexingCheckpoint struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	LastIndexedAt time.Time `json:"last_indexed_at"`
	Metadata      string    `json:"metadata"`
}

// ── Pipeline Strategy ───────────────────────────────────────────────

// PipelineStrategy controls how existing data is handled during processing.
type PipelineStrategy string

const (
	StrategyVerify  PipelineStrategy = "verify"
	StrategySkip    PipelineStrategy = "skip"
	StrategyReplace PipelineStrategy = "replace"
)

// NormalizeStrategy coerces arbitrary user input to a known PipelineStrategy
// value. Unknown inputs default to StrategyVerify unless force is true, in
// which case they coerce to StrategyReplace.
func NormalizeStrategy(strategy string, force bool) PipelineStrategy {
	s := PipelineStrategy(strings.ToLower(strings.TrimSpace(strategy)))
	switch s {
	case StrategySkip, StrategyVerify, StrategyReplace:
		return s
	}
	if force {
		return StrategyReplace
	}
	return StrategyVerify
}

// ActiveKey produces a deterministic enqueue dedup key for jobs in the
// inactive/active-pending state.
func ActiveKey(prefix, term, folderID string, strategy string, dryRun bool) string {
	return fmt.Sprintf("%s|%s|%s|%s|%t",
		prefix,
		term,
		folderID,
		strategy,
		dryRun,
	)
}

// ── Monitoring ──────────────────────────────────────────────────────

// MonitoredSource represents a discovered external source (YouTube video,
// Artlist asset, Drive file, etc.)
type MonitoredSource struct {
	ID             string `json:"id" db:"id"`
	Source         string `json:"source" db:"source"`
	ExternalID     string `json:"external_id" db:"external_id"`
	ExternalURL    string `json:"external_url" db:"external_url"`
	Title          string `json:"title" db:"title"`
	ChannelID      string `json:"channel_id" db:"channel_id"`
	ChannelURL     string `json:"channel_url" db:"channel_url"`
	Keyword        string `json:"keyword" db:"keyword"`
	GroupName      string `json:"group_name" db:"group_name"`
	Category       string `json:"category" db:"category"`
	Status         string `json:"status" db:"status"`
	LastSeenAt     string `json:"last_seen_at" db:"last_seen_at"`
	LastCheckedAt  string `json:"last_checked_at" db:"last_checked_at"`
	ProcessedCount int    `json:"processed_count" db:"processed_count"`
	MetadataJSON   string `json:"metadata_json" db:"metadata_json"`
	CreatedAt      string `json:"created_at" db:"created_at"`
	UpdatedAt      string `json:"updated_at" db:"updated_at"`
}

// TableName returns the database table name for the MonitoredSource model.
func (MonitoredSource) TableName() string {
	return "monitored_sources"
}
