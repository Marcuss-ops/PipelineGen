package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSemanticProductionSearchEnabledByDefault pins the production activation:
// the semantic planner owns the measured retrieval path, so a deployment that
// does not mention it must run it. An explicit `false` in YAML stays an
// operator rollback, and the env var wins over both.
func TestSemanticProductionSearchEnabledByDefault(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if !cfg.Scripts.SemanticProductionSearch {
		t.Fatal("SemanticProductionSearch must default to true for production")
	}
	if cfg.Scripts.SemanticPlanCacheTTLSeconds != 3600 {
		t.Fatalf("SemanticPlanCacheTTLSeconds = %d, want 3600", cfg.Scripts.SemanticPlanCacheTTLSeconds)
	}
	if got := cfg.Scripts.WithDefaults().SemanticPlanCacheTTLSeconds; got != 3600 {
		t.Fatalf("WithDefaults TTL = %d, want 3600", got)
	}
}

// An explicit false in the YAML file is not masked by the default pass.
func TestSemanticProductionSearchExplicitFalseWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("scripts:\n  semantic_production_search: false\n  semantic_plan_cache_ttl_seconds: 900\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := GetFromPath(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Scripts.SemanticProductionSearch {
		t.Fatal("explicit semantic_production_search: false was overridden by the default")
	}
	if cfg.Scripts.SemanticPlanCacheTTLSeconds != 900 {
		t.Fatalf("TTL = %d, want the explicit 900", cfg.Scripts.SemanticPlanCacheTTLSeconds)
	}
}

func TestSemanticProductionSearchEnvOverride(t *testing.T) {
	t.Setenv("VELOX_SCRIPTS_SEMANTIC_PRODUCTION_SEARCH", "false")
	t.Setenv("VELOX_SCRIPTS_SEMANTIC_PLAN_CACHE_TTL_SECONDS", "600")
	cfg := &Config{}
	applyDefaults(cfg)
	applyEnvVars(cfg)
	if cfg.Scripts.SemanticProductionSearch {
		t.Fatal("VELOX_SCRIPTS_SEMANTIC_PRODUCTION_SEARCH=false did not disable the planner")
	}
	if cfg.Scripts.SemanticPlanCacheTTLSeconds != 600 {
		t.Fatalf("TTL = %d, want the env value 600", cfg.Scripts.SemanticPlanCacheTTLSeconds)
	}
}
