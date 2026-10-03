package wiring

import "testing"

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
