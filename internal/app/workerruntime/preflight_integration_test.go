// Package workerruntime — preflight_integration_test.go (July 2026).
//
// End-to-end tests for the worker bootstrap preflight sequence. Each
// test spins up a real httptest server that serves /health and /ready,
// then exercises the same call sequence that Run() performs before the
// worker is allowed to start claiming jobs.
package workerruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestBootstrapPreflight_MasterHealthyAndScriptGenerateReady verifies the
// happy path: /health returns 200 and /ready reports script_generate as
// applicable and ok. Both preflight probes should pass in sequence.
func TestBootstrapPreflight_MasterHealthyAndScriptGenerateReady(t *testing.T) {
	server := newMasterServer(t, masterServerConfig{
		healthStatus: http.StatusOK,
		readyStatus:  http.StatusOK,
		readyBody: readyResponse{
			OK:     true,
			Status: "ready",
			Checks: map[string]readyCheck{
				"script_generate": {OK: true, Applicable: true},
			},
		},
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := PreflightMasterHealth(ctx, server.URL); err != nil {
		t.Fatalf("/health preflight failed: %v", err)
	}
	if err := PreflightMasterScriptGenerateReady(ctx, server.URL); err != nil {
		t.Fatalf("/ready script_generate preflight failed: %v", err)
	}
}

// TestBootstrapPreflight_MasterHealthyButScriptGenerateNotReady verifies that
// the worker bootstrap fails when /health passes but /ready reports
// script_generate as applicable and not ok.
func TestBootstrapPreflight_MasterHealthyButScriptGenerateNotReady(t *testing.T) {
	server := newMasterServer(t, masterServerConfig{
		healthStatus: http.StatusOK,
		readyStatus:  http.StatusOK,
		readyBody: readyResponse{
			OK:     true,
			Status: "ready",
			Checks: map[string]readyCheck{
				"script_generate": {OK: false, Applicable: true},
			},
		},
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if err := PreflightMasterHealth(ctx, server.URL); err != nil {
		t.Fatalf("/health preflight failed: %v", err)
	}
	err := PreflightMasterScriptGenerateReady(ctx, server.URL)
	if err == nil {
		t.Fatal("expected /ready script_generate preflight to fail")
	}
	if !strings.Contains(err.Error(), "script_generate") {
		t.Errorf("error should mention script_generate, got: %v", err)
	}
}

// TestBootstrapPreflight_MasterUnhealthy verifies that a failing /health
// preflight stops the bootstrap before /ready is ever consulted.
func TestBootstrapPreflight_MasterUnhealthy(t *testing.T) {
	readyCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/ready":
			readyCalls++
			w.WriteHeader(http.StatusOK)
			resp := readyResponse{
				OK:     true,
				Status: "ready",
				Checks: map[string]readyCheck{
					"script_generate": {OK: true, Applicable: true},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := PreflightMasterHealth(ctx, server.URL)
	if err == nil {
		t.Fatal("expected /health preflight to fail")
	}
	if readyCalls > 0 {
		t.Fatalf("/ready should not be consulted when /health fails; got %d calls", readyCalls)
	}
}

// TestBootstrapPreflight_ScriptGenerateNotApplicable verifies that a worker
// bootstrap passes when the master reports script_generate as not applicable.
func TestBootstrapPreflight_ScriptGenerateNotApplicable(t *testing.T) {
	server := newMasterServer(t, masterServerConfig{
		healthStatus: http.StatusOK,
		readyStatus:  http.StatusOK,
		readyBody: readyResponse{
			OK:     true,
			Status: "ready",
			Checks: map[string]readyCheck{
				"script_generate": {OK: false, Applicable: false},
			},
		},
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := PreflightMasterHealth(ctx, server.URL); err != nil {
		t.Fatalf("/health preflight failed: %v", err)
	}
	if err := PreflightMasterScriptGenerateReady(ctx, server.URL); err != nil {
		t.Fatalf("/ready script_generate preflight failed: %v", err)
	}
}

// TestBootstrapPreflight_ScriptGenerateMissing verifies that a worker
// bootstrap passes when the master predates the script_generate check.
func TestBootstrapPreflight_ScriptGenerateMissing(t *testing.T) {
	server := newMasterServer(t, masterServerConfig{
		healthStatus: http.StatusOK,
		readyStatus:  http.StatusOK,
		readyBody: readyResponse{
			OK:     true,
			Status: "ready",
			Checks: map[string]readyCheck{
				"db": {OK: true, Applicable: true},
			},
		},
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := PreflightMasterHealth(ctx, server.URL); err != nil {
		t.Fatalf("/health preflight failed: %v", err)
	}
	if err := PreflightMasterScriptGenerateReady(ctx, server.URL); err != nil {
		t.Fatalf("/ready script_generate preflight failed: %v", err)
	}
}

// TestBootstrapPreflight_MasterReadyEventuallyScriptGenerateReady verifies that
// the bootstrap waits through transient /ready failures and succeeds once
// script_generate becomes ready.
func TestBootstrapPreflight_MasterReadyEventuallyScriptGenerateReady(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/ready":
			calls++
			if calls < 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				resp := readyResponse{
					OK:     false,
					Status: "not ready",
					Checks: map[string]readyCheck{
						"script_generate": {OK: false, Applicable: true},
					},
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			resp := readyResponse{
				OK:     true,
				Status: "ready",
				Checks: map[string]readyCheck{
					"script_generate": {OK: true, Applicable: true},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := PreflightMasterHealth(ctx, server.URL); err != nil {
		t.Fatalf("/health preflight failed: %v", err)
	}
	if err := PreflightMasterScriptGenerateReady(ctx, server.URL); err != nil {
		t.Fatalf("/ready script_generate preflight failed: %v", err)
	}
	if calls < 2 {
		t.Fatalf("expected at least 2 calls to /ready, got %d", calls)
	}
}

// masterServerConfig configures the fake master for an integration test.
type masterServerConfig struct {
	healthStatus int
	readyStatus  int
	readyBody    readyResponse
}

// newMasterServer builds an httptest server that serves /health and /ready
// according to cfg. It fails the test if the /ready body cannot be encoded.
func newMasterServer(t *testing.T, cfg masterServerConfig) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(cfg.healthStatus)
		case "/ready":
			w.WriteHeader(cfg.readyStatus)
			body, err := json.Marshal(cfg.readyBody)
			if err != nil {
				t.Fatalf("marshal /ready body: %v", err)
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
}
