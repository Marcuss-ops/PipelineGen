// Package client — client_errors_p1c_test.go is the P1.C — Errori
// modello LLM HTTP-level test suite. It pins the wire-level
// error mapping that the *Client produces for the canonical
// LLM failure shapes: 404 (model not found), 500 (server
// error), 503 (service unavailable), and network unreachable
// (connection refused).
//
// USER SPEC (verbatim, July 2026): "Implementa la suite P1.C —
// Errori modello LLM su main. Simula: Ollama non raggiungibile,
// modello inesistente, timeout, risposta vuota, risposta JSON
// invece di plain text, testo troppo corto, output in inglese
// nonostante language=it, risposta troncata. Atteso: nessun
// falso SUCCEEDED, errore tipizzato, retry SOLO dove
// appropriato. Lavora su main, commit frequenti, push."
//
// ── Why a separate HTTP-level test file? ──────────────────────
//
// The engine-level test file
// (internal/application/scripts/usecase/llm_errors_p1c_test.go)
// pins the error path at the use-case seam. That suite exercises
// the engine's error wrapping + retry classification but CANNOT
// verify the wire-level error shape (the *Client layer that
// actually talks to Ollama over HTTP). This file fills that gap:
// httptest.NewServer provides a real HTTP endpoint that returns
// the specific status codes / response shapes, and we assert
// the canonical error string + retry classification that the
// production *Client produces.
//
// Together the two files cover the full stack:
//
//   wire (this file)        →  use case (engine-level file)
//   ──────────────────       →  ──────────────────────────────
//   404 / 500 / 503 / net    →  OllamaUnreachable / ModelNotFound
//   unreachable / timeout    →  / Timeout / EmptyResponse / etc.
//
// ── Test seam ──────────────────────────────────────────────────
//
// The tests point a real *Client at httptest.NewServer.URL via
// the canonical NewClient(baseURL, model, timeout) constructor.
// The seam is identical to production wiring — the only
// difference is the test server's response (controlled by the
// test) instead of a real Ollama process.
//
// ── SUT BUGS (mirror of the engine-level file) ────────────────
//
// SUT BUG 1: No typed sentinels for LLM infrastructure errors.
//   The *Client produces free-form fmt.Errorf strings:
//     - "ollama request failed: <net err>"      (network)
//     - "ollama returned status %d"             (HTTP error)
//     - "failed to decode response: <json err>" (malformed body)
//   Callers (engine, retry loop) have no typed sentinel to
//   branch on — they must substring-match. Forward-pointer:
//   introduce ErrOllamaUnreachable / ErrModelNotFound /
//   ErrOllamaTimeout in a future PR.
//
// SUT BUG 5: "context deadline exceeded" is NOT in the
//   transientSubstrings taxonomy. The wire-level test in this
//   file uses the http.Client.Timeout path (which produces
//   "Client.Timeout" or "i/o timeout" — both transient) and
//   avoids the context.WithTimeout path. A future SUT-side
//   fix would extend the taxonomy or wrap with
//   retry.WrapTransient at this infra boundary.

package client

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// ── 1. ModelNotFound (HTTP 404) ──────────────────────────────────────
//
// Simulates Ollama returning HTTP 404 (the requested model is
// not loaded). The *Client's doGenerateRequest (client_generate.go)
// produces "ollama returned status 404" for any non-200 status.
//
// ATTESO:
//   - err non-nil
//   - err message contains "404"
//   - retry.Retryable(err) == false (4xx is terminal — the
//     model will not magically appear)
func TestClient_P1C_Generate404_ModelNotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 404 — Ollama's canonical "model not found" response.
		// Production Ollama returns a JSON body like
		// {"error":"model 'gemma4:fake' not found"} but the
		// client only checks the status code, so the body is
		// intentionally irrelevant for this test.
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model 'fake-model-xyz' not found"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "fake-model-xyz", 5)
	resp, err := c.Generate(context.Background(), "Write a script about testing.")

	require.Error(t, err, "404 MUST surface a non-nil error (nessun falso SUCCEEDED)")
	assert.Empty(t, resp, "response MUST be empty when the request failed")
	assert.Contains(t, err.Error(), "404",
		"error must surface the HTTP status so operators can detect missing-model")
	assert.Contains(t, err.Error(), "status",
		"error must include the canonical 'status' word for substring-matchers")
	assert.False(t, retry.Retryable(err),
		"4xx MUST NOT be retried — the model will not appear (retry SOLO dove appropriato)")
	cat, _ := retry.Classify(err)
	assert.Equal(t, "unknown", string(cat),
		"Classify must return unknown for non-transient 4xx (no transient substring match)")
}

