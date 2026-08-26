// Package usecase — text_track_whisper.go is the Whisper-side leaf
// of the text-track 6-file split. It owns ONLY the canonical
// Phase-1.b typed Whisper port invocation
// (WhisperTranscriberPort.TranscribeAudioWithDetection).
//
// AGENTS.md / godlike/06 SSOT split (July 2026): the orchestrator
// (text_track_resolver.go) is the SOLE canonical site for:
//   - asset.Normalize() calls (BCP-47 normalisation post-Whisper)
//   - the empty-DetectedLanguage → "und" fallback decision
//   - the RequireLanguageCertainty policy gate (fires
//     asset.ErrLanguageUndeterminable when Whisper errors AND the
//     policy requires certainty)
//
// This leaf is intentionally a one-helper file so the orchestrator's
// priority-5 path stays declarative without inlining the port call.
// The orchestrator's pre-condition handling (transcriber != nil AND
// audioPath != "") stays in the orchestrator — this leaf only does
// the typed-port call so the chain's signature is stable across
// future port reshaping (Fase 2.b swaps in the
// AudioWithDetectionResult shape).
//
// godlike/07 honest lock: a nil port is propagated as
// (TranscriptResult{}, nil) so the orchestrator can keep its
// fail-closed path without each leaf re-implementing the
// nil-tolerance guard. The empty-result path (Text=="") is left to
// the orchestrator's existing chain logic ("chain exhausted").
package usecase

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// fetchWhisperTranscriptRaw performs the canonical Phase-1.b typed
// Whisper port invocation. The result is the raw TranscriptResult
// (Text + DetectedLanguage + Confidence) — the orchestrator
// normalises DetectedLanguage and composes the ResolvedTextBundle
// provenance, never this leaf.
//
// godlike/07: a nil port is propagated as (TranscriptResult{}, nil)
// so the orchestrator's fail-closed path is preserved. The
// orchestrator remains the SOLE owner of the language-certainty
// policy gate (RequireLanguageCertainty fires
// ErrLanguageUndeterminable pre-Step-9 when Whisper errors).
func fetchWhisperTranscriptRaw(
	ctx context.Context,
	transcriber youtubeports.WhisperTranscriberPort,
	audioPath string,
) (detail.TranscriptResult, error) {
	if transcriber == nil {
		return detail.TranscriptResult{}, nil
	}
	return transcriber.TranscribeAudioWithDetection(ctx, audioPath)
}
