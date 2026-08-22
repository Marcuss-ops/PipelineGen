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
	LegacyFileMD5    string
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

// AudioPostOutput carries the cleaned-path surface plus the silence-
// removal edit map. When EditMap is populated, raw TTS word boundaries
// (which refer to the ORIGINAL timeline) can be remapped onto the
// cleaned timeline so timing stays accurate after silence removal.
type AudioPostOutput struct {
	CleanedPath string

	// DurationUS is the FINAL cleaned audio duration in microseconds.
	// Required (together with EditMap) to build a valid timing artifact
	// after silence removal.
	DurationUS int64

	// EditMap describes the silence-removal edits in source→cleaned
	// timeline terms (see audio.AudioEdit). Empty when the processor
	// does not report edits — timing capture then degrades per policy
	// (required fails closed, best-effort marks timing unavailable).
	EditMap []audio.AudioEdit
}

// SilenceCleanupReport is the observability summary of post-TTS silence
// removal for one voiceover: the original (pre-clean) duration, the leading
// and trailing trims, and the resulting clean duration. The renderer never
// consumes it — it exists so operators can verify that the timeline uses the
// cleaned duration rather than a clip-derived or pre-clean value.
type SilenceCleanupReport struct {
	OriginalDurationUS int64 `json:"original_duration_us"`
	TrimStartUS        int64 `json:"trim_start_us"`
	TrimEndUS          int64 `json:"trim_end_us"`
	CleanDurationUS    int64 `json:"clean_duration_us"`
}

// BuildSilenceCleanupReport summarizes a silence-removal edit map into the
// four-field report. Leading/trailing trims are derived from the edits
// (an edit at source 0 is the leading trim; an edit ending at the original
// duration is the trailing trim). Returns nil when no edits were reported.
func BuildSilenceCleanupReport(originalUS, cleanUS int64, edits []audio.AudioEdit) *SilenceCleanupReport {
	if len(edits) == 0 {
		return nil
	}
	report := &SilenceCleanupReport{
		OriginalDurationUS: originalUS,
		CleanDurationUS:    cleanUS,
	}
	for _, e := range edits {
		removed := e.SourceEndUS - e.SourceStartUS
		if removed <= 0 {
			continue
		}
		if e.SourceStartUS == 0 {
			report.TrimStartUS += removed
		}
		if e.SourceEndUS == originalUS {
			report.TrimEndUS += removed
		}
	}
	return report
}
