package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateDetailedPropagatesKeepAliveAndTiming(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"ok","done":true,"load_duration":120000000,"prompt_eval_count":4,"prompt_eval_duration":80000000,"eval_count":8,"eval_duration":200000000,"total_duration":420000000}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	options := map[string]any{"keep_alive": "45m", "num_predict": 8, "think": false}
	result, err := c.GenerateDetailed(t.Context(), "gemma4:e4b", "hello", options)
	if err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("content = %q, want ok", result.Content)
	}
	if result.Metrics == nil || result.Metrics.ModelLoadMS() != 120 || result.Metrics.TokensPerSecond() != 40 {
		t.Fatalf("unexpected metrics: %#v", result.Metrics)
	}
	if got, ok := body["keep_alive"].(string); !ok || got != "45m" {
		t.Fatalf("top-level keep_alive = %#v, want 45m", body["keep_alive"])
	}
	if _, nested := body["options"].(map[string]any)["keep_alive"]; nested {
		t.Fatal("keep_alive must not be nested in options")
	}
	if _, nested := body["options"].(map[string]any)["think"]; nested {
		t.Fatal("think must not be nested in options")
	}
	if options["keep_alive"] != "45m" || options["think"] != false {
		t.Fatalf("caller options mutated: %#v", options)
	}
}

func TestGenerateDetailedUsesResidentDefaultKeepAlive(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":"ok","done":true}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 5)
	if _, err := c.GenerateDetailed(t.Context(), "", "hello", nil); err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if got, ok := body["keep_alive"].(string); !ok || got != "30m" {
		t.Fatalf("default keep_alive = %#v, want 30m", body["keep_alive"])
	}
}
