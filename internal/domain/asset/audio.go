// Package asset defines the canonical contract for TTS generation that
// the voiceover service consumes. The concrete implementation lives in
// internal/infrastructure/audio.
//
// Architecture (per AGENTS.md split app/infrastructure):
//
//   - application/voiceover/Service holds an AudioProcessor field
//     (interface, not concrete). Wiring is done in internal/app/.
//   - infrastructure/audio/processor.go owns the os/exec subprocess
//     invocation of bridges/tts_edge.py plus the post-TTS pipeline
//     (silence removal via ffmpeg, hash, Drive upload).
//   - domain/asset/audio.go owns the contract: minimal signature that
//     hides the implementation choice (Python sidecar today, could be
//     a hosted TTS API tomorrow).
package asset

import "context"

// AudioInput is the canonical input for TTS generation.
type AudioInput struct {
	Text          string
	Language      string
	Voice         string
	Speed         float64
	Filename      string
	OutputDir     string
	OutputPath    string
	Destination   *ResolveRequest
	Strategy      string // "replace", "skip", "fail"
	RemoveSilence bool
}

// AudioResult is the canonical output of TTS generation.
type AudioResult struct {
	LocalPath   string
	CleanedPath string
	FilePath    string
	FileHash    string
	DurationMs  int64
	DriveLink   string
	DriveFileID string
	Status      string
	Error       string
}

// AudioProcessor is the canonical contract for TTS generation. The
// signature matches the concrete infrastructure implementation's
// Generate method (input/output via pointer wrappers, ctx for
// cancellation). New implementations (e.g. a hosted-TTS adapter) MUST
// satisfy this signature; the voiceover service depends on the
// interface, not on the concrete subprocess impl.
type AudioProcessor interface {
	Generate(ctx context.Context, input *AudioInput) (*AudioResult, error)
}
