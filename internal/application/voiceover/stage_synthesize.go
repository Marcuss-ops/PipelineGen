// Package voiceover — stage_synthesize.go (PR-VO-STAGES-SPLIT, P0 #2 in
// VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-01).
//
// Stage 1 of the 5-stage voiceover pipeline: TTS synthesis via
// s.ttsProvider.Synthesize. Populates BatchItem.LocalPath,
// CleanedPath, Voice, FileHash on success. Returns
// item.fail(FailureTTSProviderUnavailable, …) when the composition
// root didn't wire ttsProvider, and item.fail(FailureTTS, err) on
// synthesize failure.
//
// Mechanical extraction from the pre-split stages.go (which
// carried 3 stages in 628 LoC). No behavior change in EXPAND.
// Compile-time lock: process_voiceover_item.go reads the same
// TTSInput / TTSProvider / Language types via the ports package —
// preserved verbatim.
package voiceover

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// voiceOverrideFor returns the canonical per-language voice override
// for a single language key from a BatchRequest's VoiceOverrides map.
// nil-safe (returns "" when req is nil, the map is nil, the key is
// missing, OR the value is empty). The empty-string return propagates
// downstream to TTSInput.Voice as the default-voice signal — the
// Python tts_edge.py --voice flag is only set when the override is
// present, so an empty Voice preserves the tts script's local
// voice-per-language defaulting path.
//
// PR-VO-AUDIT-P04 micro-commit #3 (June 2026): replaces the previous
// synthesizeStage hard-coded `Voice: ""` literal that dropped every
// per-language override silently. Audit-pin:
//   - TestProcessOneVoiceoverUseCase_PropagatesVoiceOverrideToTTSInput
//     (asserts end-to-end propagation from the item's scalar voice
//     through req.VoiceOverrides into TTSInput.Voice);
//   - TestTTSBridge_UsesPerLanguageVoice (asserts the synthesizeStage
//     lookup hits the canonical map);
//   - TestE2E_VoiceOverrideReachesPython (asserts the resolved voice
//     flows through to the Python tts_edge.py --voice flag).
//
// PR-VO-TYPED-PRIMITIVES (July 2026): language is the typed
// BCP-47 envelope. The map key is still raw string (the wire
// shape for BatchRequest.VoiceOverrides is map[string]string so
// the JSON unmarshal does not break on a typed key); the typed
// lookup is a string() conversion at the boundary.
func voiceOverrideFor(req *BatchRequest, language Language) string {
	if req == nil || len(req.VoiceOverrides) == 0 {
		return ""
	}
	return req.VoiceOverrides[string(language)]
}

// synthesizeStage is Stage 1 (TTS via ttsProvider). Wired
// between the stageLog("synthesize") wrappers in process.go.
//
// RESTORED body: invokes s.ttsProvider.Synthesize with the
// canonical TTSInput shape. On success populates LocalPath,
// CleanedPath, Voice, FileHash on the BatchItem. On error the
// item.fail plumbing surfaces a BatchItem with Status=StatusFailed
// for the caller to observe.
//
// Note: process.go's processLanguage calls this inside stageLog
// telemetry — durations and errors are logged at the stage-wrapper
// level, so this method only NEEDS to surface errors, not durations.
//
// PR-VO-TYPED-PRIMITIVES (July 2026): language is the typed
// BCP-47 envelope.
func (s *Service) synthesizeStage(
	ctx context.Context,
	item BatchItem,
	req *BatchRequest,
	outputDir string,
	filename string,
	language Language,
) BatchItem {
	if s.ttsProvider == nil {
		return item.fail(FailureTTSProviderUnavailable,
			fmt.Errorf("%s: ttsProvider not wired (composition root)", restoreIdent))
	}

	removeSilence := false
	if req.RemoveSilence != nil {
		removeSilence = *req.RemoveSilence
	}

	// TTSInput is the canonical voiceover port wire-shape (defined
	// in voiceover/ports.go). The useCaseTTSAdapter bridge (in
	// internal/app/adapters_voiceover_use_case.go) maps TTSInput
	// fields 1-a-1 onto audioasset.AudioInput so the production
	// *audioasset.Processor receives the same shape it would have
	// received pre-P1-2.
	//
	// PR-VO-AUDIT-P04 micro-commit #3 (June 2026): the Voice field is
	// populated from the canonical req.VoiceOverrides[language] lookup
	// (via voiceOverrideFor helper at the top of this file). nil-safe:
	// voiceOverrideFor returns "" when the map is missing or the
	// language key is missing, which propagates downstream to the
	// tts_edge.py --voice flag as the default-voice path. Pre-P0.4
	// this lookup was missing — the legacy code hard-coded
	// `Voice: ""`, so per-language voice overrides in
	// req.VoiceOverrides were silently dropped at Stage 1 before
	// reaching the Python bridge.
	input := TTSInput{
		Text:          req.Text,
		Language:      language,
		Voice:         voiceOverrideFor(req, language),
		Filename:      filename,
		OutputDir:     outputDir,
		RemoveSilence: removeSilence,
	}

	result, err := s.ttsProvider.Synthesize(ctx, input)
	if err != nil {
		if s.log != nil {
			s.log.Warn("synthesizeStage: TTS failed",
				zap.String("restored", restoreIdent),
				zap.String("language", string(language)),
				zap.Error(err))
		}
		return item.fail(FailureTTS, err)
	}

	item.LocalPath = result.LocalPath
	item.CleanedPath = result.CleanedPath
	item.Voice = result.Voice
	item.FileHash = result.FileHash
	item.Status = StatusGenerated
	return item
}
