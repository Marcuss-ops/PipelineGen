package audio

import kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"

type AudioMixPolicy = kernelaudio.AudioMixPolicy

const (
	MixVoiceoverOnly           = kernelaudio.MixVoiceoverOnly
	MixVoiceoverWithDuckedClip = kernelaudio.MixVoiceoverWithDuckedClip
	BackgroundMusicGainDB      = kernelaudio.BackgroundMusicGainDB
	SoundEffectGainDB          = kernelaudio.SoundEffectGainDB
	DuckClipBaseGainDB         = kernelaudio.DuckClipBaseGainDB
	DuckClipActiveGainDB       = kernelaudio.DuckClipActiveGainDB
	DuckAttackUS               = kernelaudio.DuckAttackUS
	DuckReleaseUS              = kernelaudio.DuckReleaseUS
)
