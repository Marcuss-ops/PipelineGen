package config

import "testing"

// TestScriptsConcurrencyEnvResolution pins the dedicated NLP/TTS concurrency
// env vars introduced for the script-generation worker pools:
//
//   - VELOX_SCRIPTS_NLP_CONCURRENCY defaults to 4 (certified) and overrides the
//     generation-gate capacity when set.
//   - VELOX_SCRIPTS_TTS_CONCURRENCY overrides the TTS voiceover pool; when unset
//     it stays 0 so the capability wiring defers to the voiceover provider bound
//     (VELOX_VOICEOVER_MAX_CONCURRENT_TTS) and then the certified default.
func TestScriptsConcurrencyEnvResolution(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	if got := cfg.Scripts.NLPConcurrency; got != 4 {
		t.Fatalf("NLPConcurrency default = %d, want 4 (certified)", got)
	}
	if got := cfg.Scripts.TTSConcurrency; got != 0 {
		t.Fatalf("TTSConcurrency default = %d, want 0 (defer to voiceover provider bound)", got)
	}

	t.Setenv("VELOX_SCRIPTS_NLP_CONCURRENCY", "8")
	t.Setenv("VELOX_SCRIPTS_TTS_CONCURRENCY", "6")
	applyEnvVars(cfg)

	if got := cfg.Scripts.NLPConcurrency; got != 8 {
		t.Fatalf("NLPConcurrency after env = %d, want 8", got)
	}
	if got := cfg.Scripts.TTSConcurrency; got != 6 {
		t.Fatalf("TTSConcurrency after env = %d, want 6", got)
	}
}

// TestScriptsConfigWithDefaults_DoesNotFakeTTSDefault locks the defer semantics:
// WithDefaults clamps NLP to 4 but must NOT invent a TTS default, because 0
// means "follow the voiceover provider bound" at the wiring boundary.
func TestScriptsConfigWithDefaults_DoesNotFakeTTSDefault(t *testing.T) {
	s := ScriptsConfig{}.WithDefaults()
	if s.NLPConcurrency != 4 {
		t.Fatalf("WithDefaults NLPConcurrency = %d, want 4", s.NLPConcurrency)
	}
	if s.TTSConcurrency != 0 {
		t.Fatalf("WithDefaults TTSConcurrency = %d, want 0 (defer, never faked)", s.TTSConcurrency)
	}
}