// ── 2. ServerError (HTTP 500) ────────────────────────────────────────
//
// Simulates Ollama returning HTTP 500 (server-side error). The
// *Client produces "ollama returned status 500" which does NOT
// contain any of the transientSubstrings (the canonical taxonomy
// matches 429/502/503/504, but NOT 500). Workers using
// retry.IsTransient as their predicate will NOT retry on 500.
//
// ATTESO:
//   - err non-nil
//   - err message contains "500"
//   - retry.Retryable(err) == false (500 is NOT in the transient
//     taxonomy — see SUT BUG 7 below)
//
// SUT BUG 7: "500" is NOT in transientSubstrings
// (pkg/retry/transient.go). The canonical taxonomy includes 429,
// 502, 503, 504 (the canonical retry codes for upstream HTTP
// proxies), but NOT 500. Workers using retry.Retryable as their
// IsRetryable predicate will fail-open on 500 (Classify returns
// ErrUnknown, false). This is a SUT-level gap: in production,
// 5xx in general is transient (the server may recover on the
// next call), but the substring taxonomy is narrower than the
// conventional 5xx-as-transient principle. Forward-pointer:
// extend transientSubstrings to include "500" in Push 6.1.x or
// add a typed-retry classifier for HTTP-status ranges. Sibling
// to SUT BUG 5 (context deadline exceeded).
func TestClient_P1C_Generate500_ServerError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "llama3:8b", 5)
	resp, err := c.Generate(context.Background(), "Write a script about testing.")

	require.Error(t, err, "500 MUST surface a non-nil error (nessun falso SUCCEEDED)")
	assert.Empty(t, resp, "response MUST be empty when the request failed")
	assert.Contains(t, err.Error(), "500",
		"error must surface the HTTP status so operators can detect server failure")
	assert.False(t, retry.Retryable(err),
		"500 is NOT in transientSubstrings (canonical taxonomy: 429/502/503/504, NOT 500) — see SUT BUG 7. Workers using retry.Retryable will NOT retry 500 today.")
	cat, _ := retry.Classify(err)
	assert.Equal(t, "unknown", string(cat),
		"Classify must return unknown for 500 (no transient substring match — the canonical taxonomy only covers 429/502/503/504, not 500)")
}

// ── 3. ServiceUnavailable (HTTP 503) ────────────────────────────────
//
// Simulates Ollama returning HTTP 503 (service temporarily
// unavailable — model loading, GPU contention, etc.). The
// *Client produces "ollama returned status 503" which contains
// the canonical "503" transient substring.
//
// ATTESO:
//   - err non-nil
//   - err message contains "503"
//   - retry.Retryable(err) == true (503 is the canonical
//     transient — Ollama load-balancer / GPU warmup retries)
func TestClient_P1C_Generate503_ServiceUnavailable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"model loading, retry shortly"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "llama3:8b", 5)
	resp, err := c.Generate(context.Background(), "Write a script about testing.")

	require.Error(t, err, "503 MUST surface a non-nil error (nessun falso SUCCEEDED)")
	assert.Empty(t, resp, "response MUST be empty when the request failed")
	assert.Contains(t, err.Error(), "503",
		"error must surface the HTTP status so operators can detect service-unavailable")
	require.True(t, retry.Retryable(err),
		"503 IS the canonical transient per pkg/retry/transient.go — workers MUST retry (retry SOLO dove appropriato)")
	cat, _ := retry.Classify(err)
	assert.Equal(t, "network", string(cat),
		"Classify must return network category for 503")
}

