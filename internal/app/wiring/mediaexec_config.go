package wiring

import (
	mediasub "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/media"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Deprecated compatibility surface. Canonical ownership lives in
// internal/app/wiring/media; callers should consume that package directly.
func MediaexecConfig(cfg *config.Config) mediaexec.ExecutionConfig {
	return mediasub.MediaexecConfig(cfg)
}

func MediaexecVideoProfile(cfg *config.Config) mediaexec.VideoProfile {
	return mediasub.MediaexecVideoProfile(cfg)
}

func MediaexecEncoderPolicy(cfg *config.Config) mediaexec.EncoderPolicy {
	return mediasub.MediaexecEncoderPolicy(cfg)
}
