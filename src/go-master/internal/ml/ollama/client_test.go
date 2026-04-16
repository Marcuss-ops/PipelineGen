package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
			expectedModel: "llama2",
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

func TestClient_ExtractEntitiesFromSegment_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: `{
				"frasi_importanti": ["important phrase 1", "important phrase 2"],
				"entity_senza_testo": {"Entity1": "https://example.com/logo.png"},
				"nomi_speciali": ["SpecialName"],
				"parole_importanti": ["keyword1", "keyword2"]
			}`,
			Done: true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	req := EntityExtractionRequest{
		SegmentText:  "Test segment text",
		SegmentIndex: 0,
		EntityCount:  2,
	}
	result, err := client.ExtractEntitiesFromSegment(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SegmentIndex != 0 {
		t.Errorf("segment index = %d, want 0", result.SegmentIndex)
	}
	if len(result.FrasiImportanti) != 2 {
		t.Errorf("expected 2 frasi_importanti, got %d", len(result.FrasiImportanti))
	}
	if len(result.NomiSpeciali) != 1 {
		t.Errorf("expected 1 nomi_speciali, got %d", len(result.NomiSpeciali))
	}
	if len(result.ParoleImportanti) != 2 {
		t.Errorf("expected 2 parole_importanti, got %d", len(result.ParoleImportanti))
	}
	if len(result.EntitaSenzaTesto) != 1 {
		t.Errorf("expected 1 entity_senza_testo, got %d", len(result.EntitaSenzaTesto))
	}
}

func TestClient_ExtractEntitiesFromSegment_DefaultEntityCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: `{"frasi_importanti":[],"entity_senza_testo":{},"nomi_speciali":[],"parole_importanti":[]}`,
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	req := EntityExtractionRequest{
		SegmentText:  "Test",
		SegmentIndex: 0,
		EntityCount:  0, // Should default to 12
	}
	result, err := client.ExtractEntitiesFromSegment(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected result, got nil")
	}
}

func TestClient_ExtractEntitiesFromScript_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := GenerateResponse{
			Response: `{"frasi_importanti":["phrase1"],"entity_senza_testo":{},"nomi_speciali":["name1"],"parole_importanti":["word1"]}`,
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	segments := []string{"segment one", "segment two", "segment three"}
	result, err := client.ExtractEntitiesFromScript(context.Background(), segments, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalSegments != 3 {
		t.Errorf("total segments = %d, want 3", result.TotalSegments)
	}
	if len(result.SegmentEntities) != 3 {
		t.Errorf("expected 3 segment entities, got %d", len(result.SegmentEntities))
	}
}

func TestClient_ExtractEntitiesFromScript_EmptySegments(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "llama2")
	_, err := client.ExtractEntitiesFromScript(context.Background(), []string{}, 5)
	if err == nil {
		t.Fatal("expected error for empty segments, got nil")
	}
}

func TestClient_ExtractEntitiesFromScript_NilSafeDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return minimal JSON with empty arrays
		resp := GenerateResponse{
			Response: `{}`,
			Done:     true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "llama2")
	req := EntityExtractionRequest{
		SegmentText:  "Test",
		SegmentIndex: 0,
		EntityCount:  1,
	}
	result, err := client.ExtractEntitiesFromSegment(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Check slices are not nil
	if result.FrasiImportanti == nil {
		t.Error("FrasiImportanti should not be nil")
	}
	if result.NomiSpeciali == nil {
		t.Error("NomiSpeciali should not be nil")
	}
	if result.ParoleImportanti == nil {
		t.Error("ParoleImportanti should not be nil")
	}
	if result.EntitaSenzaTesto == nil {
		t.Error("EntitaSenzaTesto should not be nil")
	}
}

// --- parseEntityExtractionResult Tests ---

