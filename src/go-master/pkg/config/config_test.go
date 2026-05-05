package config

import (
	"testing"
)

func TestConfigValidateFailsWithoutAdminTokenWhenAuthEnabled(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: true,
			AdminToken: "",
		},
		Server: ServerConfig{
			Port:        8080,
			ReadTimeout: 600,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when auth enabled without admin token")
	}
}

func TestConfigValidateFailsWithInvalidPort(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"port zero", 0},
		{"port too high", 70000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Security: SecurityConfig{
					EnableAuth: false,
				},
				Server: ServerConfig{
					Port:        tt.port,
					ReadTimeout: 600,
					WriteTimeout: 600,
				},
				External: ExternalConfig{
					OllamaURL: "http://localhost:11434",
				},
			}
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected error for port %d", tt.port)
			}
		})
	}
}

func TestConfigValidateFailsWithMissingOllamaURL(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: false,
		},
		Server: ServerConfig{
			Port:        8080,
			ReadTimeout: 600,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL: "",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when ollama url is empty")
	}
}

func TestConfigValidateFailsWithZeroReadTimeout(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: false,
		},
		Server: ServerConfig{
			Port:        8080,
			ReadTimeout: 0,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when read timeout is zero")
	}
}

func TestConfigValidateFailsWithZeroWriteTimeout(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: false,
		},
		Server: ServerConfig{
			Port:        8080,
			ReadTimeout: 600,
			WriteTimeout: 0,
		},
		External: ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error when write timeout is zero")
	}
}

func TestConfigValidateAcceptsValidConfig(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth: true,
			AdminToken: "valid-token",
		},
		Server: ServerConfig{
			Port:        8080,
			ReadTimeout: 600,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL: "http://localhost:11434",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid config to pass, got error: %v", err)
	}
}
