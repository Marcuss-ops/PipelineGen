package contract

import (
	audiocontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/audio"
	kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

type RenderArtifact = renderqueue.RenderArtifact

// FinalAudioReference is the canonical delivery-audio contract.
type FinalAudioReference = audiocontract.FinalAudioReference

// AudioConfig is the request-side audio phase contract. GenerateRequest embeds
// it so the phase owns the fields while the durable JSON keeps its flat keys.
type AudioConfig = audiocontract.Config

// AudioOutput is the result-side audio phase contract. GenerateResult embeds
// it for the same reason: one owner, unchanged persisted wire shape.
type AudioOutput = audiocontract.Output

// AudioPipelineMetrics is an audio-friendly API projection of canonical
// observability stages and operations. It is not an authority and must not
// acquire independent timers or persistence writers.
//
// Relationship to the legacy domain contract: internal/kernel/script
// .GenerationTimings carries an overlapping set of flat *_ms audio fields used
// only by the migration-only internal/capabilities/scripts/usecase path. That
// struct is a legacy projection; this struct is the authority. Field map:
//
//	GenerationTimings (domain, legacy)  AudioPipelineMetrics (projection)
//	tts_total_ms                          tts_ms
//	audio_mix_ms                          mix_ms
//	audio_encode_ms                       aac_encode_ms
//	audio_probe_ms                        probe_ms
//	audio_hash_ms                         hash_ms
//	audio_pipeline_total_ms               total_ms
//	final_audio_duration_ms               audio_duration_ms
//	audio_encode_passes                   audio_encode_passes (unchanged)
//	tts_calls                             tts_calls (unchanged)
//	timeline_compile_ms                   timeline_compile_ms (unchanged)
//	audio_plan_compile_ms                 audio_plan_compile_ms (unchanged)
//	clip_audio_prepare_ms                 clip_audio_prepare_ms (unchanged)
//
// Projection-only fields with no legacy equivalent: media_fetch_ms, upload_ms,
// audio_rtf, audio_speed, tts_scenes, prepare_ms, loudness_ms.
//
// prepare_ms/loudness_ms are the Rust renderer's OWN pre-mix measurements:
// source probing, chain resolution and the one-per-path BGM mean-volume
// measurement that decodes the whole music asset. They are carried like
// mix_ms/aac_encode_ms — never re-timed here — so the render wall and its
// operations close the same budget.
//
// Do NOT add new timing fields here as an authority; derive them from the
// canonical RunReport and keep this struct stable for API consumers.
type AudioPipelineMetrics = kernelaudio.AudioPipelineMetrics

type TTSSSceneMetric = kernelaudio.TTSSSceneMetric
