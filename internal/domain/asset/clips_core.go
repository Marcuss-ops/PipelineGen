package asset

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sqlutil "github.com/Marcuss-ops/PipelineGen/pkg/sqlutil"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ClipFolder represents a folder containing multiple clips from the same source
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

// ClipManifest represents the JSON manifest for a clip folder
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

// ClipFolderStats represents aggregated statistics for the folder
type ClipFolderStats struct {
	ClipCount      int `json:"clip_count"`
	ProcessedCount int `json:"processed_count"`
	FailedCount    int `json:"failed_count"`
	SkippedCount   int `json:"skipped_count"`
}

// ClipManifestItem represents a clip entry in the manifest
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

func (s *AssetStoreSQLite) UpsertFolder(ctx context.Context, folder *ClipFolder) error {
	now := time.Now()
	// Compute search key: lowercase group + folder path, remove spaces
	searchKey := strings.ToLower(folder.Group + " " + folder.FolderPath)
	searchKey = strings.ReplaceAll(searchKey, " ", "")

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO clip_folders (id, source, source_url, video_id, folder_id, folder_path,
			local_folder_path, group_name, manifest_txt_path, manifest_json_path,
			clip_count, processed_count, failed_count, skipped_count, last_error, metadata, created_at, updated_at, search_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source, source_url=excluded.source_url, video_id=excluded.video_id,
			folder_id=excluded.folder_id, folder_path=excluded.folder_path,
			local_folder_path=excluded.local_folder_path, group_name=excluded.group_name,
			manifest_txt_path=excluded.manifest_txt_path, manifest_json_path=excluded.manifest_json_path,
			clip_count=excluded.clip_count, processed_count=excluded.processed_count,
			failed_count=excluded.failed_count, skipped_count=excluded.skipped_count,
			last_error=excluded.last_error, metadata=excluded.metadata, updated_at=excluded.updated_at,
			search_key=excluded.search_key
		`, folder.ID, folder.Source, folder.SourceURL, folder.VideoID, folder.FolderID, folder.FolderPath,
		folder.LocalFolderPath, folder.Group, folder.ManifestTXTPath, folder.ManifestJSONPath,
		folder.ClipCount, folder.ProcessedCount, folder.FailedCount, folder.SkippedCount, folder.LastError, folder.Metadata,
		timeutil.FormatRFC3339(folder.CreatedAt), timeutil.FormatRFC3339(now), searchKey)

	return err
}

// DeleteFolder deletes a clip folder by its ID.
func (s *AssetStoreSQLite) DeleteFolder(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("clip folder id is required")
	}

	_, err := s.db.ExecContext(ctx, "DELETE FROM clip_folders WHERE id = ?", id)
	return err
}

// GetFolder retrieves a clip folder by ID
func (s *AssetStoreSQLite) GetFolder(ctx context.Context, id string) (*ClipFolder, error) {
	query := buildClipFolderQuery("") + " WHERE id = ? LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, id)

	var folder ClipFolder
	var createdAt, updatedAt string
	err := row.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID, &folder.FolderID,
		&folder.FolderPath, &folder.LocalFolderPath, &folder.Group, &folder.ManifestTXTPath,
		&folder.ManifestJSONPath, &folder.ClipCount, &folder.ProcessedCount, &folder.FailedCount,
		&folder.SkippedCount, &folder.LastError, &folder.Metadata, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	folder.CreatedAt = timeutil.ParseRFC3339(createdAt)
	folder.UpdatedAt = timeutil.ParseRFC3339(updatedAt)

	return &folder, nil
}

// GetFolderByVideoID retrieves a clip folder by video ID
func (s *AssetStoreSQLite) GetFolderByVideoID(ctx context.Context, videoID string) (*ClipFolder, error) {
	query := buildClipFolderQuery("") + " WHERE video_id = ? LIMIT 1"
	row := s.db.QueryRowContext(ctx, query, videoID)

	var folder ClipFolder
	var createdAt, updatedAt string
	err := row.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID, &folder.FolderID,
		&folder.FolderPath, &folder.LocalFolderPath, &folder.Group, &folder.ManifestTXTPath,
		&folder.ManifestJSONPath, &folder.ClipCount, &folder.ProcessedCount, &folder.FailedCount,
		&folder.SkippedCount, &folder.LastError, &folder.Metadata, &createdAt, &updatedAt)

	if err != nil {
		return nil, err
	}

	folder.CreatedAt = timeutil.ParseRFC3339(createdAt)
	folder.UpdatedAt = timeutil.ParseRFC3339(updatedAt)

	return &folder, nil
}

// ListByFolderID returns all clips for a given folder ID (canonical column after migration 059).
func (s *AssetStoreSQLite) ListByFolderID(ctx context.Context, folderID string) ([]*Asset, error) {
	query := buildMediaAssetQuery("") + " AND folder_id = ? ORDER BY created_at ASC"
	rows, err := s.db.QueryContext(ctx, query, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// ListByFolderPath returns all clips for a given folder path (canonical column).
func (s *AssetStoreSQLite) ListByFolderPath(ctx context.Context, folderPath string) ([]*Asset, error) {
	query := buildMediaAssetQuery("") + " AND folder_path = ? ORDER BY created_at ASC"
	rows, err := s.db.QueryContext(ctx, query, folderPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			return nil, err
		}
		clips = append(clips, clip)
	}
	return clips, rows.Err()
}

// CountByFolderID returns the number of clips in a folder (folder_id is a canonical column).
func (s *AssetStoreSQLite) CountByFolderID(ctx context.Context, folderID string) (int, error) {
	query := "SELECT COUNT(*) FROM media_assets WHERE folder_id = ?"
	row := s.db.QueryRowContext(ctx, query, folderID)
	var count int
	err := row.Scan(&count)
	return count, err
}

// ListFolders returns all clip folders, optionally filtered by source
func (s *AssetStoreSQLite) ListFolders(ctx context.Context, source string) ([]*ClipFolder, error) {
	query := buildClipFolderQuery(source)
	if source != "" {
		query += " ORDER BY updated_at DESC"
	} else {
		query += " ORDER BY updated_at DESC"
	}
	args := []any{}
	if source != "" {
		args = append(args, source)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []*ClipFolder
	for rows.Next() {
		var folder ClipFolder
		var createdAt, updatedAt string
		err := rows.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID,
			&folder.FolderID, &folder.FolderPath, &folder.LocalFolderPath, &folder.Group,
			&folder.ManifestTXTPath, &folder.ManifestJSONPath, &folder.ClipCount,
			&folder.ProcessedCount, &folder.FailedCount, &folder.SkippedCount,
			&folder.LastError, &folder.Metadata, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		folder.CreatedAt = timeutil.ParseRFC3339(createdAt)
		folder.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
		folders = append(folders, &folder)
	}
	return folders, rows.Err()
}

// SearchFolders searches clip folders by keyword in source_url, video_id, group_name, or folder_path
// Uses LIKE search.
func (s *AssetStoreSQLite) SearchFolders(ctx context.Context, keyword string) ([]*ClipFolder, error) {
	columns := []string{"source_url", "video_id", "group_name", "folder_path"}
	keywords := strings.Fields(keyword)
	if len(keywords) == 0 {
		keywords = []string{keyword}
	}

	conditionSQL, args := sqlutil.BuildFallbackLikeConditions(keywords, columns)
	if conditionSQL == "" {
		return []*ClipFolder{}, nil
	}

	query := buildClipFolderQuery("") + " WHERE " + conditionSQL + " ORDER BY updated_at DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []*ClipFolder
	for rows.Next() {
		var folder ClipFolder
		var createdAt, updatedAt string
		err := rows.Scan(&folder.ID, &folder.Source, &folder.SourceURL, &folder.VideoID,
			&folder.FolderID, &folder.FolderPath, &folder.LocalFolderPath, &folder.Group,
			&folder.ManifestTXTPath, &folder.ManifestJSONPath, &folder.ClipCount,
			&folder.ProcessedCount, &folder.FailedCount, &folder.SkippedCount,
			&folder.LastError, &folder.Metadata, &createdAt, &updatedAt)
		if err != nil {
			return nil, err
		}
		folder.CreatedAt = timeutil.ParseRFC3339(createdAt)
		folder.UpdatedAt = timeutil.ParseRFC3339(updatedAt)
		folders = append(folders, &folder)
	}
	return folders, rows.Err()
}

// GetFolderChildren returns all clips that are children of the given parent_folder_id.
// parent_folder_id is stored in metadata_json.
// Pass an empty string to get root folders.
