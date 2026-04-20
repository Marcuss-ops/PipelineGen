package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- Client Tests with Mock HTTP Server ---

func TestNewClient(t *testing.T) {
	tests := []struct {
		name          string
		baseURL       string
		model         string
		expectedURL   string
		expectedModel string
	}{
		{
			name:          "Custom URL and model",
			baseURL:       "http://localhost:9999",
			model:         "mistral",
			expectedURL:   "http://localhost:9999",
			expectedModel: "mistral",
		},
		{
			name:          "Empty defaults",
			baseURL:       "",
			model:         "",
			expectedURL:   "http://localhost:11434",
			expectedModel: "gemma3:4b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient(tt.baseURL, tt.model)
			if c.baseURL != tt.expectedURL {
				t.Errorf("baseURL = %q, want %q", c.baseURL, tt.expectedURL)
			}
			if c.model != tt.expectedModel {
				t.Errorf("model = %q, want %q", c.model, tt.expectedModel)
			}
		})
	}
}

func TestClient_Generate_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/generate" {
			t.Errorf("expected /api/generate, got %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		var req GenerateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		if req.Model != "llama2" {
			t.Errorf("request model = %q, want llama2", req.Model)
		}
		if req.Stream != false {
			t.Errorf("request stream = %v, want false", req.Stream)
		}

		resp := GenerateResponse{
			Response: "Hello, this is a test response.",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	result, err := client.Generate(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Hello, this is a test response." {
		t.Errorf("result = %q, want %q", result, "Hello, this is a test response.")
	}
}

func TestClient_Generate_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	_, err := client.Generate(context.Background(), "test prompt")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestClient_Generate_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not valid json"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	_, err := client.Generate(context.Background(), "test prompt")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestClient_Generate_ConnectionRefused(t *testing.T) {
	// Point to a port that's not listening
	client := NewClient("http://127.0.0.1:1", "llama2")
	_, err := client.Generate(context.Background(), "test prompt")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestClient_GenerateScript(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: "Generated script content here.",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	result, err := client.GenerateScript(context.Background(), "source", "title", "english", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Generated script content here." {
		t.Errorf("result = %q, want %q", result, "Generated script content here.")
	}
}

func TestClient_GenerateScriptFromYouTube(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: "Script from YouTube transcript.",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	result, err := client.GenerateScriptFromYouTube(context.Background(), "transcript", "title", "english", 60)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Script from YouTube transcript." {
		t.Errorf("result = %q, want %q", result, "Script from YouTube transcript.")
	}
}

func TestClient_Summarize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: "Summary text.",
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	result, err := client.Summarize(context.Background(), "long text to summarize", 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Summary text." {
		t.Errorf("result = %q, want %q", result, "Summary text.")
	}
}

func TestClient_CheckHealth_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("expected /api/tags, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	if !client.CheckHealth(context.Background()) {
		t.Error("expected healthy, got unhealthy")
	}
}

func TestClient_CheckHealth_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	if client.CheckHealth(context.Background()) {
		t.Error("expected unhealthy, got healthy")
	}
}

func TestClient_CheckHealth_ConnectionRefused(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "llama2")
	if client.CheckHealth(context.Background()) {
		t.Error("expected unhealthy, got healthy")
	}
}

func TestClient_ListModels_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ListModelsResponse{
			Models: []ModelInfo{
				{Name: "llama2", Size: 3800000000, ModifiedAt: "2024-01-01"},
				{Name: "mistral", Size: 4100000000, ModifiedAt: "2024-02-01"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].Name != "llama2" {
		t.Errorf("first model = %q, want llama2", models[0].Name)
	}
	if models[1].Name != "mistral" {
		t.Errorf("second model = %q, want mistral", models[1].Name)
	}
}

func TestClient_ListModels_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	_, err := client.ListModels(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
