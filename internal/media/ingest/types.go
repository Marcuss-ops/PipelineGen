package ingest

type Kind string

const (
	KindImage     Kind = "image"
	KindVoiceover Kind = "voiceover"
	KindClip      Kind = "clip"
	KindStock     Kind = "stock"
)

type Request struct {
	Kind         string         `json:"kind"`
	Name         string         `json:"name"`
	Filename     string         `json:"filename"`
	Source       string         `json:"source"`
	SourceID     string         `json:"source_id"`
	Group        string         `json:"group"`
	Subfolder    string         `json:"subfolder"`
	FolderID     string         `json:"folder_id"`
	FolderPath   string         `json:"folder_path"`
	LocalPath    string         `json:"local_path"`
	URL          string         `json:"url"`
	DriveLink    string         `json:"drive_link"`
	DriveFileID  string         `json:"drive_file_id"`
	DownloadLink string         `json:"download_link"`
	Duration     int            `json:"duration"`
	Tags         []string       `json:"tags"`
	Metadata     map[string]any `json:"metadata"`
}

type Result struct {
	OK               bool           `json:"ok"`
	Status           string         `json:"status"`
	Kind             string         `json:"kind"`
	ID               string         `json:"id"`
	Source           string         `json:"source"`
	SourceID         string         `json:"source_id"`
	Name             string         `json:"name"`
	Filename         string         `json:"filename"`
	FolderID         string         `json:"folder_id"`
	FolderPath       string         `json:"folder_path"`
	LocalPath        string         `json:"local_path"`
	DriveLink        string         `json:"drive_link"`
	DriveFileID      string         `json:"drive_file_id"`
	DownloadLink     string         `json:"download_link"`
	FileHash         string         `json:"file_hash"`
	ContentHash      string         `json:"content_hash"`
	SkippedDuplicate bool           `json:"skipped_duplicate"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}
