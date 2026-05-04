package assetpipeline

type AssetKind string

const (
	AssetKindVideo AssetKind = "video"
	AssetKindAudio AssetKind = "audio"
	AssetKindImage AssetKind = "image"
	AssetKindDocument AssetKind = "document"
)

type FinalizeInput struct {
	ID          string
	Name        string
	Filename    string
	Kind        AssetKind
	Source      string
	SourceID    string
	Group       string
	Subfolder   string

	LocalPath   string
	FolderID    string
	FolderPath  string

	DriveLink    string
	DownloadLink string

	Metadata string

	RequireLocal bool
	RequireHash  bool
	RequireDrive bool
	VerifyDB     bool
}

type FinalizeResult struct {
	OK           bool
	Status       string
	FileHash     string
	ContentHash  string
	DriveLink    string
	DownloadLink string
	LocalPath    string
	Error        string
}
