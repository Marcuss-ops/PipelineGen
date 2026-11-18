package wiring

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

func TestStartupWarmModelsDefaultsToConfiguredSingleModel(t *testing.T) {
	t.Setenv(EnvWarmModels, "")
	got := startupWarmModels("gemma4:e4b")
	if len(got) != 1 || got[0] != "gemma4:e4b" {
		t.Fatalf("startupWarmModels = %v, want [gemma4:e4b]", got)
	}
}

func TestStartupWarmModelsParsesAndDedupesEnvList(t *testing.T) {
	t.Setenv(EnvWarmModels, " gemma4:e4b , gemma4:e2b, gemma4:e4b ,,")
	got := startupWarmModels("gemma4:e4b")
	if len(got) != 2 || got[0] != "gemma4:e4b" || got[1] != "gemma4:e2b" {
		t.Fatalf("startupWarmModels = %v, want [gemma4:e4b gemma4:e2b]", got)
	}
}

func TestStartupWarmModelsDropsAutoAlias(t *testing.T) {
	t.Setenv(EnvWarmModels, "auto,gemma4:e2b")
	got := startupWarmModels("gemma4:e4b")
	if len(got) != 1 || got[0] != "gemma4:e2b" {
		t.Fatalf("startupWarmModels = %v, want [gemma4:e2b] (auto is not a warmable model)", got)
	}
}

func TestStartupWarmModelsFallsBackToConfiguredOnGarbageList(t *testing.T) {
	t.Setenv(EnvWarmModels, ",,auto,")
	got := startupWarmModels("gemma4:e4b")
	if len(got) != 1 || got[0] != "gemma4:e4b" {
		t.Fatalf("startupWarmModels = %v, want [gemma4:e4b] (configured fallback)", got)
	}
}

func TestStartupWarmModelsNilWhenNothingConfigured(t *testing.T) {
	t.Setenv(EnvWarmModels, "")
	if got := startupWarmModels(""); got != nil {
		t.Fatalf("startupWarmModels = %v, want nil", got)
	}
}

func TestStartupVisionWarmModelOnlyForDedicatedClient(t *testing.T) {
	textClient := ollamaclient.NewClient("http://127.0.0.1:11434", "text-model", 5)
	cfg := &config.Config{External: config.ExternalConfig{
		OllamaModel:       "text-model",
		OllamaVisionModel: "vision-model",
	}}

	shared := &ComposeRoot{AI: &AIBundle{OllamaClient: textClient, VisionVerdictClient: textClient}}
	if got := startupVisionWarmModel(cfg, shared); got != "" {
		t.Fatalf("shared client must not warm a second model, got %q", got)
	}

	dedicated := ollamaclient.NewClient("http://127.0.0.1:11435", "vision-model", 5)
	root := &ComposeRoot{AI: &AIBundle{OllamaClient: textClient, VisionVerdictClient: dedicated}}
	if got := startupVisionWarmModel(cfg, root); got != "vision-model" {
		t.Fatalf("vision warm model = %q, want vision-model", got)
	}

	cfg.External.OllamaVisionModel = ""
	if got := startupVisionWarmModel(cfg, root); got != "text-model" {
		t.Fatalf("vision model fallback = %q, want text-model", got)
	}
}
