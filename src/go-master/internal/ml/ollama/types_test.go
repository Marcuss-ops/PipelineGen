package ollama

import (
	"encoding/json"
	"testing"
)

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
