package audio

import (
	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scenecontract "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contracts/scene"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// Config is the audio-phase projection of GenerateRequest. It owns the audio
// mode, voiceover language selection, timing policy, SFX/BGM intents, mix
// policy and the publish destination. Video, editorial and final-job fields
// stay out of this contract.
type Config struct {
	Audio                    capabilityaudio.AudioMode              `json:"audio_mode,omitempty"`
	VoiceoverLanguages       []scenecontract.Language               `json:"voiceover_languages,omitempty"`
	Timing                   *capabilityaudio.TimingRequest         `json:"voiceover_timing,omitempty"`
	RandomSFXOnClipStart     bool                                   `json:"random_sfx_on_clip_start,omitempty"`
	RandomSFXOnImageOverlay  bool                                   `json:"random_sfx_on_image_overlay,omitempty"`
	RandomSFXOnPhraseOverlay bool                                   `json:"random_sfx_on_phrase_overlay,omitempty"`
	MixPolicy                capabilityaudio.AudioMixPolicy         `json:"mix_policy,omitempty"`
	BackgroundMusic          []scriptpkg.BackgroundMusicIntent      `json:"background_music,omitempty"`
	SoundEffects             []scriptpkg.SoundEffectIntent          `json:"sound_effects,omitempty"`
	ClipStartSoundEffects    []scriptpkg.ClipStartSoundEffectIntent `json:"clip_start_sound_effects,omitempty"`
	VoiceoverFolderID        string                                 `json:"voiceover_folder_id,omitempty"`
	TTSRate                  string                                 `json:"tts_rate,omitempty"`
}
