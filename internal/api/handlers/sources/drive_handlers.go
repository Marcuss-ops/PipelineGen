package sources

// DeleteDriveFileRequest represents a request to delete/trash a clip by Drive file ID or link.
type DeleteDriveFileRequest struct {
	Source    string `json:"source,omitempty"`
	DriveLink string `json:"drive_link"`
	FileID    string `json:"file_id"`
	DryRun    bool   `json:"dry_run"`
	Confirm   bool   `json:"confirm"`
	Mode      string `json:"mode"` // "trash" or "delete"
}

// DeleteDriveFileResult represents the result of a drive file delete/trash operation.
type DeleteDriveFileResult struct {
	OK           bool   `json:"ok"`
	Source       string `json:"source,omitempty"`
	ClipID       string `json:"clip_id,omitempty"`
	FileID       string `json:"file_id"`
	DriveLink    string `json:"drive_link,omitempty"`
	FoundDB      bool   `json:"found_db"`
	DryRun       bool   `json:"dry_run"`
	Action       string `json:"action,omitempty"`
	DriveDeleted bool   `json:"drive_deleted,omitempty"`
	DBDeleted    bool   `json:"db_deleted,omitempty"`
	Error        string `json:"error,omitempty"`
}