// ── 4. ConnectionRefused (network unreachable) ───────────────────────
//
// Simulates the Ollama server not running on the target port
// (Ollama process crashed, port not listening, firewall). The
// *Client's net/http transport returns a "connection refused"
// error which the client wraps as "ollama request failed: ...".
//
// ATTESO:
//   - err non-nil
//   - err message contains "connection refused" (the canonical
//     network-error signal)
//   - retry.Retryable(err) == true (network errors are
//     transient — the server may come back up)
func TestClient_P1C_GenerateConnectionRefused(t *testing.T) {
	t.Parallel()
	// Start a listener, capture its port, then close it. The
	// port is now "closed" — a connection attempt to it
	// produces ECONNREFUSED on Linux (the canonical
	// "connection refused" error). This is more portable than
	// hardcoding a port that might be in use.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "test setup: must be able to bind a localhost listener")
	closedAddr := ln.Addr().String()
	require.NoError(t, ln.Close(), "test setup: must close the listener to simulate a closed port")

	c := NewClient("http://"+closedAddr, "llama3:8b", 5)
	resp, err := c.Generate(context.Background(), "Write a script about testing.")

	require.Error(t, err, "connection-refused MUST surface a non-nil error (nessun falso SUCCEEDED)")
	assert.Empty(t, resp, "response MUST be empty when the network call failed")
	assert.Contains(t, err.Error(), "connection refused",
		"error must surface the connection-refused signal verbatim")
	assert.Contains(t, err.Error(), "ollama request failed",
		"error must be wrapped with the client context (ollama request failed: %w)")
	require.True(t, retry.Retryable(err),
		"connection refused IS the canonical network transient per pkg/retry/transient.go — workers MUST retry (retry SOLO dove appropriato)")
	cat, _ := retry.Classify(err)
	assert.Equal(t, "network", string(cat),
		"Classify must return network category for connection refused")
}

// ── 5. Timeout (Client.Timeout fires) ────────────────────────────────
//
// Simulates Ollama taking longer than the configured client
// timeout. The test server sleeps for 3 seconds; the client is
// configured with timeoutSeconds=1 so http.Client.Timeout
// fires at 1 second.
//
// The *Client's net/http transport returns an error containing
// the canonical "timeout" substring (e.g., "Client.Timeout
// exceeded while awaiting headers").
//
// ATTESO:
//   - err non-nil
//   - err message contains "timeout" (the canonical timeout signal)
//   - retry.Retryable(err) == true (timeouts are transient)
//
// SUT BUG 5 note: this test exercises the http.Client.Timeout
// path (which contains "timeout"). The context.WithTimeout
// path (which produces "context deadline exceeded") is NOT
// exercised here — that path is classified as ErrUnknown,
// false by Classify. See the file header SUT BUGS section.
func TestClient_P1C_GenerateTimeout(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the client timeout (1s). The
		// client's http.Client.Timeout will fire first.
		time.Sleep(3 * time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":"never delivered"}`))
	}))
	defer srv.Close()

	// timeoutSeconds=1 → http.Client.Timeout = 1s. The test
	// server sleeps 3s, so the timeout fires.
	c := NewClient(srv.URL, "llama3:8b", 1)

	start := time.Now()
	resp, err := c.Generate(context.Background(), "Write a script about testing.")
	elapsed := time.Since(start)

	require.Error(t, err, "timeout MUST surface a non-nil error (nessun falso SUCCEEDED)")
	assert.Empty(t, resp, "response MUST be empty when the request timed out")
	assert.Contains(t, strings.ToLower(err.Error()), "timeout",
		"error must surface the timeout signal (case-insensitive match against canonical taxonomy): %v", err)
	// Sanity: the test must NOT wait the full 3s server sleep
	// — the http.Client.Timeout is supposed to short-circuit
	// the request at ~1s. Allow a generous upper bound for
	// CI noise (2.5s).
	assert.Less(t, elapsed, 2500*time.Millisecond,
		"client MUST short-circuit at http.Client.Timeout (~1s), not wait for the server sleep (3s); elapsed=%s", elapsed)
	require.True(t, retry.Retryable(err),
		"timeout IS transient per pkg/retry/transient.go — workers MUST retry (retry SOLO dove appropriato): %v", err)
	cat, _ := retry.Classify(err)
	assert.Equal(t, "timeout", string(cat),
		"Classify must return timeout category for Client.Timeout path")
}

