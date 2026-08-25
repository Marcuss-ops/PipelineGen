package audit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProbeTTSHealth_HappyPath: well-behaved /health 200 → Reachable=true.
func TestProbeTTSHealth_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res := probeTTSHealth(context.Background(), srv.URL)
	if !res.Reachable {
		t.Fatalf("reachable=false after OK probe; err=%q", res.Error)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status_code=%d want 200", res.StatusCode)
	}
	if !strings.HasSuffix(res.URL, "/health") {
		t.Fatalf("url=%q want /health suffix", res.URL)
	}
	if res.ProbeTimeISO == "" {
		t.Fatalf("probe_time_iso empty")
	}
}

// TestProbeTTSHealth_Unreachable: closed listener forces http.DefaultClient
// to fail fast. Expects Reachable=false + populated Error.
func TestProbeTTSHealth_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // immediate close → connection refused on next dial

	res := probeTTSHealth(context.Background(), srv.URL)
	if res.Reachable {
		t.Fatalf("reachable=true on closed server; status=%d", res.StatusCode)
	}
	if res.Error == "" {
		t.Fatalf("error empty on unreachable probe")
	}
}

// TestProbeTTSHealth_5xxMarksUnreachable: every 5xx response is NOT
// reachable (#M1 MAJOR per code-reviewer). godlike/07 NO-FAKE-AVAILABILITY
// only a genuine 200 counts as healthy. Table-driven so a future 5xx
// variant is added with a single new struct entry.
func TestProbeTTSHealth_5xxMarksUnreachable(t *testing.T) {
	cases := []struct {
		code int
		name string
	}{
		{http.StatusInternalServerError, "InternalServerError"}, // 500
		{http.StatusBadGateway, "BadGateway"},                   // 502
		{http.StatusServiceUnavailable, "ServiceUnavailable"},   // 503
		{http.StatusGatewayTimeout, "GatewayTimeout"},           // 504
	}
	for _, c := range cases {
		c := c
		t.Run(fmt.Sprintf("status_%d_%s", c.code, c.name), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.code)
			}))
			defer srv.Close()

			res := probeTTSHealth(context.Background(), srv.URL)
			if res.Reachable {
				t.Fatalf("reachable=true on %d status", c.code)
			}
			expected := fmt.Sprintf("%d", c.code)
			if !strings.Contains(res.Error, expected) {
				t.Fatalf("error=%q want substring %q", res.Error, expected)
			}
			if res.StatusCode != c.code {
				t.Fatalf("status_code=%d want %d", res.StatusCode, c.code)
			}
		})
	}
}

// TestRunVoiceoverHealth_PrintsJSON_HappyPath: dispatcher printer does
// not panic on a valid --base-url + healthy server.
func TestRunVoiceoverHealth_PrintsJSON_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := RunVoiceoverHealth([]string{"--base-url", srv.URL}); err != nil {
		t.Fatalf("RunVoiceoverHealth err=%v", err)
	}
}

// TestRunVoiceoverHealth_FlagsParsed: --base-url override is honoured.
func TestRunVoiceoverHealth_FlagsParsed(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := RunVoiceoverHealth([]string{"--base-url", srv.URL, "/generate"}); err != nil {
		t.Fatalf("RunVoiceoverHealth err=%v", err)
	}
	if !called {
		t.Fatalf("httptest server was not hit; --base-url override did not propagate")
	}
}

// TestRunVoiceoverHealth_FailsOnUnreachable_WhenFlagSet (MAJOR #2 per
// code-reviewer, godlike/07 typed-error contract). The --fail-on-unreachable
// flag flips the exit code to non-zero when the worker is unreachable, so CI
// gates can rely on a binary signal. JSON envelope stays byte-stable for
// jq pipelines.
func TestRunVoiceoverHealth_FailsOnUnreachable_WhenFlagSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // immediate close triggers connection refused on next dial

	err := RunVoiceoverHealth([]string{"--base-url", srv.URL, "--fail-on-unreachable"})
	if err == nil {
		t.Fatalf("expected non-nil error from --fail-on-unreachable on closed listener")
	}
	if !strings.Contains(err.Error(), "TTS worker unreachable") {
		t.Fatalf("error=%q want substring 'TTS worker unreachable'", err.Error())
	}
}

// TestRunVoiceoverHealth_FailOnUnreachable_HealthyOK (godlike/07
// NO-FAKE-AVAILABILITY): --fail-on-unreachable MUST NOT fail on a healthy
// worker (preserve the canonical happy-path).
func TestRunVoiceoverHealth_FailOnUnreachable_HealthyOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := RunVoiceoverHealth([]string{"--base-url", srv.URL, "--fail-on-unreachable"}); err != nil {
		t.Fatalf("expected nil error on healthy worker, got %v", err)
	}
}
