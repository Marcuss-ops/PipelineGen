package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
)

// TestGenerateVideoMetadataPinsTopLevelJSONFormat pins the constrained-decoding
// contract of the video-metadata call (B2, TODO-pipeline-100x-velocita): the
// /api/chat body MUST carry a TOP-LEVEL `format` of "json" so Ollama cannot
// spend the call on prose-wrapped JSON that the tolerant parser then discards.
func TestGenerateVideoMetadataPinsTopLevelJSONFormat(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"{\"description\":\"d\",\"tags\":[\"t\"]}"}}`))
	}))
	defer server.Close()

	g := NewGenerator(client.NewClient(server.URL, "gemma4:e2b", 5))
	description, tags, err := g.GenerateVideoMetadataWithModel(context.Background(), "a title", "gemma4:e2b")
	if err != nil {
		t.Fatalf("GenerateVideoMetadataWithModel: %v", err)
	}
	if description != "d" || len(tags) != 1 || tags[0] != "t" {
		t.Fatalf("unexpected metadata: %q %v", description, tags)
	}
	if captured["format"] != "json" {
		t.Fatalf("top-level format = %v, want \"json\" (constrained decoding)", captured["format"])
	}
}

// TestGenerateVideoMetadataMalformedJSONStillParsesTolerantly pins the
// backward-compatible failure behaviour: with constrained decoding the decoder
// cannot produce broken JSON, but a caller-compatible graceful answer stays
// available (description verbatim, empty tags) instead of an error.
func TestGenerateVideoMetadataMalformedJSONStillParsesTolerantly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"plain prose answer"}}`))
	}))
	defer server.Close()

	g := NewGenerator(client.NewClient(server.URL, "gemma4:e2b", 5))
	description, _, err := g.GenerateVideoMetadataWithModel(context.Background(), "a title", "gemma4:e2b")
	if err != nil {
		t.Fatalf("GenerateVideoMetadataWithModel: %v", err)
	}
	if description != "plain prose answer" {
		t.Fatalf("description = %q, want verbatim prose fallback", description)
	}
}
