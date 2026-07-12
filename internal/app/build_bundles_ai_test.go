package app

import "testing"

func TestResolveOllamaEmbedModel_DefaultsToEmbeddingModel(t *testing.T) {
	if got := resolveOllamaEmbedModel(""); got != "nomic-embed-text" {
		t.Fatalf("resolveOllamaEmbedModel(\"\") = %q, want %q", got, "nomic-embed-text")
	}
}

func TestResolveOllamaEmbedModel_PreservesExplicitEmbeddingModel(t *testing.T) {
	if got := resolveOllamaEmbedModel("nomic-embed-text:latest"); got != "nomic-embed-text:latest" {
		t.Fatalf("resolveOllamaEmbedModel explicit model = %q, want %q", got, "nomic-embed-text:latest")
	}
}
