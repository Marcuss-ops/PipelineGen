package artlist

// ClipStatusResponse represents the status of a clip
type ClipStatusResponse struct {
	ClipID       string `json:"clip_id"`
	Name         string `json:"name"`
	HasLocalFile bool   `json:"has_local_file"`
	LocalPath    string `json:"local_path"`
	DriveLink    string `json:"drive_link"`
	HasDriveLink bool   `json:"has_drive_link"`
	FileHash     string `json:"file_hash"`
	Source       string `json:"source"`
	ExternalURL  string `json:"external_url"`
}

// DownloadClipRequest represents a download request
type DownloadClipRequest struct {
	OutputDir string `json:"output_dir"`
}

// DownloadClipResponse represents a download response
type DownloadClipResponse struct {
	OK        bool   `json:"ok"`
	ClipID    string `json:"clip_id"`
	LocalPath string `json:"local_path"`
	FileHash  string `json:"file_hash"`
	Error     string `json:"error,omitempty"`
}

// UploadClipToDriveRequest represents an upload to Drive request
type UploadClipToDriveRequest struct {
	FolderID string `json:"folder_id"`
}

// UploadClipToDriveResponse represents an upload response
type UploadClipToDriveResponse struct {
	OK           bool   `json:"ok"`
	ClipID       string `json:"clip_id"`
	DriveLink    string `json:"drive_link"`
	DownloadLink string `json:"download_link"`
	Error        string `json:"error,omitempty"`
}

// ProcessClipRequest represents a process clip request
type ProcessClipRequest struct {
	Term         string `json:"term"`
	ClipID       string `json:"clip_id"`
	AutoDownload bool   `json:"auto_download"`
	AutoUpload   bool   `json:"auto_upload_drive"`
}

// ProcessClipResponse represents a process clip response
type ProcessClipResponse struct {
	OK     bool   `json:"ok"`
	ClipID string `json:"clip_id"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
