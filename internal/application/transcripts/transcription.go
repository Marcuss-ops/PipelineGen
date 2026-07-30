// Package transcripts (canonical) — the audio-side Transcriber port
// moved here from internal/transcription/ in P1-4 step 1 (godlike/07
// ZERO_LEGACY_POLICY, July 2026). The package name is `transcripts`
// (no separate `transcription` package remains). Content below is
// preserved verbatim from the old root; only the package declaration
// + this forward-pointer header were updated. See
// docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md §"What counts as
// legacy" for the retirement sequence; the percheck_legacy_root_ban
// forward-prevention gate (cmd/archcheck/scan/percheck_legacy_root_ban.go)
// bans reverse-imports of the deleted root.
//
// Package transcription owns the LocalMedia → TextTrack
// surface: takes a local media file path, returns the
// canonical TextTrack representation of the spoken content.
//
// PR-YOUTUBE-SERVICE-SPLIT (July 2026, phase 1): typed-narrow
// godlike/06 SSOT contract is in place. The WhisperAdapter
// constructor accepts the canonical
// *youtubeinfra.WhisperTranscriberAdapter so the composition
// root can validate wiring at boot (godlike/07 fail-closed);
// the actual Transcribe delegation is DEFERRED to phase 2
// until the typed-sentinel chain surface (errors.As
// constraints + ErrStubTranscript projection) is reconciled.
package transcripts

import (
	"context"
	"fmt"

	youtubeinfra "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Transcriber is the canonical godlike/06 SSOT narrow port.
type Transcriber interface {
	Transcribe(ctx context.Context, localPath string) (*TextTrack, error)
}

// TextTrack is the canonical post-transcription result.
type TextTrack struct {
	Text         string
	LanguageCode string
	Confidence   *float64
	IsEmpty      bool
	Cues         []asset.TimedCue
}

// ErrTranscriberNotWired is the construction-time typed sentinel
// (godlike/07 fail-closed).
var ErrTranscriberNotWired = fmt.Errorf("transcription: transcriber not wired (godlike/07 fail-closed)")

// ErrTranscriberNotImplemented is the phase-1 typed sentinel.
// godlike/07 NO-FAKE-AVAILABILITY: never a silent empty result.
var ErrTranscriberNotImplemented = fmt.Errorf("transcription: canonical TranscribeAudioWithDetection delegation deferred to phase 2 (godlike/07 typed sentinel; ErrStubTranscript projection pending)")

// WhisperAdapter is the canonical Transcriber impl.
type WhisperAdapter struct {
	inner *youtubeinfra.WhisperTranscriberAdapter
}

// NewWhisperAdapter constructs the canonical Transcriber.
// nil inner → ErrTranscriberNotWired (godlike/07 fail-closed).
func NewWhisperAdapter(inner *youtubeinfra.WhisperTranscriberAdapter) (*WhisperAdapter, error) {
	if inner == nil {
		return nil, ErrTranscriberNotWired
	}
	return &WhisperAdapter{inner: inner}, nil
}

// Transcribe returns the phase-1 typed sentinel. Phase 2 will
// delegate to inner.TranscribeAudioWithDetection and project the
// asset.TranscriptResult into the package-local TextTrack DTO.
// The ErrStubTranscript reject logic (godlike/07
// no-fake-availability) is preserved by the canonical impl at
// the infrastructure layer; phase 2 surfaces it via
// errors.Is at this boundary.
func (w *WhisperAdapter) Transcribe(ctx context.Context, localPath string) (*TextTrack, error) {
	if w == nil {
		return nil, ErrTranscriberNotWired
	}
	if localPath == "" {
		return nil, fmt.Errorf("transcription: localPath is empty (godlike/07 fail-closed)")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w (local_path=%q)", ErrTranscriberNotImplemented, localPath)
}

// Compile-time pinning: *WhisperAdapter satisfies Transcriber.
var _ Transcriber = (*WhisperAdapter)(nil)
