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
	UseStdin      bool // pipe text via stdin instead of --text (avoids OS arg limits)
}

// AudioResult carries the outcome of a single TTS generation.
// PR-VO-A1 (June 2026, Lost Voice): the canonical TTS voice identifier
// returned by scripts/bridges/tts_edge.py lives in `Voice` only (captured
// in processor.go from the bridge's stdout JSON). Consumers MUST read
// Voice — no parallel fields.
type AudioResult struct {
	LocalPath   string
	CleanedPath string
	FileHash    string
	DriveLink   string
	DriveFileID string
	Status      string
	Error       string
	// Voice is the canonical TTS voice returned by scripts/bridges/tts_edge.py
	// (e.g. "en-US-RogerNeural"). Empty when the bridge returned no voice.
	Voice string
}
