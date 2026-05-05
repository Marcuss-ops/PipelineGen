package mediaasset

type AssetInput struct {
	ID               string
	Name             string
	SourceURL         string
	Term             string
	OutputDir         string
	Filename         string
	FolderID         string
	Duration         int
	FPS              int
	// Download options
	DownloadSections []string
	ForceKeyframes    bool
	// Normalize options
	Normalize      *bool
	KeepAudio      bool
	DisableDuration bool
	// Metadata
	Metadata map[string]interface{}
}

type AssetResult struct {
	ID           string
	Filename     string
	LocalPath    string
	FileHash     string
	DriveLink    string
	DriveFileID  string
	DownloadLink string
	Status       string
	Error        string
}
