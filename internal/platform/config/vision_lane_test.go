package config

import (
	"strings"
	"testing"
)

func TestExternalConfigVisionLaneDefaultsAndEnvBindings(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.External.OllamaVisionURL != "" {
		t.Fatalf("ollama_vision_url must default empty, got %q", cfg.External.OllamaVisionURL)
	}
	if cfg.External.OllamaVisionModel != "" {
		t.Fatalf("ollama_vision_model must default empty, got %q", cfg.External.OllamaVisionModel)
	}

	t.Setenv("OLLAMA_VISION_ADDR", "http://127.0.0.1:11435")
	t.Setenv("OLLAMA_VISION_MODEL", "qwen2.5vl:7b")
	applyEnvVars(cfg)
	if cfg.External.OllamaVisionURL != "http://127.0.0.1:11435" {
		t.Fatalf("vision URL = %q, want env binding", cfg.External.OllamaVisionURL)
	}
	if cfg.External.OllamaVisionModel != "qwen2.5vl:7b" {
		t.Fatalf("vision model = %q, want env binding", cfg.External.OllamaVisionModel)
	}
}

func TestConfigValidateRejectsVisionModelWithoutEndpoint(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth:         false,
			DeliveryHMACSecret: "0123456789abcdef0123456789abcdef",
		},
		Server: ServerConfig{
			Host:         "127.0.0.1",
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL:         "http://localhost:11434",
			OllamaModel:       "gemma4:e2b",
			OllamaVisionModel: "qwen2.5vl:7b",
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for ollama_vision_model without ollama_vision_url")
	}
	if !strings.Contains(err.Error(), "ollama_vision_url") {
		t.Fatalf("error = %v, want it to name ollama_vision_url", err)
	}
}

func TestConfigValidateAcceptsDedicatedVisionLane(t *testing.T) {
	cfg := &Config{
		Security: SecurityConfig{
			EnableAuth:         false,
			DeliveryHMACSecret: "0123456789abcdef0123456789abcdef",
		},
		Server: ServerConfig{
			Host:         "127.0.0.1",
			Port:         8080,
			ReadTimeout:  600,
			WriteTimeout: 600,
		},
		External: ExternalConfig{
			OllamaURL:            "http://localhost:11434",
			OllamaModel:          "gemma4:e2b",
			OllamaVisionURL:      "http://127.0.0.1:11435",
			OllamaVisionModel:    "qwen2.5vl:7b",
			OllamaTimeoutSeconds: 120,
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("dedicated vision lane must validate: %v", err)
	}
}
