// Package asset — transcript_result.go: canonical typed Whisper
// transcription output (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 1.b, July 2026).
//
// godlike/06 SSOT (one canonical owner per fact):
//   - TranscriptResult lives here (domain layer) so the application-
//     layer port (internal/capabilities/youtube/ports/ports.go) and
//     the infrastructure-layer port (internal/infrastructure/youtube/ports.go)
//     BOTH reference the same type without import cycles. The
//     godlike/06 SSOT invariant (one canonical owner per fact) is
//     preserved: a future drift on the Whisper output shape is
//     caught at compile-time via the typed-port assertions on both
//     layers.
//
// godlike/07 no-fake-availability: DetectedLanguage is normalized
// to BCP-47 by the concrete Whisper adapter; empty/unknown input
// collapses to "und" (BCP-47 undetermined) per the canonical
// bcp47.Normalize contract. Callers MUST treat "und" as the
// known-undetermined signal and apply the
// media.multilingual.require_language_certainty policy gate.
package detail

// TranscriptResult is the structured Whisper transcription output.
// The plain string-only return of the legacy TranscribeAudio method
// could not surface the detected language or the per-cue confidence,
// so a typed-result sibling method (TranscribeAudioWithDetection) is
// added to both the application-layer WhisperTranscriberPort and
// the infrastructure-layer WhisperTranscriber interface.
type TranscriptResult struct {
	// Text is the canonical transcript plaintext (post-language-
	// detection). Empty when the model returned an empty
	// transcription (still a non-error signal — caller falls
	// through to the next resolver priority).
	Text string
	// DetectedLanguage is the BCP-47 language code Whisper
	// detected for the audio. Empty string means "model did not
	// report a language"; the concrete adapter MUST collapse
	// unknown/empty to "und" via the canonical bcp47.Normalize
	// helper (per godlike/07 no-fake-availability).
	DetectedLanguage string
	// Confidence is the per-clip average probability [0.0, 1.0].
	// nil when the underlying model does not report one. The
	// text-track hash factory (text_track_hashes.go::SourceVersion)
	// does NOT consume this field today; future Fase 3 + Fase 4
	// materializer workers may surface it as a quality-gate input.
	Confidence *float64
	// DurationMs is the duration of the audio in milliseconds.
	DurationMs int64
	// Cues is the list of timestamped subtitle segments.
	Cues []TimedCue
}
