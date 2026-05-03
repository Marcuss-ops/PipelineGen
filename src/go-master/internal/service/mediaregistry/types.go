package mediaregistry

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
	DownloadLink  string
	FileHash      string
	Metadata      string
	Duration      int
	Tags          []string
	Status        string
	Error         string
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
