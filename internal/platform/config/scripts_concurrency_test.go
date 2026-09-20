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

// TestScriptsSeparateItemRenderWorkersResolution pins the per-item overlay
// render pool as an operator surface: it defaults to the certified 4, it is
// overridable from the environment, and it can never resolve to 0 — a
// zero-slot pool would render no per-item overlay at all instead of failing.
func TestScriptsSeparateItemRenderWorkersResolution(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if got := cfg.Scripts.SeparateItemRenderWorkers; got != 4 {
		t.Fatalf("SeparateItemRenderWorkers default = %d, want 4 (certified)", got)
	}

	t.Setenv("VELOX_SCRIPTS_SEPARATE_ITEM_RENDER_WORKERS", "6")
	applyEnvVars(cfg)
	if got := cfg.Scripts.SeparateItemRenderWorkers; got != 6 {
		t.Fatalf("SeparateItemRenderWorkers after env = %d, want 6", got)
	}

	// A zero value is not a documented "disable" — it is clamped to the
	// default so the pool is never the reason no overlay is rendered.
	if got := (ScriptsConfig{}).WithDefaults().SeparateItemRenderWorkers; got != 4 {
		t.Fatalf("WithDefaults SeparateItemRenderWorkers = %d, want 4", got)
	}
	if got := (ScriptsConfig{SeparateItemRenderWorkers: 7}).WithDefaults().SeparateItemRenderWorkers; got != 7 {
		t.Fatalf("WithDefaults must preserve an explicit pool of 7, got %d", got)
	}
}

func TestScriptsOverlayPublicationWorkersResolution(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if got := cfg.Scripts.OverlayPublicationWorkers; got != 6 {
		t.Fatalf("OverlayPublicationWorkers default = %d, want 6", got)
	}

	t.Setenv("VELOX_SCRIPTS_OVERLAY_PUBLICATION_WORKERS", "8")
	applyEnvVars(cfg)
	if got := cfg.Scripts.OverlayPublicationWorkers; got != 8 {
		t.Fatalf("OverlayPublicationWorkers after env = %d, want 8", got)
	}
	if got := (ScriptsConfig{OverlayPublicationWorkers: 99}).WithDefaults().OverlayPublicationWorkers; got != 8 {
		t.Fatalf("OverlayPublicationWorkers cap = %d, want 8", got)
	}
	if got := (ScriptsConfig{}).WithDefaults().OverlayPublicationWorkers; got != 6 {
		t.Fatalf("OverlayPublicationWorkers zero default = %d, want 6", got)
	}
}

// TestScriptsOverlayRenderConcurrencyResolution pins the multilingual overlay
// render fan-out as an operator surface: it defaults to the certified 2, it is
// overridable from the environment, and it can never resolve to 0 — a zero-slot
// pool would render no language at all instead of failing.
func TestScriptsOverlayRenderConcurrencyResolution(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if got := cfg.Scripts.OverlayRenderConcurrency; got != 2 {
		t.Fatalf("OverlayRenderConcurrency default = %d, want 2 (certified first step)", got)
	}

	t.Setenv("VELOX_SCRIPTS_OVERLAY_RENDER_CONCURRENCY", "3")
	applyEnvVars(cfg)
	if got := cfg.Scripts.OverlayRenderConcurrency; got != 3 {
		t.Fatalf("OverlayRenderConcurrency after env = %d, want 3", got)
	}

	if got := (ScriptsConfig{}).WithDefaults().OverlayRenderConcurrency; got != 2 {
		t.Fatalf("WithDefaults OverlayRenderConcurrency = %d, want 2", got)
	}
	if got := (ScriptsConfig{OverlayRenderConcurrency: 1}).WithDefaults().OverlayRenderConcurrency; got != 1 {
		t.Fatalf("WithDefaults must preserve the serial baseline 1, got %d", got)
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
