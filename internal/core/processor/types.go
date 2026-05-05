package processor

import "context"

// Processor is the canonical interface for processing media assets.
// This is a simplified version of mediaasset.MediaProcessor for core usage.
type Processor interface {
	// Process downloads, processes, and uploads an asset.
	Process(ctx context.Context, input *ProcessInput) (*ProcessResult, error)
}

// ProcessInput contains the input for processing an asset.
type ProcessInput struct {
	ID        string
	Name      string
	SourceURL string
	Term      string
	OutputDir string
	FolderID  string
	Duration  int
	Width     int
	Height    int
	Metadata  map[string]interface{}
}

// ProcessResult contains the result of processing an asset.
type ProcessResult struct {
	ID           string
	Filename     string
	LocalPath    string
	FileHash     string
	DriveLink    string
	DownloadLink string
	Status       string
	Error        string
}
