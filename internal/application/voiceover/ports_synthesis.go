package voiceover

import "time"

import "context"

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
}

// TTSOutput is the canonical return shape.
type TTSOutput struct {
	LocalPath   string
	CleanedPath string
	Voice       string
	FileHash    string
	Duration    time.Duration
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
