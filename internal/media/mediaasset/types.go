package mediaasset

type AssetInput struct {
	ID        string
	Name      string
	SourceURL string
	Term      string
	OutputDir string
	Filename  string
	FolderID  string
	Duration  int
	FPS       int
	// Download options
	DownloadSections []string
	ForceKeyframes   bool
	StreamCopy       bool
	// Normalize options
	Normalize       *bool
	KeepAudio       bool
	DisableDuration bool
	// ClipPageURL is the original page URL for browser-based download (e.g., Artlist clip page)
	ClipPageURL string
	// Metadata
	Metadata map[string]any
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
	DuplicateOf  string
}
