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
	LocalPath     string
	CleanedPath   string
	FileHash      string
	DriveLink     string
	DriveFileID   string
	Status        string
	Error         string
	Voice         string // backward-compat: actual TTS voice (e.g. "en-US-RogerNeural") — used by ae20d7bf voiceover
	VoiceProfile  string // Voice profile returned from tts_edge.py (e.g., "en-US-RogerNeural")
}
