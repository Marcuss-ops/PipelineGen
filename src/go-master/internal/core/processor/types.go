package processor

import "context"

// Processor is the canonical interface for processing media assets.
// This matches mediaasset.MediaProcessor for unified usage.
type Processor interface {
	// Process downloads, processes, and uploads an asset.
	Process(ctx context.Context, input *ProcessInput) (*ProcessResult, error)
}

// ProcessInput contains the input for processing an asset.
type ProcessInput struct {
	ID               string
	Name             string
	SourceURL         string
	Term             string
	OutputDir         string
	Filename         string
	FolderID         string
	Duration         int
	ForceKeyframes    bool
	DownloadSections  []string
	Normalize         *bool
	KeepAudio         bool
	DisableDuration   bool
	Width            int
	Height           int
	Metadata         map[string]interface{}
}

// ProcessResult contains the result of processing an asset.
type ProcessResult struct {
	ID           string
	Filename     string
	LocalPath    string
	FileHash     string
	ContentHash  string
	DriveLink    string
	DownloadLink string
	Status       string
	Error        string
}
