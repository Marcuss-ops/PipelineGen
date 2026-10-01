package scriptgeneration

import (
	"context"
	"strings"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// audioCompileStage is the canonical observability stage under which the
// combined-audio subtimings are recorded as operations. It mirrors the
// AUDIO_COMPILE execution step: the step is the business/orchestration phase,
// and each subtiming below it is a technical operation. The literal is owned
// by internal/kernel/observability (registry.go) and aliased here — a phase
// name is a wire fact, so a second spelling is a silent drift hazard.
//
// It is carried as a plain string (like the report lookups that consume it);
// sites that measure on the canonical clock pass kernobs.StageName(stage).
const audioCompileStage = string(kernobs.StageRunAudioCompile)

// voiceoverStage mirrors the VOICEOVER execution step and hosts the
// owner-measured TTS subtiming. Same ownership rule as audioCompileStage.
const voiceoverStage = string(kernobs.StageRunVoiceover)

// recordAudioOperation appends one owner-measured audio subtiming as an
// OperationReport under the audio_compile stage. The duration comes from the
// canonical AudioPipelineMetrics field and was measured by its owner (the
// compile functions, the Rust render plane, or the Drive publisher); it is
// never re-timed here. A non-positive duration is skipped so an unmeasured
// subtiming never fakes a zero-length operation.
func (r *Runner) recordAudioOperation(ctx context.Context, operation, component string, durationMs int64) {
	if durationMs <= 0 {
		return
	}
	kernobs.RecordOperation(ctx, kernobs.OperationInfo{
		Stage:     kernobs.StageName(audioCompileStage),
		Component: kernobs.ComponentName(component),
		Operation: kernobs.OperationName(operation),
	}, durationMs)
}

// recordAudioCompileOperations projects the compile-time subtimings
// (timeline build, clip/voiceover audio preparation, audio plan compile).
func (r *Runner) recordAudioCompileOperations(ctx context.Context, t AudioCompileTimings) {
	r.recordAudioOperation(ctx, "audio_asset_resolve", "audio", t.AudioAssetResolveMS)
	r.recordAudioOperation(ctx, "timeline_compile", "audio", t.TimelineCompileMS)
	r.recordAudioOperation(ctx, "clip_audio_prepare", "audio", t.ClipAudioPrepareMS)
	r.recordAudioOperation(ctx, "audio_plan_compile", "audio", t.AudioPlanCompileMS)
}

// recordAudioRenderOperations projects the Rust-render subtimings (mix, AAC
// encode, probe, hash). Hash is computed in Go but is part of the same
// combined-audio render boundary, so it shares the audio component.
func (r *Runner) recordAudioRenderOperations(ctx context.Context, m AudioPipelineMetrics) {
	r.recordAudioOperation(ctx, "mix", "audio", m.MixMS)
	r.recordAudioOperation(ctx, "aac_encode", "audio", m.AACEncodeMS)
	r.recordAudioOperation(ctx, "probe", "audio", m.ProbeMS)
	r.recordAudioOperation(ctx, "hash", "audio", m.HashMS)
}

// logPhraseMotionSelections records the phrase animation that was actually
// assigned to each phrase in the compiled plan, making repeated selections
// visible when comparing generated jobs.
func (r *Runner) logPhraseMotionSelections(runID string, plan *capabilityoverlay.OverlayPlan) {
	if r == nil || r.log == nil || plan == nil {
		return
	}
	counts := make(map[string]int)
	phraseOrdinal := 0
	for _, item := range plan.Items {
		if item.Kind != "text_phrase" {
			continue
		}
		counts[item.MotionID]++
		r.log.Info("phrase animation selected",
			zap.String("run_id", runID),
			zap.String("plan_id", plan.PlanID),
			zap.Int("phrase_ordinal", phraseOrdinal),
			zap.String("scene_id", item.SceneID),
			zap.String("item_id", item.ID),
			zap.String("phrase", item.Text),
			zap.Int("words", len(strings.Fields(item.Text))),
			zap.Int64("start_us", item.StartUS),
			zap.Int64("duration_us", item.DurationUS),
			zap.String("motion_id", item.MotionID),
			zap.String("preset_id", item.PresetID),
		)
		phraseOrdinal++
	}
	r.log.Info("phrase animation selection summary",
		zap.String("run_id", runID),
		zap.String("plan_id", plan.PlanID),
		zap.Int("phrase_count", phraseOrdinal),
		zap.Any("motion_counts", counts),
	)
}

// logPhraseAnchoringDiagnostics records, for every important-phrase candidate
// the overlay planner considered, whether it anchored to the certified speech
// timing, its word count and the skip reason. A phrase that does not anchor is
// dropped from the render, so this is the only place that shows how many long
// phrases were taken versus silently discarded.
func (r *Runner) logPhraseAnchoringDiagnostics(runID string, result *GenerateResult, language Language) {
	if r == nil || r.log == nil || result == nil {
		return
	}
	diagnostics := DiagnosePhraseAnchoring(result, language)
	if len(diagnostics) == 0 {
		return
	}
	anchored, skipped := 0, 0
	maxWords := 0
	for _, diag := range diagnostics {
		if diag.Words > maxWords {
			maxWords = diag.Words
		}
		if diag.Anchored {
			anchored++
			continue
		}
		skipped++
		r.log.Warn("phrase overlay candidate skipped",
			zap.String("run_id", runID),
			zap.String("scene_id", diag.SceneID),
			zap.Int("words", diag.Words),
			zap.String("phrase", diag.Text),
			zap.String("reason", diag.Reason),
		)
	}
	r.log.Info("phrase overlay anchoring summary",
		zap.String("run_id", runID),
		zap.Int("candidates", len(diagnostics)),
		zap.Int("anchored", anchored),
		zap.Int("skipped", skipped),
		zap.Int("max_words", maxWords),
	)
}
