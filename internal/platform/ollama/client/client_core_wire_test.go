package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

func TestChatKeepAliveIsTopLevel(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"content":"ok"},"done":true}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	options := map[string]any{"keep_alive": "5m", "num_ctx": 2048}
	if _, err := c.Chat(t.Context(), []types.Message{{Role: "user", Content: "hello"}}, options, nil); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got, ok := body["keep_alive"].(string); !ok || got != "5m" {
		t.Fatalf("top-level keep_alive = %#v, want 5m", body["keep_alive"])
	}
	if _, nested := body["options"].(map[string]any)["keep_alive"]; nested {
		t.Fatal("keep_alive must not be nested in options")
	}
	if got := options["keep_alive"]; got != "5m" {
		t.Fatalf("caller options mutated: keep_alive=%v", got)
	}
}
