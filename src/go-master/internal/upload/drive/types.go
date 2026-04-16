// Package drive provides types for Google Drive integration.
package drive

import "time"

// Folder represents a Google Drive folder
type Folder struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Link        string    `json:"link,omitempty"`
	Parents     []string  `json:"parents,omitempty"`
	Depth       int       `json:"depth,omitempty"`
	Subfolders  []Folder  `json:"subfolders,omitempty"`
	CreatedTime time.Time `json:"created_time,omitempty"`
}

// File represents a Google Drive file
type File struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	MimeType     string    `json:"mime_type"`
	Link         string    `json:"link,omitempty"`
	Size         int64     `json:"size,omitempty"`
	ModifiedTime time.Time `json:"modified_time,omitempty"`
	CreatedTime  time.Time `json:"created_time,omitempty"`
	Parents      []string  `json:"parents,omitempty"`

	// Video properties (populated for video files)
	DurationMs int64  `json:"duration_ms,omitempty"`
	Width      int64  `json:"width,omitempty"`
	Height     int64  `json:"height,omitempty"`
}

// Doc represents a Google Docs document
type Doc struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content,omitempty"`
}

// UploadSession represents a resumable upload session
type UploadSession struct {
	SessionID string `json:"session_id"`
	FileID    string `json:"file_id,omitempty"`
	FileName  string `json:"file_name"`
	Progress  int64  `json:"progress"` // bytes uploaded
	Total     int64  `json:"total"`    // total bytes
	Status    string `json:"status"`   // "pending", "uploading", "completed", "failed"
}

// UploadStatus represents upload progress
type UploadStatus struct {
	SessionID string `json:"session_id"`
	FileID    string `json:"file_id,omitempty"`
	FileName  string `json:"file_name"`
	Progress  int64  `json:"progress"`
	Total     int64  `json:"total"`
	Percent   int    `json:"percent"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// FolderContent represents the content of a folder
type FolderContent struct {
	FolderID      string   `json:"folder_id"`
	FolderName    string   `json:"folder_name,omitempty"`
	Subfolders    []Folder `json:"subfolders"`
	Files         []File   `json:"files"`
	TotalFolders  int      `json:"total_folders"`
	TotalFiles    int      `json:"total_files"`
}

// ListFoldersOptions options for listing folders
type ListFoldersOptions struct {
	ParentID  string `json:"parent_id,omitempty"`
	MaxDepth  int    `json:"max_depth"`
	MaxItems  int    `json:"max_items"`
	OrderBy   string `json:"order_by,omitempty"`
}

// CreateFolderRequest request for creating a folder
type CreateFolderRequest struct {
	Name     string `json:"name" binding:"required,min=1"`
	ParentID string `json:"parent_id,omitempty"`
}

// CreateDocRequest request for creating a Google Doc
type CreateDocRequest struct {
	Title    string `json:"title" binding:"required,min=1"`
	Content  string `json:"content,omitempty"`
	FolderID string `json:"folder_id,omitempty"`
}

// AppendDocRequest request for appending to a Google Doc
type AppendDocRequest struct {
	DocID   string `json:"doc_id,omitempty"`
	DocURL  string `json:"doc_url,omitempty"`
	Content string `json:"content" binding:"required,min=1"`
}

// UploadClipRequest request for uploading a clip
type UploadClipRequest struct {
	Topic      string                   `json:"topic" binding:"required,min=1"`
	VideoURL   string                   `json:"video_url,omitempty"`
	VideoTitle string                   `json:"video_title,omitempty"`
	Moments    []ClipMoment             `json:"moments" binding:"required,min=1"`
	Group      string                   `json:"group,omitempty"`
}

// ClipMoment represents a clip moment
type ClipMoment struct {
	Start    string  `json:"start" binding:"required,min=1"`
	End      string  `json:"end" binding:"required,min=1"`
	Text     string  `json:"text" binding:"required,min=1"`
	Duration float64 `json:"duration"`
	Score    float64 `json:"score"`
}

// UploadClipSimpleRequest simple clip upload request
type UploadClipSimpleRequest struct {
	Text      string `json:"text" binding:"required,min=1"`
	Timestamp string `json:"timestamp" binding:"required,min=1"`
	Topic     string `json:"topic"`
}

// FolderStructureRequest request for creating folder structure
type FolderStructureRequest struct {
	Topic string `json:"topic" binding:"required,min=1"`
	Group string `json:"group,omitempty"`
}

// DownloadUploadClipRequest request for downloading from YouTube and uploading to Drive
type DownloadUploadClipRequest struct {
	YouTubeURL string `json:"youtube_url" binding:"required,url"`
	Topic      string `json:"topic"`
	StartTime  string `json:"start_time" binding:"required,min=1"`
	EndTime    string `json:"end_time" binding:"required,min=1"`
	Group      string `json:"group,omitempty"`
}

// StockDriveGroups predefined groups for stock clips
var StockDriveGroups = map[string]string{
	"elon_musk":  "Elon Musk",
	"tech":       "Tech & AI",
	"business":   "Business",
	"interview":  "Interviews",
	"podcast":    "Podcasts",
	"news":       "News",
	"science":    "Science",
	"default":    "Stock Footage",
}