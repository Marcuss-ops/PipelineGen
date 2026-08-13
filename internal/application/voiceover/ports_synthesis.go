package voiceover

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// ────────────────────────────────────────────────────────────────────────
// Synthesis territory — text-to-speech + post-processing.
// ────────────────────────────────────────────────────────────────────────
//
// These ports carry ZERO database/sql, drive, or qdrant imports.
// Production concretes satisfy them by structural conformance (Go's
// implicit interface rules).

// TTSProvider is the canonical port for text-to-speech synthesis.
// The production concrete is *audioasset.Processor (lowered from
// internal/infrastructure/audio so voiceover never imports the
// infrastructure package directly).
type TTSProvider interface {
	Synthesize(ctx context.Context, input TTSInput) (TTSOutput, error)
}

// TTSInput is the canonical wire-shape the use case passes to
// TTSProvider.Synthesize. Mirrors audioasset.AudioInput fields so a
// future thin adapter is a one-line forward.
type TTSInput struct {
	Text string
	// Language is the typed BCP-47 envelope (voiceover.Language).
	// The cross-package seam (useCaseTTSAdapter at
	// internal/app/adapters_voiceover_use_case.go) converts to
	// the raw string when forwarding to audioasset.AudioInput.
	Language      Language
	Voice         string
	Filename      string
	OutputDir     string
	RemoveSilence bool
	// Timing is the canonical voiceover timing policy. nil means the
	// provider applies the canonical defaults (best_effort / word /
	// [json]) — timing capture is never implicitly mandatory.
	Timing *audio.TimingRequest
}

// RawSpeechBoundary is one provider-neutral word boundary in integer
// microseconds. Providers hand these over in TTSOutput; the application
// layer is responsible for building the canonical SpeechTimingArtifact
// (hash-bound to the final audio), never the provider.
type RawSpeechBoundary struct {
	Text    string
	StartUS int64
	EndUS   int64
}

// TTSOutput is the canonical return shape.
type TTSOutput struct {
	LocalPath   string
	CleanedPath string
	Voice       string
	FileHash    string
	Duration    time.Duration

	// Provider is the canonical provider identifier (e.g. "edge_tts")
	// that produced this output. Empty when the provider does not
	// declare an identity.
	Provider string
	// BoundaryMode is the boundary granularity captured by the
	// provider. Zero (empty) means no timing was captured.
	BoundaryMode audio.BoundaryMode
	// WordBoundaries are the RAW provider boundaries in microseconds.
	// The canonical artifact (hashes + monotonic validation) is built
	// by the use case, not the provider.
	WordBoundaries []RawSpeechBoundary
}

// AudioPostProcessor is the canonical port for post-TTS audio cleanup
// (silence removal via ffmpeg). Nil-safe at the use case boundary —
// only invoked when cmd.RemoveSilence == true.
type AudioPostProcessor interface {
	Process(ctx context.Context, input AudioPostInput) (AudioPostOutput, error)
}

// AudioPostInput is the canonical input shape.
type AudioPostInput struct {
	LocalPath string
	OutputDir string
	Filename  string
}

// AudioPostOutput carries the cleaned-path surface.
type AudioPostOutput struct {
	CleanedPath string
}
