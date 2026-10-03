package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// keepAliveCaptureServer records the top-level keep_alive of every request.
func keepAliveCaptureServer(t *testing.T, response string) (*httptest.Server, *[]string) {
	t.Helper()
	// Every env-driven test re-arms the once-guard on cleanup so later tests in
	// the package observe the pristine environment instead of this test's
	// cached override.
	t.Cleanup(resetEnvKeepAliveForTest)
	captured := &[]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if v, ok := body["keep_alive"].(string); ok {
			*captured = append(*captured, v)
		} else {
			*captured = append(*captured, "")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server, captured
}

func TestResidentKeepAliveDefaultsTo30mWhenEnvUnset(t *testing.T) {
	t.Cleanup(resetEnvKeepAliveForTest)
	t.Setenv(EnvResidentKeepAlive, "")
	resetEnvKeepAliveForTest()
	if got := residentKeepAlive(); got != residentKeepAliveDefault {
		t.Fatalf("residentKeepAlive() = %q, want default %q", got, residentKeepAliveDefault)
	}
}

func TestResidentKeepAliveEnvOverridePropagatesToChatAndGenerate(t *testing.T) {
	t.Setenv(EnvResidentKeepAlive, "12h")
	resetEnvKeepAliveForTest()
	if got := residentKeepAlive(); got != "12h" {
		t.Fatalf("residentKeepAlive() = %q, want 12h", got)
	}

	chatServer, chatSeen := keepAliveCaptureServer(t, `{"message":{"content":"ok"},"done":true}`)
	c := NewClient(chatServer.URL, "gemma4:e4b", 5)
	if _, err := c.ChatDetailed(context.Background(), []types.Message{{Role: "system", Content: "x"}}, nil, nil); err != nil {
		t.Fatalf("ChatDetailed: %v", err)
	}
	if len(*chatSeen) != 1 || (*chatSeen)[0] != "12h" {
		t.Fatalf("chat keep_alive = %v, want [12h]", *chatSeen)
	}

	genServer, genSeen := keepAliveCaptureServer(t, `{"response":"ok","done":true}`)
	g := NewClient(genServer.URL, "gemma4:e4b", 5)
	if _, err := g.GenerateDetailed(context.Background(), "gemma4:e4b", "hello", nil); err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if len(*genSeen) != 1 || (*genSeen)[0] != "12h" {
		t.Fatalf("generate keep_alive = %v, want [12h]", *genSeen)
	}
}

func TestResidentKeepAliveNegativeOneIsAccepted(t *testing.T) {
	t.Cleanup(resetEnvKeepAliveForTest)
	t.Setenv(EnvResidentKeepAlive, "-1")
	resetEnvKeepAliveForTest()
	if got := residentKeepAlive(); got != "-1" {
		t.Fatalf("residentKeepAlive() = %q, want -1", got)
	}
}

func TestResidentKeepAliveInvalidOverrideFallsBackToDefault(t *testing.T) {
	t.Cleanup(resetEnvKeepAliveForTest)
	t.Setenv(EnvResidentKeepAlive, "forever-and-ever")
	resetEnvKeepAliveForTest()
	if got := residentKeepAlive(); got != residentKeepAliveDefault {
		t.Fatalf("residentKeepAlive() = %q, want fallback %q", got, residentKeepAliveDefault)
	}
}

func TestValidOllamaKeepAliveAcceptsOllamaContract(t *testing.T) {
	t.Cleanup(resetEnvKeepAliveForTest)
	valid := []string{"-1", "0", "30", "30m", "12h", "90s", "1500ms", "1.5m"}
	for _, v := range valid {
		if !validOllamaKeepAlive(v) {
			t.Fatalf("validOllamaKeepAlive(%q) = false, want true", v)
		}
	}
	invalid := []string{"", "forever", "12h30", "1.5", "-5m", "hm", "12 H"}
	for _, v := range invalid {
		if validOllamaKeepAlive(v) {
			t.Fatalf("validOllamaKeepAlive(%q) = true, want false", v)
		}
	}
}

func TestExplicitCallerKeepAliveStillWinsOverEnv(t *testing.T) {
	t.Cleanup(resetEnvKeepAliveForTest)
	t.Setenv(EnvResidentKeepAlive, "12h")
	resetEnvKeepAliveForTest()
	server, seen := keepAliveCaptureServer(t, `{"response":"ok","done":true}`)
	c := NewClient(server.URL, "gemma4:e4b", 5)
	if _, err := c.GenerateDetailed(context.Background(), "gemma4:e4b", "hello", map[string]any{"keep_alive": "45m"}); err != nil {
		t.Fatalf("GenerateDetailed: %v", err)
	}
	if len(*seen) != 1 || (*seen)[0] != "45m" {
		t.Fatalf("explicit keep_alive = %v, want [45m]", *seen)
	}
}
