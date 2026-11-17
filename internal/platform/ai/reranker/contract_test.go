package reranker

import (
	"encoding/json"
	"testing"
)

// TestWireContractUsesVectorScore pins the JSON contract shared with the Python
// sidecar (scripts/services/reranker_server.py). The 2026-11 vector rename
// made vector_score the candidate/result key; a sidecar still emitting the
// retired spelling would silently drop the pgvector similarity because
// encoding/json ignores unknown fields.
func TestWireContractUsesVectorScore(t *testing.T) {
	score := 0.42
	request := Request{
		Query: "query",
		Candidates: []Candidate{{
			ID:          "asset-1",
			Text:        "candidate",
			VectorScore: &score,
		}},
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var requestPayload struct {
		Candidates []map[string]json.RawMessage `json:"candidates"`
	}
	if err := json.Unmarshal(requestJSON, &requestPayload); err != nil {
		t.Fatalf("request: decode candidates: %v", err)
	}
	if len(requestPayload.Candidates) != 1 {
		t.Fatalf("request: candidates = %d, want 1", len(requestPayload.Candidates))
	}
	if _, ok := requestPayload.Candidates[0]["vector_score"]; !ok {
		t.Fatalf("request candidate: key %q missing (payload: %s)", "vector_score", requestJSON)
	}

	responseJSON := []byte(`{"results":[{"id":"asset-1","rerank_score":0.91,"vector_score":0.42}]}`)
	var response Response
	if err := json.Unmarshal(responseJSON, &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(response.Results))
	}
	if response.Results[0].VectorScore == nil || *response.Results[0].VectorScore != score {
		t.Fatalf("VectorScore = %v, want %v", response.Results[0].VectorScore, score)
	}

	resultJSON, err := json.Marshal(response.Results[0])
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	assertJSONKey(t, "result", resultJSON, "vector_score", true)
}

func assertJSONKey(t *testing.T, surface string, payload []byte, key string, want bool) {
	t.Helper()
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("%s: decode object: %v", surface, err)
	}
	if _, ok := decoded[key]; ok != want {
		t.Fatalf("%s: key %q present=%v, want %v (payload: %s)", surface, key, ok, want, payload)
	}
}
