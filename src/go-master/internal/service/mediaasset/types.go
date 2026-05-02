package mediaasset

type AssetInput struct {
	ID        string
	Name      string
	SourceURL string
	Term      string
	OutputDir string
	FolderID  string
	Duration  int
	Width     int
	Height    int
	FPS       int
}

type AssetResult struct {
	ID           string
	Filename     string
	LocalPath    string
	FileHash     string
	DriveLink    string
	DownloadLink string
	Status       string
	Error        string
}
