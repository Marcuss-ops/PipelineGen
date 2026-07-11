// Package workerruntime — preflight_test.go (July 2026).
//
// Synthetic tests for the worker bootstrap preflight probes. All tests
// use in-process httptest servers so they are fast and deterministic.
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

// readyResponse mirrors the /ready payload shape produced by the
// server's HealthHandler.
type readyResponse struct {
	OK     bool                  `json:"ok"`
	Status string                `json:"status"`
	Checks map[string]readyCheck `json:"checks"`
}

type readyCheck struct {
	OK         bool `json:"ok"`
	Applicable bool `json:"applicable"`
}

func TestPreflightMasterScriptGenerateReady_Healthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
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
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := PreflightMasterScriptGenerateReady(ctx, server.URL); err != nil {
		t.Fatalf("expected ready preflight to pass, got: %v", err)
	}
}

func TestPreflightMasterScriptGenerateReady_NotApplicable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		resp := readyResponse{
			OK:     true,
			Status: "ready",
			Checks: map[string]readyCheck{
				"script_generate": {OK: true, Applicable: false},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := PreflightMasterScriptGenerateReady(ctx, server.URL); err != nil {
		t.Fatalf("expected not-applicable script_generate to pass, got: %v", err)
	}
}

func TestPreflightMasterScriptGenerateReady_MissingCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		resp := readyResponse{
			OK:     true,
			Status: "ready",
			Checks: map[string]readyCheck{
				"db": {OK: true, Applicable: true},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := PreflightMasterScriptGenerateReady(ctx, server.URL); err != nil {
		t.Fatalf("expected missing script_generate check to pass, got: %v", err)
	}
}

func TestPreflightMasterScriptGenerateReady_Unhealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		resp := readyResponse{
			OK:     true,
			Status: "ready",
			Checks: map[string]readyCheck{
				"script_generate": {OK: false, Applicable: true},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := PreflightMasterScriptGenerateReady(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error when script_generate check fails")
	}
	if !strings.Contains(err.Error(), "script_generate") {
		t.Errorf("error should mention script_generate, got: %v", err)
	}
}

func TestPreflightMasterScriptGenerateReady_ReadyNotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		resp := readyResponse{
			OK:     false,
			Status: "not ready",
			Checks: map[string]readyCheck{
				"script_generate": {OK: false, Applicable: true},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := PreflightMasterScriptGenerateReady(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error when /ready reports ok=false")
	}
	if !strings.Contains(err.Error(), "unhealthy") {
		t.Errorf("error should mention unhealthy, got: %v", err)
	}
}

func TestPreflightMasterScriptGenerateReady_UnhealthyWithErrorAndNote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		// Use a raw JSON payload so we can include the optional `error`
		// and `note` fields inside the script_generate check object.
		body := []byte(`{
			"ok": true,
			"status": "ready",
			"checks": {
				"script_generate": {
					"ok": false,
					"applicable": true,
					"error": "model not loaded",
					"note": "warmup pending"
				}
			}
		}`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := PreflightMasterScriptGenerateReady(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error when script_generate check fails with diagnostics")
	}
	if !strings.Contains(err.Error(), "model not loaded") {
		t.Errorf("error should surface script_generate error, got: %v", err)
	}
}

func TestPreflightMasterScriptGenerateReady_BadStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := PreflightMasterScriptGenerateReady(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error when /ready returns 500")
	}
}

func TestPreflightMasterScriptGenerateReady_MalformedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not-json"))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := PreflightMasterScriptGenerateReady(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error when /ready body is malformed")
	}
}

func TestPreflightMasterScriptGenerateReady_EventuallyReady(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			http.NotFound(w, r)
			return
		}
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
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := PreflightMasterScriptGenerateReady(ctx, server.URL); err != nil {
		t.Fatalf("expected preflight to pass after master becomes ready, got: %v", err)
	}
	if calls < 2 {
		t.Fatalf("expected at least 2 calls to /ready, got %d", calls)
	}
}

func TestPreflightMasterHealth_OK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := PreflightMasterHealth(context.Background(), server.URL); err != nil {
		t.Fatalf("expected /health preflight to pass, got: %v", err)
	}
}

func TestPreflightMasterHealth_NotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := PreflightMasterHealth(ctx, server.URL)
	if err == nil {
		t.Fatal("expected error when /health returns 503")
	}
}
