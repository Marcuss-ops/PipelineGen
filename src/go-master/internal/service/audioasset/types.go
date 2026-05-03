package audioasset

import "velox/go-master/internal/service/assetdestination"

type AudioInput struct {
	ID            string
	Text          string
	Language      string
	OutputDir     string
	Filename      string
	RemoveSilence bool
	Destination   *assetdestination.ResolveRequest
}

type AudioResult struct {
	LocalPath    string
	CleanedPath  string
	FileHash     string
	DriveLink    string
	Status       string
	Error        string
}

type DestinationSpec struct {
	FolderID   string
	FolderPath string
}
