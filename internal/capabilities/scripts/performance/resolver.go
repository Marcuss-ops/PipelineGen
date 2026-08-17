package performance

import (
	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// PhaseResolver maps one job's canonical inputs into comparable phase
// measurements. Exactly one production resolver exists; call sites must not
// re-implement the phase→source mapping locally (architecture rule: shared
// registry/resolver, no duplicated decision logic).
type PhaseResolver interface {
	Resolve(run kernobs.RunReport, audio scriptgeneration.AudioPipelineMetrics, steps []scriptgeneration.ExecutionStep) []PhaseMeasurement
}

// DefaultPhaseResolver is the single canonical resolver. It reads:
//
//   - RunReport.Operations for the Ollama/gemma generate operation
//   - AudioPipelineMetrics for the audio subtimings
//   - the runner's execution steps for DOCUMENT and VELOX_ENQUEUE
//
// and never fabricates a value: a phase with no populated source is reported
// unmeasured (Measured=false) instead of estimated.
type DefaultPhaseResolver struct{}

var _ PhaseResolver = DefaultPhaseResolver{}

const (
	srcOllamaGenerate = "run.operations:ollama/generate"
	srcAudioMetrics   = "audio_metrics"
	srcExecutionStep  = "execution_step"
)

func (DefaultPhaseResolver) Resolve(run kernobs.RunReport, audio scriptgeneration.AudioPipelineMetrics, steps []scriptgeneration.ExecutionStep) []PhaseMeasurement {
	// script_gemma: aggregate every Ollama "generate" operation on the run.
	// A single job may issue one generation per scene, so the phase is the
	// aggregate inference time — never the whole SCRIPT step (which also
	// carries scene commit, checkpoint, and input-attach overhead).
	gemmaMS := gemmaInferenceMS(run)

	// audio_prepare groups the two pre-mix compile steps that the canonical
	// AudioPipelineMetrics keeps as separate fields.
	prepareMS := audio.TimelineCompileMS + audio.ClipAudioPrepareMS

	return []PhaseMeasurement{
		measure(PhaseScriptGemma, srcOllamaGenerate, gemmaMS, nil),
		measure(PhaseEdgeTTS, srcAudioMetrics+".tts_ms", audio.TTSMS, map[string]float64{
			"tts_calls":  float64(audio.TTSCalls),
			"tts_scenes": float64(len(audio.TTSScenes)),
		}),
		measure(PhaseAudioPrepare, srcAudioMetrics+".timeline_compile_ms+clip_audio_prepare_ms", prepareMS, nil),
		measure(PhaseAudioPlan, srcAudioMetrics+".audio_plan_compile_ms", audio.AudioPlanCompileMS, nil),
		measure(PhaseRustMix, srcAudioMetrics+".mix_ms", audio.MixMS, nil),
		measure(PhaseRustEncode, srcAudioMetrics+".aac_encode_ms", audio.AACEncodeMS, nil),
		measure(PhaseProbe, srcAudioMetrics+".probe_ms", audio.ProbeMS, nil),
		measure(PhaseHash, srcAudioMetrics+".hash_ms", audio.HashMS, nil),
		measure(PhaseUpload, srcAudioMetrics+".upload_ms", audio.UploadMS, nil),
		measure(PhaseGoogleDoc, srcExecutionStep+":DOCUMENT", stepDuration(steps, "DOCUMENT"), nil),
		measure(PhaseRenderEnqueue, srcExecutionStep+":VELOX_ENQUEUE", stepDuration(steps, "VELOX_ENQUEUE"), nil),
	}
}

// stepDuration returns the duration of the latest COMPLETED execution step
// with the given name, or 0 when absent.
func stepDuration(steps []scriptgeneration.ExecutionStep, name string) int64 {
	for _, s := range steps {
		if s.Name == name && s.Status == "COMPLETED" {
			return s.DurationMS
		}
	}
	return 0
}

// gemmaInferenceMS sums every Ollama "generate" operation on the run. The
// Ollama client owns the timer for each inference call, so this is the
// aggregate provider inference time — never caller-estimated.
func gemmaInferenceMS(run kernobs.RunReport) int64 {
	var total int64
	for _, op := range run.Operations {
		if op.Component == string(kernobs.ComponentOllama) && op.Operation == string(kernobs.OperationGenerate) {
			total += op.DurationMs
		}
	}
	return total
}

// scriptSummary splits the SCRIPT execution step into provider inference and
// script overhead (checkpoint, scene-commit emission, input-asset attach,
// etc.). TotalMS is the SCRIPT step duration, InferenceMS is the sum of the
// ollama/generate operations, and OverheadMS is total minus inference
// (clamped at zero so a clock skew never produces a negative number).
func scriptSummary(run kernobs.RunReport, steps []scriptgeneration.ExecutionStep) ScriptSummary {
	total := stepDuration(steps, "SCRIPT")
	inference := gemmaInferenceMS(run)
	overhead := total - inference
	if overhead < 0 {
		overhead = 0
	}
	return ScriptSummary{TotalMS: total, InferenceMS: inference, OverheadMS: overhead}
}

// measure builds a phase measurement. A phase is measured only when its
// resolved duration is strictly positive; otherwise it stays unmeasured so the
// report surfaces the instrumentation gap instead of estimating it.
func measure(phase PerformancePhase, source string, ms int64, counters map[string]float64) PhaseMeasurement {
	return PhaseMeasurement{
		Phase:      phase,
		Source:     source,
		DurationMS: ms,
		Measured:   ms > 0,
		Counters:   counters,
	}
}

// waitSummary projects the canonical wait signals. QueueMS and BlockedMS map
// 1:1 onto the run fields; CompletionMS is the sum of completion waits (the
// waiter is woken by the completion transition rather than polling); and
// OutboxDeliveryMS is the sum of outbox-delivery waits. All of them are waits,
// never stages: waiting does not inflate pipeline CPU time.
func waitSummary(run kernobs.RunReport) WaitSummary {
	var completionMS, outboxDeliveryMS int64
	items := make([]WaitMeasurement, 0, len(run.Waits))
	for _, w := range run.Waits {
		items = append(items, WaitMeasurement{Kind: string(w.Kind), Component: string(w.Component), DurationMS: w.DurationMs})
		switch w.Kind {
		case kernobs.WaitChildDependency, kernobs.WaitCompletion:
			completionMS += w.DurationMs
		case kernobs.WaitOutboxDelivery:
			outboxDeliveryMS += w.DurationMs
		}
	}
	return WaitSummary{
		QueueMS:          run.QueueWaitMs,
		BlockedMS:        run.BlockedMs,
		CompletionMS:     completionMS,
		OutboxDeliveryMS: outboxDeliveryMS,
		Items:            items,
	}
}

// audioSummary projects the canonical audio metrics without deriving new
// timings.
func audioSummary(audio scriptgeneration.AudioPipelineMetrics) AudioSummary {
	return AudioSummary{
		DurationMS: audio.AudioDurationMS,
		RTF:        audio.AudioRTF,
		TTSCalls:   audio.TTSCalls,
		TTSScenes:  len(audio.TTSScenes),
	}
}
