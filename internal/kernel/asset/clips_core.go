// Package asset — clip_folders and clip_manifests (Wave C / Phase 2 slim).
//
// Phase 2 (Wave C / Blocco 1 Asset SSOT, June 2026): the 9 SQL
// receivers that used to live here
// (UpsertFolder/DeleteFolder/GetFolder/GetFolderByVideoID/ListByFolderID/
// ListByFolderPath/CountByFolderID/ListFolders/SearchFolders) are now
// canonical on the LOCAL infra sqlite asset store
// (internal/infrastructure/database/sqlite/assets/folder_queries.go)
// and reached via HYBRID-embed promotion through the legacy struct.
//
// This file now carries ONLY the canonical domain types
// (ClipFolder/ClipManifest/ClipFolderStats/ClipManifestItem) and the
// `ClipManifestItem.UnmarshalJSON` parser. No SQL primitives, no
// `database/sql` import — acceptance rg `\bdatabase/sql\b` returns 0
// when combined with Phase 3.
package asset

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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

// ClipManifestItem represents a clip entry in the manifest. See the
// UnmarshalJSON method for the legacy `tags` shape compatibility.
type ClipManifestItem struct {
	ID                string
	Name              string
	RawName           string
	CleanTitle        string
	ShortTitle        string
	Start             string
	End               string
	StartSeconds      int
	EndSeconds        int
	DurationSeconds   int
	Filename          string
	LocalPath         string
	DriveLink         string
	FileHash          string
	Status            string
	Tags              []string
	SourceTags        []string
	ClipTags          []string
	SearchKeywords    []string
	EmbeddingText     string
	VideoTitle        string
	Channel           string
	Description       string
	RawTranscript     string
	Transcript        string
	CleanTranscript   string
	ClipSummary       string
	Hook              string
	Topics            []string
	Speakers          []string
	People            []string
	MentionedPeople   []string
	QualityScore      float64
	SearchVisibility  string
	DuplicateGroupID  string
	DuplicateOf       string
	IsDuplicate       bool
	IsBestVersion     bool
	DuplicateReason   string
	DuplicateScore    float64
	TopicClusterID    string
	TopicClusterLabel string
	TopicClusterSize  int
	TopicClusterRank  int
	YouTubeURL        string
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
