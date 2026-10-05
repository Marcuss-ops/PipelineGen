package wiring

import (
	"context"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestBuildVisualNERBackendSelectsConfiguredBackend(t *testing.T) {
	backend, err := buildVisualNERBackend(&config.Config{}, nil)
	if err != nil || backend == nil {
		t.Fatalf("build rust backend = %v, %v", backend, err)
	}
	var _ interface {
		Extract(context.Context, string, string, int) ([]scriptgen.VisualEntity, error)
	} = backend
	cfg := &config.Config{}
	cfg.External.VisualNERBackend = "spacy"
	if _, err := buildVisualNERBackend(cfg, nil); err == nil {
		t.Fatal("expected missing spaCy endpoint to fail closed")
	}
	cfg.External.SpacyNERURL = "http://127.0.0.1:8001"
	backend, err = buildVisualNERBackend(cfg, nil)
	if err != nil || backend == nil {
		t.Fatalf("build spaCy backend = %v, %v", backend, err)
	}
	cfg.External.VisualNERBackend = "xlm-roberta"
	if _, err := buildVisualNERBackend(cfg, nil); err == nil {
		t.Fatal("expected unregistered XLM-R to fail closed")
	}
}
