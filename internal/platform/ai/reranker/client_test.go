package reranker

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

func TestConfigDefaultsToCanonicalRegistryModel(t *testing.T) {
	got := (Config{}).WithDefaults()
	if got.Model != models.Reranker.ID {
		t.Fatalf("model = %q, want canonical registry model %q", got.Model, models.Reranker.ID)
	}
}

func TestClientDisablesNonCanonicalModel(t *testing.T) {
	client := NewClient(Config{Enabled: true, Model: "different/reranker"})
	if client.IsEnabled() {
		t.Fatal("client must be disabled when configured model differs from registry")
	}
}
