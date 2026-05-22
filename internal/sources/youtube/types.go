package youtube

type ExtractRequest struct {
	URL            string              `json:"url"`
	Segments       []Segment           `json:"segments"`
	ForceKeyframes bool                `json:"force_keyframes"`
	Normalize      *bool               `json:"normalize,omitempty"` // Use pointer to distinguish between missing and false
	KeepAudio      bool                `json:"keep_audio"`
	WriteSummary   *bool               `json:"write_summary,omitempty"`
	Strategy       string              `json:"strategy,omitempty"` // verify, skip, replace
	Destination    *DestinationRequest `json:"destination,omitempty"`
}

type TopicSearchRequest struct {
	Q     string `form:"q" json:"q" binding:"required"`
	Limit int    `form:"limit" json:"limit"`
	Sort  string `form:"sort" json:"sort"`
}

type DestinationRequest struct {
	Group           string `json:"group,omitempty"`
	FolderID        string `json:"folder_id,omitempty"`
	FolderPath      string `json:"folder_path,omitempty"`
	SubfolderName   string `json:"subfolder_name,omitempty"`
	CreateSubfolder bool   `json:"create_subfolder,omitempty"`
}

type Segment struct {
	Start string   `json:"start"`
	End   string   `json:"end"`
	Name  string   `json:"name"`
	Tags  []string `json:"tags,omitempty"`
}

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

type FolderInfo struct {
	ID               string `json:"id"`
	LocalFolderPath  string `json:"local_folder_path"`
	DriveFolderID    string `json:"drive_folder_id,omitempty"`
	DriveFolderPath  string `json:"drive_folder_path,omitempty"`
	ManifestTXTPath  string `json:"manifest_txt_path,omitempty"`
	ManifestJSONPath string `json:"manifest_json_path,omitempty"`
}

type ExtractStats struct {
	Requested int `json:"requested"`
	Processed int `json:"processed"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
}

type ExtractItem struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name"`
	Start           string `json:"start"`
	End             string `json:"end"`
	LocalPath       string `json:"local_path,omitempty"`
	DriveLink       string `json:"drive_link,omitempty"`
	DriveFileID     string `json:"drive_file_id,omitempty"`
	DownloadLink    string `json:"download_link,omitempty"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
	DriveFolderID   string `json:"drive_folder_id,omitempty"`
	DriveFolderPath string `json:"drive_folder_path,omitempty"`
}

type VideoMetadata struct {
	ID           string           `json:"id"`
	URL          string           `json:"url"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	Duration     float64          `json:"duration"`
	Uploader     string           `json:"uploader"`
	ThumbnailURL string           `json:"thumbnail_url"`
	Thumbnails   []VideoThumbnail `json:"thumbnails,omitempty"`
	Chapters     []VideoChapter   `json:"chapters,omitempty"`
	Categories   []string         `json:"categories,omitempty"`
	Tags         []string         `json:"tags,omitempty"`
	UploadDate   string           `json:"upload_date,omitempty"`
	ViewCount    int64            `json:"view_count,omitempty"`
}

type VideoChapter struct {
	Title     string  `json:"title"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

type VideoThumbnail struct {
	URL    string `json:"url"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}
