// Package voiceover — process.go.
//
// Shared helpers used by the canonical ProcessSegmentUseCase path:
// stageLog emits structured lifecycle telemetry and
// resolveVoiceForLanguage fails closed when the language registry is
// missing or incomplete.
package voiceover

import (
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// stageLog returns a closure that emits pipeline_stage_started now
// and pipeline_stage_completed when invoked. Mirrors the
// scripts/jobs/job_helpers.go canonical emission pattern (Pattern
// 3, AGENTS.md).
//
// FASE 5 (VO-OPERATIONAL-READINESS, July 2026): added assetID and
// project fields per the structured logging contract. Every stage
// now logs: job_id, asset_id, project, language, stage, status,
// duration_ms.
//
// Stage timings are recorded by the canonical observability run when one is
// bound; this helper only emits the structured lifecycle log.
func stageLog(log *zap.Logger, jobID, assetID, project, stage, language string) func(status string) {
	start := time.Now()
	logFields := []zap.Field{
		zap.String("stage", stage),
		zap.String("job_id", jobID),
		zap.String("asset_id", assetID),
		zap.String("language", language),
	}
	if project != "" {
		logFields = append(logFields, zap.String("project", project))
	}
	log.Info("pipeline_stage_started", logFields...)
	return func(status string) {
		dur := time.Since(start)
		completedFields := append(logFields,
			zap.Int64("duration_ms", dur.Milliseconds()),
			zap.String("status", status),
		)
		log.Info("pipeline_stage_completed", completedFields...)
	}
}

// voiceOverrideFor returns the canonical per-language voice override
// for a single language key from a BatchRequest's VoiceOverrides map.
// nil-safe (returns "" when req is nil, the map is nil, the key is
// missing, OR the value is empty). The empty-string return propagates
// downstream to TTSInput.Voice as the default-voice signal.
//
// Moved here from stage_synthesize.go (removed in Azione #1, July 2026).
func voiceOverrideFor(req *BatchRequest, language Language) string {
	if req == nil || len(req.VoiceOverrides) == 0 {
		return ""
	}
	return req.VoiceOverrides[string(language)]
}

// resolveVoiceForLanguage returns the canonical voice for a language.
// Explicit per-request overrides are allowed; all registry-derived voices
// are mandatory. Missing, disabled, or incomplete registry entries fail
// closed instead of delegating voice selection to the TTS bridge.
func resolveVoiceForLanguage(req *BatchRequest, language Language, registry asset.LanguageRegistry, log *zap.Logger) (string, error) {
	if voice := voiceOverrideFor(req, language); voice != "" {
		return voice, nil
	}
	if registry == nil {
		return "", fmt.Errorf("voice registry is not configured for language %q", language)
	}
	spec, ok := registry.Resolve(string(language))
	if !ok {
		return "", fmt.Errorf("voice registry has no entry for language %q", language)
	}
	if !spec.Enabled {
		return "", fmt.Errorf("voice registry disabled language %q", language)
	}
	if !spec.GenerateTTS {
		return "", fmt.Errorf("voice registry disables TTS for language %q", language)
	}
	if spec.EdgeTTSVoice == "" {
		return "", fmt.Errorf("voice registry has no TTS voice for language %q", language)
	}
	return spec.EdgeTTSVoice, nil
}
