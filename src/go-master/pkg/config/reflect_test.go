package config

import "testing"

func TestApplyDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("Server.Host default = %q, want %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 8080 {
		t.Fatalf("Server.Port default = %d, want %d", cfg.Server.Port, 8080)
	}
	if cfg.Logging.Level != "info" {
		t.Fatalf("Logging.Level default = %q, want %q", cfg.Logging.Level, "info")
	}
	if cfg.Storage.DataDir != "./data" {
		t.Fatalf("Storage.DataDir default = %q, want %q", cfg.Storage.DataDir, "./data")
	}
	if cfg.Security.CORSOrigins == nil || len(cfg.Security.CORSOrigins) != 1 || cfg.Security.CORSOrigins[0] != "*" {
		t.Fatalf("Security.CORSOrigins default = %v, want [\"*\"]", cfg.Security.CORSOrigins)
	}
}

func TestApplyEnvVars(t *testing.T) {
	t.Setenv("VELOX_PORT", "9999")
	t.Setenv("VELOX_DATA_DIR", "/tmp/velox-data")
	t.Setenv("VELOX_CORS_ORIGINS", "http://a.com,http://b.com")

	cfg := &Config{}
	applyDefaults(cfg)
	applyEnvVars(cfg)

	if cfg.Server.Port != 9999 {
		t.Fatalf("Server.Port = %d, want 9999", cfg.Server.Port)
	}
	if cfg.Storage.DataDir != "/tmp/velox-data" {
		t.Fatalf("Storage.DataDir = %q, want /tmp/velox-data", cfg.Storage.DataDir)
	}
	if len(cfg.Security.CORSOrigins) != 2 {
		t.Fatalf("Security.CORSOrigins length = %d, want 2", len(cfg.Security.CORSOrigins))
	}
}

func TestResolveRelativePath(t *testing.T) {
	if got := resolveRelativePath(""); got != "" {
		t.Fatalf("resolveRelativePath(\"\") = %q, want empty", got)
	}

	t.Setenv("VELOX_CREDENTIALS_FILE", "credentials.json")
	cfg := &Config{}
	applyDefaults(cfg)

	if got := cfg.GetCredentialsPath(); got == "" {
		t.Fatalf("GetCredentialsPath() returned empty path")
	}
	if got := cfg.GetTokenPath(); got == "" {
		t.Fatalf("GetTokenPath() returned empty path")
	}
}

func TestLoadFullSequence(t *testing.T) {
	t.Setenv("VELOX_PORT", "5555")
	cfg := load()
	if cfg.Server.Port != 5555 {
		t.Fatalf("Server.Port = %d, want 5555", cfg.Server.Port)
	}
}

func TestUnexportedFieldsSkipped(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	applyEnvVars(cfg)
}

func TestEnvOverridesDefaults(t *testing.T) {
	t.Setenv("VELOX_PORT", "7777")

	cfg := &Config{}
	applyDefaults(cfg)
	applyEnvVars(cfg)

	if cfg.Server.Port != 7777 {
		t.Fatalf("Server.Port = %d, want 7777", cfg.Server.Port)
	}
}
