package contract

import (
	audiocontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script/renderqueue"
)

type RenderArtifact = renderqueue.RenderArtifact

// FinalAudioReference is the canonical delivery-audio contract.
type FinalAudioReference = audiocontract.FinalAudioReference

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
type AudioPipelineMetrics struct {
	TTSMS        int64 `json:"tts_ms"`
	MediaFetchMS int64 `json:"media_fetch_ms"`
	// AudioAssetResolveMS is the owner-measured BGM/SFX asset resolution
	// boundary. It is separate from plan compilation so resolver latency is
	// visible instead of being folded into the audio-plan phase.
	AudioAssetResolveMS int64             `json:"audio_asset_resolve_ms"`
	TimelineCompileMS   int64             `json:"timeline_compile_ms"`
	AudioPlanCompileMS  int64             `json:"audio_plan_compile_ms"`
	ClipAudioPrepareMS  int64             `json:"clip_audio_prepare_ms"`
	PrepareMS           int64             `json:"prepare_ms"`
	LoudnessMS          int64             `json:"loudness_ms"`
	MixMS               int64             `json:"mix_ms"`
	AACEncodeMS         int64             `json:"aac_encode_ms"`
	ProbeMS             int64             `json:"probe_ms"`
	HashMS              int64             `json:"hash_ms"`
	UploadMS            int64             `json:"upload_ms"`
	TotalMS             int64             `json:"total_ms"`
	AudioDurationMS     int64             `json:"audio_duration_ms"`
	TTSCalls            int               `json:"tts_calls"`
	AudioRTF            float64           `json:"audio_rtf"`
	AudioSpeed          float64           `json:"audio_speed"`
	AudioEncodePasses   int               `json:"audio_encode_passes"`
	TTSScenes           []TTSSSceneMetric `json:"tts_scenes,omitempty"`
	// VoiceoverRequested counts every (scene, language) text that needed a
	// voiceover. VoiceoverReused counts scene-projection reuse (a voiceover
	// reference already attached before dispatch). VoiceoverGenerated counts
	// the items dispatched to the voiceover pipeline.
	VoiceoverRequested int `json:"voiceover_requested,omitempty"`
	VoiceoverReused    int `json:"voiceover_reused,omitempty"`
	VoiceoverGenerated int `json:"voiceover_generated,omitempty"`
	// VoiceoverDBCacheHits counts the dispatched items served by the
	// cross-run SQLite fingerprint cache (0 TTS calls, 0 uploads, 0
	// finalize). Warm identical runs report Generated == DBCacheHits and
	// TTSCalls == 0.
	VoiceoverDBCacheHits int `json:"voiceover_db_cache_hits,omitempty"`
	// CacheOutcome names how this run obtained the master: "HIT_PLAN" when a
	// previously certified artifact was reused for an identical plan, empty
	// when the audio was rendered. It is evidence of reuse, not a decision.
	CacheOutcome string `json:"cache_outcome,omitempty"`
}

type TTSSSceneMetric struct {
	SceneID          string   `json:"scene_id"`
	Language         Language `json:"language"`
	DurationMS       int64    `json:"duration_ms"`
	Characters       int      `json:"characters"`
	Words            int      `json:"words"`
	OutputDurationMS int64    `json:"output_duration_ms,omitempty"`
}
