package assetregistry

type MediaRecord struct {
	ID            string
	Name          string
	Filename      string
	Source        string
	Category      string
	MediaType     string
	ExternalURL   string
	FolderID      string
	FolderPath    string
	Group         string
	LocalPath     string
	DriveLink     string
	DriveFileID   string
	DownloadLink  string
	FileHash      string
	ContentHash   string
	Metadata      string
	Duration      int
	Tags          []string
	Status        string
	Error         string
	// Asset index fields
	SourceID      string
	Subfolder     string
	PHash         string
	VisualEmbeddingJSON string
}

type FinalizeOptions struct {
	RequireLocal bool
	RequireHash  bool
	RequireDrive bool
	VerifyDB     bool
}

type FinalizeResult struct {
	OK            bool
	Status        string
	DBSaved       bool
	LocalExists   bool
	DriveUploaded bool
	Error         string
	Record        *MediaRecord
}
