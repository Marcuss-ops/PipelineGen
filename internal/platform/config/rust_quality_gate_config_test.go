package config

import "testing"

func TestExternalConfigRustQualityGateIsOptIn(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.External.RustQualityGateEnabled {
		t.Fatal("Rust quality gate must be disabled by default")
	}

	t.Setenv("VELOX_RUST_QUALITY_GATE", "true")
	applyEnvVars(cfg)
	if !cfg.External.RustQualityGateEnabled {
		t.Fatal("VELOX_RUST_QUALITY_GATE=true must enable the Rust gate")
	}
}