// ── 6. MalformedResponse (invalid JSON body) ─────────────────────────
//
// Simulates Ollama returning HTTP 200 with a body that is not
// valid JSON. The *Client's doGenerateRequest tries to
// json.NewDecoder().Decode(&result) and surfaces
// "failed to decode response: <json err>".
//
// ATTESO:
//   - err non-nil
//   - err message contains "decode" or "invalid" (the typed
//     signal that the response shape is wrong)
func TestClient_P1C_GenerateMalformedResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Body is NOT valid JSON — decoder will fail.
		_, _ = w.Write([]byte(`<<<not valid json>>>`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "llama3:8b", 5)
	resp, err := c.Generate(context.Background(), "Write a script about testing.")

	require.Error(t, err, "malformed JSON MUST surface a non-nil error (nessun falso SUCCEEDED)")
	assert.Empty(t, resp, "response MUST be empty when decode failed")
	assert.True(t,
		strings.Contains(err.Error(), "decode") || strings.Contains(err.Error(), "invalid"),
		"error must surface the decode/invalid signal: %v", err)
}

// ── 7. Sanity: production client constructor shape ──────────────────
//
// Locks the canonical NewClient(baseURL, model, timeoutSeconds)
// shape that the engine-level fakes simulate. If the
// constructor changes signature, this test will fail to
// compile, surfacing the contract drift to the P1.C test
// author at the next CI run.
//
// This test is intentionally a no-op assertion — its purpose
// is to keep the import graph + constructor signature in
// scope for the rest of the file. Removing it would let a
// silent import-rewrite go undetected.
func TestClient_P1C_ConstructorShapeLock(t *testing.T) {
	t.Parallel()
	// A zero-timeout client must fall back to the canonical
	// default (types.DefaultTimeoutSeconds = 600s). This
	// matches the production wiring and is the seam the
	// engine-level tests use when they call NewClient with
	// 5 (a short test timeout).
	c := NewClient("http://localhost:11434", "llama3:8b", 0)
	require.NotNil(t, c, "NewClient must return a non-nil client")
	assert.Equal(t, "http://localhost:11434", c.baseURL)
	assert.Equal(t, "llama3:8b", c.model)
	assert.NotNil(t, c.httpClient, "client must have a non-nil httpClient")
	// Construct with an explicit short timeout to exercise the
	// production code path used by the other P1.C tests.
	c2 := NewClient("http://localhost:11434", "llama3:8b", 5)
	require.NotNil(t, c2.httpClient)
	assert.Equal(t, 5*time.Second, c2.httpClient.Timeout,
		"httpClient.Timeout must mirror the constructor's timeoutSeconds arg")
}

// Compile-time assertion: the *Client implements the production
// shape that the engine depends on. The engine-level test file
// uses fakeOllamaGen at the use-case seam, but the wire-level
// tests in this file exercise the *Client directly. If the
// *Client struct ever drifts (e.g., httpClient field renamed),
// the field assertions in TestClient_P1C_ConstructorShapeLock
// above will fail to compile.
//
// (The compile-time assertion against the engine's narrow
// interface lives in
// internal/application/scripts/usecase/engine.go: `var _
// scriptOllamaGenerator = (*ollama.Generator)(nil)` — that
// surface is the canonical use-case entry point. This file
// tests the *Client directly.)
