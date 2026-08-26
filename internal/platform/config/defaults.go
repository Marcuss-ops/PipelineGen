package config

import "github.com/Marcuss-ops/PipelineGen/internal/kernel/models"

// applyCanonicalModelDefaults fills model settings that are not explicit
// operator overrides from the canonical model registry. It runs after YAML
// and environment resolution so configured values remain visible and are
// validated by the composition-root contract gates.
func applyCanonicalModelDefaults(c *Config) {
	if c == nil {
		return
	}
	if c.External.OllamaEmbedModel == "" {
		c.External.OllamaEmbedModel = models.E5.ID
	}
	if c.Reranker.Model == "" {
		c.Reranker.Model = models.Reranker.ID
	}
}

// DefaultScriptDefaults returns the canonical script defaults represented by
// the config loader tags. Production values are normally populated by
// YAML/env resolution; this helper is only for isolated composition fixtures.
func DefaultScriptDefaults() ScriptDefaultsConfig {
	return ScriptDefaultsConfig{WordsPerMinute: 150, SafetyLanguage: "en"}
}

// DefaultVoiceoverDefaults returns the canonical voiceover defaults
// represented by the config loader tags. Production values are normally
// populated by YAML/env resolution.
func DefaultVoiceoverDefaults() VoiceoverDefaultsConfig {
	return VoiceoverDefaultsConfig{
		DefaultFilenameTemplate: "{slug}_{lang}.mp3",
		DefaultStrategy:         "verify",
		DefaultLanguage:         "en",
		DefaultParallelism:      3,
		MaxParallelism:          8,
		ChunkConcurrency:        3,
	}
}
