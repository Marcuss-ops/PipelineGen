package config

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
		ChunkConcurrency:        2,
	}
}
