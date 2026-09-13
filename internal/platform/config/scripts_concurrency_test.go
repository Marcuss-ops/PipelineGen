package config

import "testing"

// TestScriptsConcurrencyEnvResolution pins the dedicated NLP/TTS concurrency
// env vars introduced for the script-generation worker pools:
//
//   - VELOX_SCRIPTS_NLP_CONCURRENCY defaults to 4 (certified) for entity work.
//   - VELOX_SCRIPTS_SCRIPT_GENERATION_CONCURRENCY independently controls
//     scene-text generation.
//   - VELOX_SCRIPTS_TTS_CONCURRENCY overrides the TTS voiceover pool; when unset
//     it stays 0 so the capability wiring defers to the voiceover provider bound
//     (VELOX_VOICEOVER_MAX_CONCURRENT_TTS) and then the certified default.
//   - VELOX_SCRIPTS_TRANSLATION_CONCURRENCY overrides the scene×language
//     translation pool, the third independent pool of the SceneTextReady
//     fan-out. It must be resolvable from config/env: it was previously a Go
//     constant with no operator surface at all.
func TestScriptsConcurrencyEnvResolution(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	if got := cfg.Scripts.NLPConcurrency; got != 4 {
		t.Fatalf("NLPConcurrency default = %d, want 4 (certified)", got)
	}
	if got := cfg.Scripts.ScriptGenerationConcurrency; got != 3 {
		t.Fatalf("ScriptGenerationConcurrency default = %d, want 3 (certified)", got)
	}
	if got := cfg.Scripts.TTSConcurrency; got != 0 {
		t.Fatalf("TTSConcurrency default = %d, want 0 (defer to voiceover provider bound)", got)
	}
	if got := cfg.Scripts.TranslationConcurrency; got != 4 {
		t.Fatalf("TranslationConcurrency default = %d, want 4 (certified)", got)
	}

	t.Setenv("VELOX_SCRIPTS_NLP_CONCURRENCY", "8")
	t.Setenv("VELOX_SCRIPTS_SCRIPT_GENERATION_CONCURRENCY", "3")
	t.Setenv("VELOX_SCRIPTS_TTS_CONCURRENCY", "6")
	t.Setenv("VELOX_SCRIPTS_TRANSLATION_CONCURRENCY", "7")
	applyEnvVars(cfg)

	if got := cfg.Scripts.NLPConcurrency; got != 8 {
		t.Fatalf("NLPConcurrency after env = %d, want 8", got)
	}
	if got := cfg.Scripts.ScriptGenerationConcurrency; got != 3 {
		t.Fatalf("ScriptGenerationConcurrency after env = %d, want 3", got)
	}
	if got := cfg.Scripts.TTSConcurrency; got != 6 {
		t.Fatalf("TTSConcurrency after env = %d, want 6", got)
	}
	if got := cfg.Scripts.TranslationConcurrency; got != 7 {
		t.Fatalf("TranslationConcurrency after env = %d, want 7", got)
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
	if s.ScriptGenerationConcurrency != 3 {
		t.Fatalf("WithDefaults ScriptGenerationConcurrency = %d, want 3", s.ScriptGenerationConcurrency)
	}
	if s.TTSConcurrency != 0 {
		t.Fatalf("WithDefaults TTSConcurrency = %d, want 0 (defer, never faked)", s.TTSConcurrency)
	}
	// Translation is a plain gate (no provider bound to defer to), so it is
	// clamped to the certified default like NLP and script generation.
	if s.TranslationConcurrency != 4 {
		t.Fatalf("WithDefaults TranslationConcurrency = %d, want 4", s.TranslationConcurrency)
	}
}

// TestScriptsConfigGateCapacitiesAreOperatorVisible pins the Pipeline Waste
// Audit §6.2 acceptance criterion: the SceneTextReady fan-out gate capacities
// must exist as an operator-visible configuration surface (config.yaml /
// environment), not only as Go constants inside the capability. Four
// independent pools are covered: script generation, NLP, translation and TTS.
func TestScriptsConfigGateCapacitiesAreOperatorVisible(t *testing.T) {
	s := ScriptsConfig{}.WithDefaults()
	capacities := map[string]int{
		"script_generation_concurrency": s.ScriptGenerationConcurrency,
		"nlp_concurrency":               s.NLPConcurrency,
		"translation_concurrency":       s.TranslationConcurrency,
	}
	for name, got := range capacities {
		if got <= 0 {
			t.Fatalf("%s resolved to %d; every fan-out gate must have an explicit positive capacity", name, got)
		}
	}
	// TTS is the one intentional exception: 0 means "defer to the voiceover
	// provider bound", so it is never faked. The deferral itself is the
	// documented operator contract.
	if s.TTSConcurrency != 0 {
		t.Fatalf("TTSConcurrency = %d, want 0 (deferred to the provider bound)", s.TTSConcurrency)
	}
}
