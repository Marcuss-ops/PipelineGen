// Package clip provides clip management functionality for the VeloxEditing system.
package clip

import (
	"time"
)

// Clip represents a video clip with metadata
type Clip struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Filename     string    `json:"filename"`
	Duration     float64   `json:"duration"`
	Resolution   string    `json:"resolution"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	Thumbnail    string    `json:"thumbnail"`
	DriveLink    string    `json:"drive_link"`
	DownloadLink string    `json:"download_link,omitempty"`
	Size         int64     `json:"size"`
	MimeType     string    `json:"mime_type"`
	CreatedAt    time.Time `json:"created_at"`
	ModifiedAt   time.Time `json:"modified_at"`
	FolderID     string    `json:"folder_id"`
	FolderName   string    `json:"folder_name"`
}

// Folder represents a clip folder with metadata
type Folder struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Path        string    `json:"path,omitempty"`
	Link        string    `json:"link"`
	ParentID    string    `json:"parent_id,omitempty"`
	ClipCount   int       `json:"clip_count"`
	Clips       []Clip    `json:"clips,omitempty"`
	Subfolders  []Folder  `json:"subfolders,omitempty"`
	Depth       int       `json:"depth,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
	ModifiedAt  time.Time `json:"modified_at,omitempty"`
}

// Suggestion represents a clip suggestion with relevance score
type Suggestion struct {
	Clip       Clip    `json:"clip"`
	Score      float64 `json:"score"`
	MatchType  string  `json:"match_type"` // "folder_name", "file_name", "content"
	MatchTerms []string `json:"match_terms,omitempty"`
}

// SearchResult represents the result of a folder search
type SearchResult struct {
	Folders     []Folder `json:"folders"`
	Total       int      `json:"total"`
	Query       string   `json:"query"`
	Group       string   `json:"group,omitempty"`
	Cached      bool     `json:"cached"`
	SearchTime  int64    `json:"search_time_ms"`
}

// FolderContent represents clips in a folder
type FolderContent struct {
	FolderID     string    `json:"folder_id"`
	FolderName   string    `json:"folder_name"`
	Clips        []Clip    `json:"clips"`
	Videos       []Clip    `json:"videos"` // Alias for clips
	Subfolders   []Folder  `json:"subfolders"`
	TotalClips   int       `json:"total_clips"`
	TotalVideos  int       `json:"total_videos"` // Alias
	TotalSubfolders int   `json:"total_subfolders"`
}

// ClipGroup represents a predefined clip group
type ClipGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	FolderID    string `json:"folder_id,omitempty"`
}

// Predefined clip groups - includes all Drive folder categories
var ClipGroups = []ClipGroup{
	{ID: "boxe", Name: "Boxe", Description: "Boxing clips and highlights"},
	{ID: "crimine", Name: "Crimine", Description: "True crime and investigation clips"},
	{ID: "discovery", Name: "Discovery", Description: "Documentary and discovery content"},
	{ID: "hiphop", Name: "HipHop", Description: "Hip hop music and culture clips"},
	{ID: "musica", Name: "Musica", Description: "Music and performance clips"},
	{ID: "wwe", Name: "Wwe", Description: "WWE wrestling highlights and moments"},
	{ID: "tech", Name: "Technology", Description: "Tech and AI related clips"},
	{ID: "stock", Name: "Stock", Description: "Generic stock footage"},
	{ID: "interviews", Name: "Interviews", Description: "Interview clips and conversations"},
	{ID: "broll", Name: "B-Roll", Description: "B-Roll footage and generic scenes"},
	{ID: "highlights", Name: "Highlights", Description: "Highlight and viral moments"},
	{ID: "general", Name: "General", Description: "General purpose clips"},
	{ID: "nature", Name: "Nature", Description: "Nature and landscape clips"},
	{ID: "urban", Name: "Urban", Description: "City and urban footage"},
	{ID: "business", Name: "Business", Description: "Business and startup clips"},
	{ID: "voiceover", Name: "Voiceover", Description: "Voiceover audio files"},
}

// SearchFoldersRequest represents a search folders request
type SearchFoldersRequest struct {
	Query      string `json:"query"`
	Group      string `json:"group"`
	ParentID   string `json:"parent_id"`
	MaxResults int    `json:"max_results"`
	MaxDepth   int    `json:"max_depth"`
}

// ReadFolderClipsRequest represents a read folder clips request
type ReadFolderClipsRequest struct {
	FolderID   string `json:"folder_id"`
	FolderName string `json:"folder_name"`
	IncludeSubfolders bool `json:"include_subfolders"`
}

// SuggestRequest represents a suggest request
type SuggestRequest struct {
	Title      string  `json:"title"`
	Script     string  `json:"script,omitempty"`
	Group      string  `json:"group"`
	MediaType  string  `json:"media_type"`   // "clip" or "stock"
	MaxResults int     `json:"max_results"`
	MinScore   float64 `json:"min_score"`
}

// SuggestResponse represents a suggest response
type SuggestResponse struct {
	Title       string       `json:"title"`
	Suggestions []Suggestion `json:"suggestions"`
	Total       int          `json:"total"`
	Group       string       `json:"group"`
	ProcessingTime int64     `json:"processing_time_ms"`
}

// CreateSubfolderRequest represents a create subfolder request
type CreateSubfolderRequest struct {
	FolderName string `json:"folder_name" binding:"required"`
	ParentID   string `json:"parent_id"`
	Group      string `json:"group"`
}

// SubfoldersRequest represents a subfolders request
type SubfoldersRequest struct {
	ParentID   string `json:"parent_id"`
	MaxDepth   int    `json:"max_depth"`
	MaxResults int    `json:"max_results"`
}

// DownloadClipRequest represents a download clip request
type DownloadClipRequest struct {
	YouTubeURL  string `json:"youtube_url" binding:"required"`
	StartTime   string `json:"start_time"`
	EndTime     string `json:"end_time"`
	Title       string `json:"title"`
	DriveFolder string `json:"drive_folder"`
	Group       string `json:"group"`
}

// UploadClipRequest represents an upload clip request
type UploadClipRequest struct {
	ClipPath    string `json:"clip_path" binding:"required"`
	DriveFolder string `json:"drive_folder" binding:"required"`
	Title       string `json:"title"`
	Group       string `json:"group"`
}

// SearchRequest represents a clip index search request
type SearchRequest struct {
	Query       string   `json:"query"`
	Group       string   `json:"group"`
	MediaType   string   `json:"media_type"`   // "clip" or "stock"
	FolderID    string   `json:"folder_id"`
	MinDuration float64  `json:"min_duration"`
	MaxDuration float64  `json:"max_duration"`
	Resolution  string   `json:"resolution"`
	Tags        []string `json:"tags"`
	MaxResults  int      `json:"max_results"`
	Offset      int      `json:"offset"`
	MinScore    float64  `json:"min_score"`
}

// SentenceSuggestRequest represents a request for clip suggestions for a sentence
type SentenceSuggestRequest struct {
	Sentence   string  `json:"sentence" binding:"required"`
	MaxResults int     `json:"max_results"`
	MinScore   float64 `json:"min_score"`
	MediaType  string  `json:"media_type"` // "clip" or "stock"
}

// ScriptSuggestRequest represents a request for clip suggestions for an entire script
type ScriptSuggestRequest struct {
	Script             string  `json:"script" binding:"required"`
	MaxResultsPerSentence int  `json:"max_results_per_sentence"`
	MinScore           float64 `json:"min_score"`
	MediaType          string  `json:"media_type"` // "clip" or "stock"
}