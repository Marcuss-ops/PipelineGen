package audio

import capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"

// Output is the audio-phase projection of GenerateResult. It is a read-only
// projection of the certified timeline, compiled plan, speech timing, final
// delivery audio and the bounded prefetch/timing evidence; it never becomes a
// second timing authority.
type Output struct {
	AudioMode          capabilityaudio.AudioMode           `json:"audio_mode,omitempty"`
	AudioStrategy      capabilityaudio.AudioRenderStrategy `json:"audio_strategy,omitempty"`
	CanonicalTimeline  *capabilityaudio.CanonicalTimeline  `json:"canonical_timeline,omitempty"`
	AudioPlan          *capabilityaudio.CompiledAudioPlan  `json:"audio_plan,omitempty"`
	PhraseTimings      []capabilityaudio.PhraseTiming      `json:"phrase_timings,omitempty"`
	SceneSpeechTimings []capabilityaudio.SceneSpeechTiming `json:"scene_speech_timings,omitempty"`
	FinalAudio         *FinalAudioReference                `json:"final_audio,omitempty"`
	CompileTimings     CompileTimings                      `json:"compile_timings,omitempty"`
	AudioMetrics       *AudioPipelineMetrics               `json:"audio_metrics,omitempty"`
	AudioPrefetch      *AudioPrefetchResult                `json:"audio_prefetch,omitempty"`
}
