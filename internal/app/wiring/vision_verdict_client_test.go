package wiring

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama"
	ollamaadapters "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/adapters"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

func testVisionConfig() *config.Config {
	return &config.Config{
		External: config.ExternalConfig{
			OllamaURL:            "http://127.0.0.1:11434",
			OllamaModel:          "gemma4:e2b",
			OllamaTimeoutSeconds: 120,
		},
	}
}

func TestNewVisionVerdictClientFallsBackToScriptClient(t *testing.T) {
	scriptClient := ollamaclient.NewClient("http://127.0.0.1:11434", "gemma4:e2b", 120)
	cfg := testVisionConfig()

	if got := newVisionVerdictClient(cfg, scriptClient); got != scriptClient {
		t.Fatalf("expected the script client when OLLAMA_VISION_ADDR is unset, got %#v", got)
	}
	if got := newVisionVerdictClient(nil, scriptClient); got != scriptClient {
		t.Fatalf("nil config must preserve the script client, got %#v", got)
	}
	if got := newVisionVerdictClient(cfg, nil); got != nil {
		t.Fatalf("nil script client must stay nil, got %#v", got)
	}
}

func TestNewVisionVerdictClientUsesDedicatedEndpoint(t *testing.T) {
	scriptClient := ollamaclient.NewClient("http://127.0.0.1:11434", "gemma4:e2b", 120)
	cfg := testVisionConfig()
	cfg.External.OllamaVisionURL = "http://127.0.0.1:11435"
	cfg.External.OllamaVisionModel = "qwen2.5vl:7b"

	got := newVisionVerdictClient(cfg, scriptClient)
	if got == scriptClient {
		t.Fatal("a configured vision endpoint must not reuse the script client")
	}
	if got.BaseURL() != "http://127.0.0.1:11435" {
		t.Fatalf("vision base URL = %q, want http://127.0.0.1:11435", got.BaseURL())
	}
	if got.Model() != "qwen2.5vl:7b" {
		t.Fatalf("vision model = %q, want qwen2.5vl:7b", got.Model())
	}
}

func TestNewVisionVerdictClientDefaultsModelToTextModel(t *testing.T) {
	scriptClient := ollamaclient.NewClient("http://127.0.0.1:11434", "gemma4:e2b", 120)
	cfg := testVisionConfig()
	cfg.External.OllamaVisionURL = "http://127.0.0.1:11435"

	got := newVisionVerdictClient(cfg, scriptClient)
	if got == scriptClient {
		t.Fatal("a configured vision endpoint must build a dedicated client")
	}
	if got.Model() != "gemma4:e2b" {
		t.Fatalf("vision model = %q, want the text model fallback gemma4:e2b", got.Model())
	}
}

func TestScriptVisualCandidateScorerPrefersAIBundleScorer(t *testing.T) {
	client := ollamaclient.NewClient("http://127.0.0.1:11435", "vision-test", 120)
	scorer := ollamaadapters.NewOllamaPersonImageScorer(client)
	root := &ComposeRoot{AI: &AIBundle{VisionVerdictScorer: scorer}}

	if got := scriptVisualCandidateScorer(root); got != scorer {
		t.Fatalf("expected the shared AI bundle scorer, got %#v", got)
	}
}

func TestVisionVerdictClientPrefersAIBundleClient(t *testing.T) {
	scriptClient := ollamaclient.NewClient("http://127.0.0.1:11434", "gemma4:e2b", 120)
	dedicated := ollamaclient.NewClient("http://127.0.0.1:11435", "qwen2.5vl:7b", 120)

	root := &ComposeRoot{AI: &AIBundle{
		VisionVerdictClient: dedicated,
		ScriptGen:           ollama.NewGenerator(scriptClient),
	}}
	if got := visionVerdictClient(root); got != dedicated {
		t.Fatalf("expected the dedicated AI bundle client, got %#v", got)
	}

	root.AI.VisionVerdictClient = nil
	if got := visionVerdictClient(root); got != scriptClient {
		t.Fatalf("expected the script fallback, got %#v", got)
	}

	if got := visionVerdictClient(nil); got != nil {
		t.Fatalf("nil root must return nil, got %#v", got)
	}
	if got := visionVerdictClient(&ComposeRoot{}); got != nil {
		t.Fatalf("root without AI must return nil, got %#v", got)
	}
}
