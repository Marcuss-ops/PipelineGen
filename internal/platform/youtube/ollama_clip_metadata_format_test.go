package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

const ollamaGeneratePath = "/api" + "/generate"

// TestOllamaClipMetadataBuilderPinsJSONFormat pins the constrained-decoding
// contract of the clip-metadata call (B2, TODO-pipeline-100x-velocita): the
// /api/generate body MUST carry a TOP-LEVEL `format` of "json" so the model
// cannot spend an attempt (or the deterministic-fallback path) on malformed
// prose-wrapped JSON. The tolerant brace-slicing parser stays as defence in
// depth; the wire constraint is the primary defence.
func TestOllamaClipMetadataBuilderPinsJSONFormat(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ollamaGeneratePath {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"{\"clip_summary\":\"s\",\"topics\":[\"t\"],\"speakers\":[],\"mentioned_people\":[],\"sponsor_segment\":false}"}`))
	}))
	defer server.Close()

	builder := NewOllamaClipMetadataBuilder(
		ollamaclient.NewClient(server.URL, "gemma4:e2b", 5),
		"gemma4:e2b",
		5*time.Second,
		nil,
	)
	metadata, err := builder.Build(context.Background(), youtubetypes.ClipMetadataInput{
		ClipID:     "clip-1",
		Title:      "title",
		Transcript: "transcript",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if captured["format"] != "json" {
		t.Fatalf("top-level format = %v, want \"json\" (constrained decoding)", captured["format"])
	}
	if metadata.Summary != "s" {
		t.Fatalf("Summary = %q, want parsed model answer", metadata.Summary)
	}
}
