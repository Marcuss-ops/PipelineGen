package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Generator Tests ---

func TestNewGenerator(t *testing.T) {
	client := NewClient("http://localhost:11434", "llama2")
	gen := NewGenerator(client)
	if gen == nil {
		t.Fatal("expected generator, got nil")
	}
	if gen.client != client {
		t.Error("generator client not set correctly")
	}
}

func TestGenerator_GetClient(t *testing.T) {
	client := NewClient("http://localhost:11434", "llama2")
	gen := NewGenerator(client)
	if gen.GetClient() != client {
		t.Error("GetClient did not return the expected client")
	}
}

func TestGenerator_GenerateFromText_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: "Generated script from text.",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	gen := NewGenerator(client)

	req := &TextGenerationRequest{
		SourceText: "Some source text",
		Title:      "Test Title",
		Language:   "english",
		Duration:   60,
		Tone:       "professional",
	}

	result, err := gen.GenerateFromText(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Script != "Generated script from text." {
		t.Errorf("script = %q, want %q", result.Script, "Generated script from text.")
	}
	if result.WordCount == 0 {
		t.Error("expected word count > 0")
	}
	if result.Model != "gemma3:12b" {
		t.Errorf("model = %q, want gemma3:12b", result.Model)
	}
}

func TestGenerator_GenerateFromText_Defaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: "Script with defaults.",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	gen := NewGenerator(client)

	req := &TextGenerationRequest{
		SourceText: "text",
		Title:      "title",
		// Language, Duration, Tone, Model not set — should use defaults
	}

	result, err := gen.GenerateFromText(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Model != "gemma3:12b" {
		t.Errorf("expected default model gemma3:12b, got %q", result.Model)
	}
}

func TestGenerator_GenerateFromYouTube_NoYouTubeClient(t *testing.T) {
	client := NewClient("http://localhost:11434", "llama2")
	gen := NewGenerator(client)
	// No YouTube client set — should return error

	req := &YouTubeGenerationRequest{
		YouTubeURL: "https://youtube.com/watch?v=abc",
		Title:      "Test",
	}

	_, err := gen.GenerateFromYouTube(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when YouTube client not configured, got nil")
	}
	if !strings.Contains(err.Error(), "YouTube client not configured") {
		t.Errorf("error = %q, want error containing 'YouTube client not configured'", err.Error())
	}
}

func TestGenerator_GenerateFromYouTubeTranscript_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: "Script from transcript.",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	gen := NewGenerator(client)

	req := &YouTubeGenerationRequest{
		Title:    "Test",
		Language: "english",
		Duration: 60,
	}

	result, err := gen.GenerateFromYouTubeTranscript(context.Background(), "transcript text", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Script != "Script from transcript." {
		t.Errorf("script = %q, want %q", result.Script, "Script from transcript.")
	}
}

func TestGenerator_Regenerate_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: "Regenerated script.",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	gen := NewGenerator(client)

	req := &RegenerationRequest{
		OriginalScript: "original script",
		Title:          "Test",
		Language:       "english",
		Tone:           "professional",
	}

	result, err := gen.Regenerate(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Script != "Regenerated script." {
		t.Errorf("script = %q, want %q", result.Script, "Regenerated script.")
	}
}

func TestGenerator_ListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ListModelsResponse{
			Models: []ModelInfo{{Name: "llama2", Size: 3800000000}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	gen := NewGenerator(client)

	models, err := gen.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].Name != "llama2" {
		t.Errorf("model name = %q, want llama2", models[0].Name)
	}
}
