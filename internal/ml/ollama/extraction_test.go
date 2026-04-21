package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- Extraction Tests ---

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

func TestClientImplementsEntityExtractor(t *testing.T) {
	// This is a compile-time check
	var _ EntityExtractor = (*Client)(nil)
}
