package audioasset

import "velox/go-master/internal/core/destination"

type AudioInput struct {
	Text          string
	Language      string
	Voice         string
	Filename      string
	OutputDir     string
	Destination   *destination.ResolveRequest
	Strategy      string // "replace", "skip", "fail"
	RemoveSilence bool
}

type AudioResult struct {
	LocalPath   string
	CleanedPath string
	FileHash    string
	DriveLink   string
	DriveFileID string
	Status      string
	Error       string
}

type DestinationSpec struct {
	FolderID   string
	FolderPath string
}
