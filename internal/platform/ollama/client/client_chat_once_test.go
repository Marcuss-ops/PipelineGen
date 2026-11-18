package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

func TestChatOnceDoesNotRetryTransientFailures(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.URL, "vision-test", 5)
	messages := []types.Message{{Role: "user", Content: "verify this image"}}

	if _, err := client.ChatOnce(context.Background(), messages, nil, nil); err == nil {
		t.Fatal("ChatOnce must return the transient error")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("ChatOnce sent %d requests, want exactly 1 (no hidden retry)", got)
	}

	if _, err := client.Chat(context.Background(), messages, nil, nil); err == nil {
		t.Fatal("Chat must return the transient error")
	}
	if got := requests.Load(); got != 4 {
		t.Fatalf("Chat sent %d requests in total, want 4 (1 ChatOnce + 3 Chat retries)", got)
	}
}