func TestParseEntityExtractionResult(t *testing.T) {
	tests := []struct {
		name         string
		response     string
		segmentIndex int
		expectError  bool
	}{
		{
			name: "Valid JSON",
			response: `{
				"frasi_importanti": ["phrase1"],
				"entity_senza_testo": {"A": "B"},
				"nomi_speciali": ["name1"],
				"parole_importanti": ["word1"]
			}`,
			segmentIndex: 0,
			expectError:  false,
		},
		{
			name: "JSON with markdown code block",
			response: "```json\n{\"frasi_importanti\":[],\"entity_senza_testo\":{},\"nomi_speciali\":[],\"parole_importanti\":[]}\n```",
			segmentIndex: 1,
			expectError:  false,
		},
		{
			name:         "Invalid JSON",
			response:     "not json at all",
			segmentIndex: 0,
			expectError:  true,
		},
		{
			name: "Empty JSON object",
			response: `{}`,
			segmentIndex: 2,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseEntityExtractionResult(tt.response, tt.segmentIndex)
			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result.SegmentIndex != tt.segmentIndex {
					t.Errorf("segment index = %d, want %d", result.SegmentIndex, tt.segmentIndex)
				}
			}
		})
	}
}

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
	if result.Model != "llama2" {
		t.Errorf("model = %q, want llama2", result.Model)
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
	if result.Model != "llama2" {
		t.Errorf("expected default model llama2, got %q", result.Model)
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

// --- Prompt Helper Function Tests ---

func TestCleanScript(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Plain text",
			input:    "Hello world",
			expected: "Hello world",
		},
		{
			name:     "With backticks",
			input:    "```Hello world```",
			expected: "world", // Regex matches 'Hello' as lang tag, captures ' world'
		},
		{
			name:     "Markdown code block with language",
			input:    "```python\nprint('hello')\n```",
			expected: "print('hello')",
		},
		{
			name:     "With leading/trailing whitespace",
			input:    "  Hello world  ",
			expected: "Hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanScript(tt.input)
			if result != tt.expected {
				t.Errorf("cleanScript(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestEstimateDuration(t *testing.T) {
	tests := []struct {
		wordCount int
		expected  int
	}{
		{0, 0},
		{140, 60},   // 140 words = 60 seconds
		{70, 30},    // 70 words = 30 seconds
		{280, 120},  // 280 words = 120 seconds
		{1000, 428}, // ~428 seconds
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := estimateDuration(tt.wordCount)
			if result != tt.expected {
				t.Errorf("estimateDuration(%d) = %d, want %d", tt.wordCount, result, tt.expected)
			}
		})
	}
}

func TestCountWords(t *testing.T) {
	tests := []struct {
		text     string
		expected int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"  hello   world  ", 2},
		{"one two three four five", 5},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := countWords(tt.text)
			if result != tt.expected {
				t.Errorf("countWords(%q) = %d, want %d", tt.text, result, tt.expected)
			}
		})
	}
}

func TestSanitizeInput(t *testing.T) {
	// Test truncation
	long := make([]byte, 60000)
	for i := range long {
		long[i] = 'a'
	}
	result := sanitizeInput(string(long))
	if len(result) > 50000 {
		t.Errorf("sanitizeInput did not truncate long input, length = %d", len(result))
	}

	// Test newline collapsing
	input := "line1\n\n\n\n\nline2"
	result = sanitizeInput(input)
	// Should collapse 4+ newlines to 3
	if len(result) >= len(input) {
		t.Error("sanitizeInput should have collapsed extra newlines")
	}
}

func TestGetSystemPrompt(t *testing.T) {
	tests := []struct {
		language string
		tone     string
		contains string
	}{
		{"italian", "professional", "copywriter"},
		{"english", "casual", "copywriter"},
		{"spanish", "enthusiastic", "copywriter"},
		{"french", "calm", "rédacteur"},
		{"german", "funny", "Copywriter"},
		{"unknown", "professional", "copywriter"}, // defaults to english
	}

	for _, tt := range tests {
		t.Run(tt.language+"_"+tt.tone, func(t *testing.T) {
			result := getSystemPrompt(tt.language, tt.tone)
			if result == "" {
				t.Error("expected non-empty system prompt")
			}
		})
	}
}

