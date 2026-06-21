package asset

import "context"

// Processor is the canonical interface for processing media assets.
// Concrete media processors implement this contract directly; adapters and
// package-local input/result mirrors are intentionally forbidden.
type Processor interface {
	// Process downloads, processes, and uploads an asset.
	Process(ctx context.Context, input *ProcessInput) (*ProcessResult, error)
}

// ProcessInput contains the input for processing an asset.
type ProcessInput struct {
	ID               string
	Name             string
	SourceURL        string
	Term             string
	OutputDir        string
	Filename         string
	FolderID         string
	Duration         int
	ForceKeyframes   bool
	StreamCopy       bool
	DownloadSections []string
	Normalize        *bool
	KeepAudio        bool
	DisableDuration  bool
	Width            int
	Height           int
	DriveFileID      string
	ClipPageURL      string
	Metadata         map[string]any
}

// ProcessResult contains the result of processing an asset.
type ProcessResult struct {
	ID           string
	Filename     string
	LocalPath    string
	FileHash     string
	ContentHash  string
	DriveLink    string
	DriveFileID  string
	DownloadLink string
	Status       string
	Error        string
	DuplicateOf  string
}
