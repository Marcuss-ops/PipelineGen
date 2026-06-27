package audioasset

import "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"

type AudioInput struct {
	Text          string
	Language      string
	Voice         string // optional: overrides auto-detected voice (passed as --voice)
	Filename      string
	OutputDir     string
	Destination   *asset.ResolveRequest
	Strategy      string // "replace", "skip", "fail"
	RemoveSilence bool
	UseStdin      bool   // pipe text via stdin instead of --text (avoids OS arg limits)
}

type AudioResult struct {
	LocalPath   string
	CleanedPath string
	FileHash    string
	DriveLink   string
	DriveFileID string
	Voice       string // the actual TTS voice used (e.g. "en-US-RogerNeural")
	Status      string
	Error       string
}
