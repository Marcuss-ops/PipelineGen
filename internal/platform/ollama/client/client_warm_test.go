package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

func TestWarmModelLoadsOnceAndVerifiesResidency(t *testing.T) {
	var psCalls atomic.Int32
	var chatCalls atomic.Int32
	var chatBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/ps":
			call := psCalls.Add(1)
			if call == 1 {
				_, _ = w.Write([]byte(`{"models":[]}`))
				return
			}
			_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:e4b@sha256:test","context_length":8192}]}`))
		case "/api/chat":
			chatCalls.Add(1)
			if err := json.NewDecoder(r.Body).Decode(&chatBody); err != nil {
				t.Errorf("decode chat request: %v", err)
			}
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	if err := c.WarmModel(context.Background(), "gemma4:e4b"); err != nil {
		t.Fatalf("WarmModel: %v", err)
	}
	if got := psCalls.Load(); got != 2 {
		t.Fatalf("/api/ps calls = %d, want pre- and post-warm verification", got)
	}
	if got := chatCalls.Load(); got != 1 {
		t.Fatalf("/api/chat calls = %d, want one warm request", got)
	}
	if got, _ := chatBody["keep_alive"].(string); got != "30m" {
		t.Fatalf("keep_alive = %q, want 30m", got)
	}
	if got, _ := chatBody["model"].(string); got != "gemma4:e4b" {
		t.Fatalf("model = %q, want configured model", got)
	}
	options, _ := chatBody["options"].(map[string]any)
	if got := options["num_ctx"]; got != float64(types.ProductionRunnerContext) {
		t.Fatalf("options.num_ctx = %v, want %d resident runner", got, types.ProductionRunnerContext)
	}
}

func TestWarmModelSkipsChatWhenAlreadyResident(t *testing.T) {
	var psCalls atomic.Int32
	var chatCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/ps" {
			psCalls.Add(1)
			_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:e4b","context_length":8192}]}`))
			return
		}
		if r.URL.Path == "/api/chat" {
			chatCalls.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	if err := c.WarmModel(context.Background(), "gemma4:e4b"); err != nil {
		t.Fatalf("WarmModel: %v", err)
	}
	if got := psCalls.Load(); got != 1 {
		t.Fatalf("/api/ps calls = %d, want one resident check", got)
	}
	if got := chatCalls.Load(); got != 0 {
		t.Fatalf("/api/chat calls = %d, want zero for resident model", got)
	}
}

func TestWarmModelReloadsWhenResidentContextIsWrong(t *testing.T) {
	var psCalls atomic.Int32
	var chatCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/ps":
			call := psCalls.Add(1)
			if call == 1 {
				_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:e4b","context_length":2048}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:e4b","context_length":8192}]}`))
		case "/api/chat":
			chatCalls.Add(1)
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	if err := c.WarmModel(context.Background(), "gemma4:e4b"); err != nil {
		t.Fatalf("WarmModel: %v", err)
	}
	if got := chatCalls.Load(); got != 1 {
		t.Fatalf("/api/chat calls = %d, want one context correction", got)
	}
}
