package media

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// MediaexecConfig resolves platform video settings once at the composition
// boundary. Downstream builders receive the application-owned contract rather
// than re-reading platform/config.
func MediaexecConfig(cfg *config.Config) mediaexec.ExecutionConfig {
	if cfg == nil {
		return mediaexec.ExecutionConfig{}
	}
	profile := cfg.Video.CanonicalVideoProfile()
	policy := cfg.Video.EncoderPolicy()
	return mediaexec.ExecutionConfig{
		Profile: mediaexec.VideoProfile{
			Width:            profile.Width,
			Height:           profile.Height,
			FPSNum:           profile.FPSNum,
			FPSDen:           profile.FPSDen,
			KeyframeInterval: profile.KeyframeInterval,
			AudioCodec:       profile.AudioCodec,
			AudioBitrate:     profile.AudioBitrate,
			SampleRate:       profile.SampleRate,
			Channels:         profile.Channels,
		},
		Policy: mediaexec.EncoderPolicy{Codec: policy.Codec, Preset: policy.Preset, CRF: policy.CRF},
	}
}

func MediaexecVideoProfile(cfg *config.Config) mediaexec.VideoProfile {
	return MediaexecConfig(cfg).Profile
}

func MediaexecEncoderPolicy(cfg *config.Config) mediaexec.EncoderPolicy {
	return MediaexecConfig(cfg).Policy
}
