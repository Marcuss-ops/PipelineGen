package audioasset

import "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"

type AudioInput struct {
	Text          string
	Language      string
	Voice         string
	Filename      string
	OutputDir     string
	Destination   *asset.ResolveRequest
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
