package youtubeclip

type ExtractRequest struct {
	URL            string             `json:"url"`
	Segments       []Segment          `json:"segments"`
	ForceKeyframes bool               `json:"force_keyframes"`
	SaveDB         bool               `json:"save_db"`
	UploadDrive    bool               `json:"upload_drive"`
	Destination    *DestinationRequest `json:"destination,omitempty"`
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
	OK              bool           `json:"ok"`
	SourceURL       string         `json:"source_url"`
	Items           []ExtractItem `json:"items"`
	Error           string         `json:"error,omitempty"`
	DriveFolderID   string         `json:"drive_folder_id,omitempty"`
	DriveFolderPath string         `json:"drive_folder_path,omitempty"`
}

type ExtractItem struct {
	Name           string `json:"name"`
	Start          string `json:"start"`
	End            string `json:"end"`
	LocalPath      string `json:"local_path,omitempty"`
	DriveLink      string `json:"drive_link,omitempty"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	DriveFolderID  string `json:"drive_folder_id,omitempty"`
	DriveFolderPath string `json:"drive_folder_path,omitempty"`
}
