package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveWarmTimeoutDefaultAndOverride(t *testing.T) {
	t.Setenv(EnvWarmTimeoutSeconds, "")
	if got := resolveWarmTimeout(); got != defaultWarmTimeout {
		t.Fatalf("default warm timeout = %s, want %s", got, defaultWarmTimeout)
	}
	t.Setenv(EnvWarmTimeoutSeconds, "45")
	if got := resolveWarmTimeout(); got != 45*time.Second {
		t.Fatalf("warm timeout override = %s, want 45s", got)
	}
	t.Setenv(EnvWarmTimeoutSeconds, "not-a-duration")
	if got := resolveWarmTimeout(); got != defaultWarmTimeout {
		t.Fatalf("invalid warm timeout = %s, want fallback %s", got, defaultWarmTimeout)
	}
}

// TestWarmModelUsesItsOwnTimeout proves the cold-load probe cannot borrow the
// 600-second generation budget: a chat request that never answers is cancelled
// by the warm timeout, not by the caller's much longer context.
func TestWarmModelUsesItsOwnTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/ps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/api/chat":
			// Sleep past the warm timeout. The client must cancel on its own;
			// the handler does not cooperate so a leak in the client is visible.
			time.Sleep(3 * time.Second)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv(EnvWarmTimeoutSeconds, "1")
	c := NewClient(server.URL, "gemma4:e2b", 30)
	started := time.Now()
	err := c.WarmModel(context.Background(), "gemma4:e2b")
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("WarmModel must fail when the model never becomes resident")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("WarmModel used %s, want the 1s warm timeout (not the 30s generation timeout)", elapsed)
	}
}
