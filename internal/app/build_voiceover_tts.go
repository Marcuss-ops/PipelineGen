// Package app — build_voiceover_tts.go
// TTS provider chain construction for the voiceover bundle. The raw
// audio processor is wrapped by the use-case adapter, then by the
// retryable (backoff + circuit breaker) and rate-limited wrappers so
// the voiceover package only ever sees the canonical TTSProvider port.
//
// Extracted from buildVoiceoverService (build_bundles_voiceover.go) as
// part of the July 2026 domain split: tts / destinations / jobs /
// validators.
package app

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	audioasset "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// buildVoiceoverTTSProvider constructs the TTS provider chain used by
// both the legacy batch service and the canonical per-item use case.
//
// P1-2 (June 2026): the application layer no longer constructs the
// production *audioasset.Processor. Construction moves UP to the
// composition root (this file) so the voiceover package can stay free
// of any internal/infrastructure/* import. The processor is wrapped by
// newUseCaseTTSAdapter so the voiceover.Service only sees the canonical
// TTSProvider port.
func buildVoiceoverTTSProvider(
	cfg *config.Config,
	log *zap.Logger,
) (*audioasset.Processor, voiceover.TTSProvider) {
	if cfg.Paths.PythonScriptsDir == "" {
		log.Warn("voiceover: cfg.Paths.PythonScriptsDir is empty; audioasset.NewProcessor will be called with an empty string (TTS invocation will fail at runtime)")
	}
	audioProcessor := audioasset.NewProcessor(cfg.Paths.PythonScriptsDir, log)
	var ttsProvider voiceover.TTSProvider = newUseCaseTTSAdapter(audioProcessor)

	// FASE 6 (July 2026): wrap TTS provider with exponential-backoff
	// retry + circuit breaker. The retry fires INSIDE the rate limiter
	// (each semaphore slot owns its retries). Circuit breaker opens
	// after N consecutive failures across all calls.
	ttsProvider = newRetryableTTSProvider(ttsProvider, cfg.Voiceover, log)

	// FASE 8 (July 2026): wrap adapters with bounded concurrency,
	// per-call timeouts, and Drive-upload retry. The voiceover package
	// stays unaware of rate-limiting; the composition root swaps the
	// raw adapters with these wrappers in-place.
	ttsProvider = newRateLimitedTTSProvider(ttsProvider, cfg.Voiceover, log) // Keep each provider request below the speech-service limit. The
	// wrapper merges ordered chunks back into one track per language.
	ttsProvider = &chunkedTTSProvider{
		inner:       ttsProvider,
		merger:      ffmpeg.NewFromConfig(cfg),
		concurrency: cfg.Voiceover.Defaults.ChunkConcurrency,
	}

	return audioProcessor, ttsProvider
}
