package config

import (
	"os"
	"testing"
)

func TestApplyDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	// Server defaults
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host default = %q, want %q", cfg.Server.Host, "0.0.0.0")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port default = %d, want %d", cfg.Server.Port, 8080)
	}
	if cfg.Server.ReadTimeout != 600 {
		t.Errorf("Server.ReadTimeout default = %d, want %d", cfg.Server.ReadTimeout, 600)
	}
	if cfg.Server.WriteTimeout != 600 {
		t.Errorf("Server.WriteTimeout default = %d, want %d", cfg.Server.WriteTimeout, 600)
	}
	if cfg.Server.GinMode != "release" {
		t.Errorf("Server.GinMode default = %q, want %q", cfg.Server.GinMode, "release")
	}

	// Logging defaults
	if cfg.Logging.Level != "info" {
		t.Errorf("Logging.Level default = %q, want %q", cfg.Logging.Level, "info")
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("Logging.Format default = %q, want %q", cfg.Logging.Format, "json")
	}

	// Jobs defaults
	if cfg.Jobs.MaxParallelPerProject != 2 {
		t.Errorf("Jobs.MaxParallelPerProject default = %d, want %d", cfg.Jobs.MaxParallelPerProject, 2)
	}
	if cfg.Jobs.LeaseTTLSeconds != 300 {
		t.Errorf("Jobs.LeaseTTLSeconds default = %d, want %d", cfg.Jobs.LeaseTTLSeconds, 300)
	}

	// Workers defaults
	if cfg.Workers.HeartbeatTimeout != 120 {
		t.Errorf("Workers.HeartbeatTimeout default = %d, want %d", cfg.Workers.HeartbeatTimeout, 120)
	}

	// Security defaults
	if cfg.Security.EnableAuth != false {
		t.Errorf("Security.EnableAuth default = %v, want false", cfg.Security.EnableAuth)
	}
	if cfg.Security.RateLimitEnabled != true {
		t.Errorf("Security.RateLimitEnabled default = %v, want true", cfg.Security.RateLimitEnabled)
	}
	if cfg.Security.RateLimitRequests != 100 {
		t.Errorf("Security.RateLimitRequests default = %d, want %d", cfg.Security.RateLimitRequests, 100)
	}

	// Clip approval defaults (float64)
	if cfg.ClipApproval.MinScore != 20.0 {
		t.Errorf("ClipApproval.MinScore default = %f, want %f", cfg.ClipApproval.MinScore, 20.0)
	}
	if cfg.ClipApproval.AutoApproveThreshold != 85.0 {
		t.Errorf("ClipApproval.AutoApproveThreshold default = %f, want %f", cfg.ClipApproval.AutoApproveThreshold, 85.0)
	}

	// TextGen defaults
	if cfg.TextGen.DefaultModel != "gemma3:4b" {
		t.Errorf("TextGen.DefaultModel default = %q, want %q", cfg.TextGen.DefaultModel, "gemma3:4b")
	}
	if cfg.TextGen.Timeout != 60 {
		t.Errorf("TextGen.Timeout default = %d, want %d", cfg.TextGen.Timeout, 60)
	}
}

func TestApplyDefaultsDoesNotOverrideNonZero(t *testing.T) {
	cfg := &Config{}
	cfg.Server.Port = 9090
	cfg.Logging.Level = "debug"
	applyDefaults(cfg)

	// Non-zero values should be preserved
	if cfg.Server.Port != 9090 {
		t.Errorf("Server.Port = %d, want %d (should not be overridden by default)", cfg.Server.Port, 9090)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %q, want %q (should not be overridden by default)", cfg.Logging.Level, "debug")
	}

	// Zero values should still get defaults
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %q, want %q (zero value should get default)", cfg.Server.Host, "0.0.0.0")
	}
}

func TestApplyEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		envKey   string
		envVal   string
		check    func(cfg *Config) bool
		expected interface{}
	}{
		{"string env", "VELOX_HOST", "127.0.0.1", func(c *Config) bool { return c.Server.Host == "127.0.0.1" }, "127.0.0.1"},
		{"int env", "VELOX_PORT", "9999", func(c *Config) bool { return c.Server.Port == 9999 }, 9999},
		{"bool env true", "VELOX_ENABLE_AUTH", "true", func(c *Config) bool { return c.Security.EnableAuth == true }, true},
		{"bool env 1", "VELOX_ENABLE_AUTH", "1", func(c *Config) bool { return c.Security.EnableAuth == true }, true},
		{"bool env false", "VELOX_ENABLE_AUTH", "false", func(c *Config) bool { return c.Security.EnableAuth == false }, false},
		{"float env", "VELOX_CLIP_MIN_SCORE", "50.5", func(c *Config) bool { return c.ClipApproval.MinScore == 50.5 }, 50.5},
		{"log level env", "VELOX_LOG_LEVEL", "debug", func(c *Config) bool { return c.Logging.Level == "debug" }, "debug"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv(tt.envKey, tt.envVal)
			defer os.Unsetenv(tt.envKey)

			cfg := &Config{}
			applyDefaults(cfg)
			applyEnvVars(cfg)

			if !tt.check(cfg) {
				t.Errorf("env %s=%s did not apply correctly", tt.envKey, tt.envVal)
			}
		})
	}
}

func TestEnvOverridesDefaults(t *testing.T) {
	os.Setenv("VELOX_PORT", "7777")
	defer os.Unsetenv("VELOX_PORT")

	cfg := &Config{}
	applyDefaults(cfg)  // sets Port=8080
	applyEnvVars(cfg)   // should override to 7777

	if cfg.Server.Port != 7777 {
		t.Errorf("Server.Port = %d, want %d (env should override default)", cfg.Server.Port, 7777)
	}
}

func TestSliceDefaultParsing(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	// CORSOrigins has default ["*"] in the tag
	if len(cfg.Security.CORSOrigins) != 1 || cfg.Security.CORSOrigins[0] != "*" {
		t.Errorf("Security.CORSOrigins = %v, want [\"*\"]", cfg.Security.CORSOrigins)
	}
}

func TestSliceEnvParsing(t *testing.T) {
	os.Setenv("VELOX_CORS_ORIGINS", "http://a.com,http://b.com")
	defer os.Unsetenv("VELOX_CORS_ORIGINS")

	cfg := &Config{}
	applyDefaults(cfg)
	applyEnvVars(cfg)

	if len(cfg.Security.CORSOrigins) != 2 {
		t.Fatalf("Security.CORSOrigins length = %d, want 2", len(cfg.Security.CORSOrigins))
	}
	if cfg.Security.CORSOrigins[0] != "http://a.com" {
		t.Errorf("Security.CORSOrigins[0] = %q, want %q", cfg.Security.CORSOrigins[0], "http://a.com")
	}
	if cfg.Security.CORSOrigins[1] != "http://b.com" {
		t.Errorf("Security.CORSOrigins[1] = %q, want %q", cfg.Security.CORSOrigins[1], "http://b.com")
	}
}

func TestLoadFullSequence(t *testing.T) {
	os.Setenv("VELOX_PORT", "5555")
	defer os.Unsetenv("VELOX_PORT")

	cfg := load()

	if cfg.Server.Port != 5555 {
		t.Errorf("Server.Port = %d, want %d (env override via load)", cfg.Server.Port, 5555)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %q, want %q (default via load)", cfg.Server.Host, "0.0.0.0")
	}
}

func TestUnexportedFieldsSkipped(t *testing.T) {
	cfg := &Config{}
	// mu is unexported — applyDefaults should not panic on it
	applyDefaults(cfg)
	applyEnvVars(cfg)
	// If we got here without panic, the test passes
}

func TestDriveConfigDefaults(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	if cfg.Drive.StockRootFolderID != "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh" {
		t.Errorf("Drive.StockRootFolderID default = %q, want default tag value", cfg.Drive.StockRootFolderID)
	}
	if cfg.DriveSync.Interval != 86400 {
		t.Errorf("DriveSync.Interval default = %d, want %d", cfg.DriveSync.Interval, 86400)
	}
	if cfg.DriveSync.SyncTimeout != 600 {
		t.Errorf("DriveSync.SyncTimeout default = %d, want %d", cfg.DriveSync.SyncTimeout, 600)
	}
}