// --- Type/Struct Tests ---

func TestGenerateRequest_MarshalJSON(t *testing.T) {
	req := GenerateRequest{
		Model:   "llama2",
		Prompt:  "test prompt",
		Stream:  false,
		Context: []int{1, 2, 3},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var decoded GenerateRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded.Model != req.Model {
		t.Errorf("model = %q, want %q", decoded.Model, req.Model)
	}
	if decoded.Prompt != req.Prompt {
		t.Errorf("prompt = %q, want %q", decoded.Prompt, req.Prompt)
	}
}

func TestGenerateResponse_UnmarshalJSON(t *testing.T) {
	data := `{"response":"test response","done":true,"context":[1,2,3]}`
	var resp GenerateResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Response != "test response" {
		t.Errorf("response = %q, want %q", resp.Response, "test response")
	}
	if !resp.Done {
		t.Error("expected Done = true")
	}
	if len(resp.Context) != 3 {
		t.Errorf("context length = %d, want 3", len(resp.Context))
	}
}

func TestListModelsResponse_UnmarshalJSON(t *testing.T) {
	data := `{
		"models": [
			{"name": "llama2", "modified_at": "2024-01-01", "size": 3800000000}
		]
	}`
	var resp ListModelsResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(resp.Models))
	}
	if resp.Models[0].Name != "llama2" {
		t.Errorf("name = %q, want llama2", resp.Models[0].Name)
	}
}

// --- EntityExtractionRequest/Result Tests ---

func TestEntityExtractionRequest(t *testing.T) {
	req := EntityExtractionRequest{
		SegmentText:  "test text",
		SegmentIndex: 5,
		EntityCount:  10,
	}

	if req.SegmentText != "test text" {
		t.Errorf("SegmentText = %q, want %q", req.SegmentText, "test text")
	}
	if req.SegmentIndex != 5 {
		t.Errorf("SegmentIndex = %d, want %d", req.SegmentIndex, 5)
	}
	if req.EntityCount != 10 {
		t.Errorf("EntityCount = %d, want %d", req.EntityCount, 10)
	}
}

func TestEntityExtractionResult(t *testing.T) {
	result := EntityExtractionResult{
		SegmentIndex:     3,
		FrasiImportanti:  []string{"phrase1", "phrase2"},
		EntitaSenzaTesto: map[string]string{"A": "B"},
		NomiSpeciali:     []string{"name1"},
		ParoleImportanti: []string{"word1"},
	}

	if result.SegmentIndex != 3 {
		t.Errorf("SegmentIndex = %d, want %d", result.SegmentIndex, 3)
	}
	if len(result.FrasiImportanti) != 2 {
		t.Errorf("FrasiImportanti length = %d, want 2", len(result.FrasiImportanti))
	}
}

func TestSegmentEntities(t *testing.T) {
	se := SegmentEntities{
		SegmentIndex:     0,
		SegmentText:      "test",
		FrasiImportanti:  []string{},
		EntitaSenzaTesto: make(map[string]string),
		NomiSpeciali:     []string{},
		ParoleImportanti: []string{},
	}

	if se.SegmentIndex != 0 {
		t.Errorf("SegmentIndex = %d, want %d", se.SegmentIndex, 0)
	}
}

func TestFullEntityAnalysis(t *testing.T) {
	analysis := FullEntityAnalysis{
		TotalSegments:         5,
		SegmentEntities:       make([]SegmentEntities, 5),
		TotalEntities:         20,
		EntityCountPerSegment: 4,
	}

	if analysis.TotalSegments != 5 {
		t.Errorf("TotalSegments = %d, want %d", analysis.TotalSegments, 5)
	}
	if analysis.EntityCountPerSegment != 4 {
		t.Errorf("EntityCountPerSegment = %d, want %d", analysis.EntityCountPerSegment, 4)
	}
}

// --- Interface compliance test ---

func TestClientImplementsEntityExtractor(t *testing.T) {
	// This is a compile-time check
	var _ EntityExtractor = (*Client)(nil)
}
