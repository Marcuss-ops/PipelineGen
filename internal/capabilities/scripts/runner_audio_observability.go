package scriptgeneration

import (
	"context"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// audioCompileStage is the canonical observability stage under which the
// combined-audio subtimings are recorded as operations. It mirrors the
// AUDIO_COMPILE execution step: the step is the business/orchestration phase,
// and each subtiming below it is a technical operation. It is a
// business-stage name (like the clipindexer "index" stage), not a generic
// observability-taxonomy entry.
const audioCompileStage = "audio_compile"

// voiceoverStage mirrors the VOICEOVER execution step and hosts the
// owner-measured TTS subtiming. Like audioCompileStage it is a business-stage
// name, not a generic observability-taxonomy entry.
const voiceoverStage = "voiceover"

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
