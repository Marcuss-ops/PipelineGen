package scriptgeneration

import (
	"context"
	"testing"
)

type registryTestBackend struct{}

func (registryTestBackend) Extract(context.Context, string, string, int) ([]VisualEntity, error) {
	return nil, nil
}

func TestVisualNERBackendRegistryResolvesAndFailsClosed(t *testing.T) {
	backend := registryTestBackend{}
	registry, err := NewVisualNERBackendRegistry(map[string]NERBackend{" Rust ": backend, "spacy": backend})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := registry.Resolve("SPACY")
	if err != nil || resolved == nil {
		t.Fatalf("Resolve(SPACY) = %v, %v", resolved, err)
	}
	for _, unavailable := range []string{"", "xlm-roberta", "gliner"} {
		if _, err := registry.Resolve(unavailable); err == nil {
			t.Errorf("Resolve(%q) unexpectedly succeeded", unavailable)
		}
	}
}

func TestVisualNERBackendRegistryRejectsInvalidRegistrations(t *testing.T) {
	if _, err := NewVisualNERBackendRegistry(nil); err == nil {
		t.Fatal("expected empty registry error")
	}
	if _, err := NewVisualNERBackendRegistry(map[string]NERBackend{"": registryTestBackend{}}); err == nil {
		t.Fatal("expected empty backend name error")
	}
	if _, err := NewVisualNERBackendRegistry(map[string]NERBackend{"rust": nil}); err == nil {
		t.Fatal("expected nil implementation error")
	}
	if _, err := NewVisualNERBackendRegistry(map[string]NERBackend{"spacy": registryTestBackend{}, " SPACY ": registryTestBackend{}}); err == nil {
		t.Fatal("expected normalized duplicate error")
	}
}
