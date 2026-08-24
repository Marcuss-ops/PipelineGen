// Package enrichment — ollama_client_test.go (PR-011B, July 2026).
//
// 8 hermetic TDD tests pinning the ollamaEnrichmentLLMClient contract.
// The tests use ONLY httptest.NewServer (no real ollama, no real network)
// so they pass in any environment that can compile the package.
//
// Test taxonomy:
//  1. TestOllamaEnrichment_NewOllamaEnrichmentLLMClient_NilClient —
//     composition-time fail-closed on nil ollama client.
//  2. TestOllamaEnrichment_HappyPath_ParsesAll6Fields —
//     happy-path: ollama returns valid JSON with all 6 fields;
//     adapter parses and returns EnrichmentResponse with non-empty
//     fields + the configured model name.
//  3. TestOllamaEnrichment_InvalidJSON_ReturnsTypedSentinel —
//     terminal sentinel on malformed JSON response.
//  4. TestOllamaEnrichment_EmptyCategory_ReturnsTypedSentinel —
//     terminal sentinel on valid JSON with empty Category
//     (the canonical 6-field "required" marker).
//  5. TestOllamaEnrichment_NetworkError_ReturnsRetryableSentinel —
//     retryable sentinel when ollama server is unreachable.
//  6. TestOllamaEnrichment_ModelOverride_PassedInOptions —
//     per-capability modelName is threaded into options["model"]
//     on the wire (canonical: cfg.External.ParseArenaLLM wins).
//  7. TestOllamaEnrichment_ModelFallback_UsesClientDefault —
//     empty modelName falls back to client.Model() at call time
//     (canonical: empty ParseArenaLLM → cfg.External.OllamaModel).
//  8. TestOllamaEnrichment_EmptyChunkID_ReturnsTypedSentinel —
//     composition-time fail-closed on empty chunk_id.
//
// godlike/06 SSOT (one canonical owner per fact): the 8 tests
// live ONLY in this file. Future contract additions MUST extend
// this file (NOT introduce a parallel test surface).
//
// godlike/07 minimum-blast-radius: zero external dependencies
// (no real ollama, no real GPU, no real network). The test
// surface is hermetic and idempotent — `go test -short -count=1`
// passes deterministically on any Go toolchain.
package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ollamaclient "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"
)

// cannedOllamaResponse is the canonical /api/chat response shape
// per the ollama wire-format. The adapter parses
// result.Message.Content as the LLM response text.
type cannedOllamaResponse struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

// newOllamaTestServer returns an httptest.Server that emulates the
// ollama /api/chat endpoint. The handler:
//   - records the request body in lastRequestBody (for assertions)
//   - returns the canned response in nextResponse (atomic swap; tests
//     can mutate the response between calls)
//   - returns an HTTP error when returnStatus != 0
func newOllamaTestServer(t *testing.T, canned string, returnStatus int) (*httptest.Server, *[]byte, *int32) {
	t.Helper()
	var lastBody []byte
	var callCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callCount, 1)
		// Record the request body for assertions.
		body := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			if _, err := r.Body.Read(body); err != nil && err.Error() != "EOF" {
				t.Logf("test server: read body: %v", err)
			}
		}
		lastBody = body
		if returnStatus != 0 {
			http.Error(w, "test server: forced error", returnStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(canned))
	}))
	t.Cleanup(srv.Close)
	return srv, &lastBody, &callCount
}

// newOllamaClient is a small constructor helper that returns a
// *client.Client pointed at the test server URL. Mirrors the
// production wiring in app/build_bundles_ai.go::buildAIBundle.
func newOllamaClient(t *testing.T, serverURL, model string) *ollamaclient.Client {
	t.Helper()
	return ollamaclient.NewClient(serverURL, model, 5)
}

// Test 1: composition-time fail-closed on nil ollama client.
func TestOllamaEnrichment_NewOllamaEnrichmentLLMClient_NilClient(t *testing.T) {
	a, err := NewOllamaEnrichmentLLMClient(nil, "gemma4:e4b", "")
	if a != nil {
		t.Errorf("expected nil adapter, got %+v", a)
	}
	if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
		t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
	}
}

// Test 2: happy-path — ollama returns valid JSON with all 6 fields;
// adapter parses and returns EnrichmentResponse with non-empty
// fields + the configured model name.
func TestOllamaEnrichment_HappyPath_ParsesAll6Fields(t *testing.T) {
	canned := `{"message":{"role":"assistant","content":"{\"category\":\"Boxe\",\"event\":\"Pacquiao vs Broner\",\"round\":\"9\",\"scene\":\"Combination in the corner\",\"subject\":\"Manny Pacquiao\",\"entities\":[\"Manny Pacquiao\",\"Adrien Broner\"]}"},"done":true}`
	srv, _, callCount := newOllamaTestServer(t, canned, 0)

	cli := newOllamaClient(t, srv.URL, "gemma4:e4b")
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "gemma4:e4b", "")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	resp, err := adapter.Enrich(context.Background(), EnrichmentRequest{
		ChunkID:        "stock:abc:chunk:0",
		Title:          "Pacquiao Broner Round 9",
		SourceProvider: "pexels",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if atomic.LoadInt32(callCount) != 1 {
		t.Errorf("expected 1 ollama call, got %d", atomic.LoadInt32(callCount))
	}
	if resp.ChunkID != "stock:abc:chunk:0" {
		t.Errorf("ChunkID mismatch: got %q", resp.ChunkID)
	}
	if resp.Fields.Category != "Boxe" {
		t.Errorf("Category mismatch: got %q", resp.Fields.Category)
	}
	if resp.Fields.Event != "Pacquiao vs Broner" {
		t.Errorf("Event mismatch: got %q", resp.Fields.Event)
	}
	if resp.Fields.Round != "9" {
		t.Errorf("Round mismatch: got %q", resp.Fields.Round)
	}
	if resp.Fields.Scene != "Combination in the corner" {
		t.Errorf("Scene mismatch: got %q", resp.Fields.Scene)
	}
	if resp.Fields.Subject != "Manny Pacquiao" {
		t.Errorf("Subject mismatch: got %q", resp.Fields.Subject)
	}
	if len(resp.Fields.Entities) != 2 {
		t.Errorf("Entities mismatch: got %v", resp.Fields.Entities)
	}
	if resp.Model != "gemma4:e4b" {
		t.Errorf("Model mismatch: got %q", resp.Model)
	}
	if resp.Elapsed < 0 {
		t.Errorf("Elapsed must be non-negative, got %v", resp.Elapsed)
	}
	if adapter.Model() != "gemma4:e4b" {
		t.Errorf("adapter.Model() mismatch: got %q", adapter.Model())
	}
}

// Test 3: parse failure (malformed JSON) returns the canonical
// terminal sentinel. The adapter wraps the JSON parse error with
// WrapInvalidLLMResponse so the worker's exponential backoff can
// flip terminal after 3 retries.
func TestOllamaEnrichment_InvalidJSON_ReturnsTypedSentinel(t *testing.T) {
	canned := `{"message":{"role":"assistant","content":"this is not valid json {{{"},"done":true}`
	srv, _, _ := newOllamaTestServer(t, canned, 0)

	cli := newOllamaClient(t, srv.URL, "gemma4:e4b")
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "gemma4:e4b", "")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	_, err = adapter.Enrich(context.Background(), EnrichmentRequest{
		ChunkID: "stock:abc:chunk:0",
		Title:   "Some title",
	})
	if err == nil {
		t.Fatal("expected error on invalid JSON")
	}
	if !errors.Is(err, ErrEnrichmentInvalidLLMResponse) {
		t.Errorf("expected ErrEnrichmentInvalidLLMResponse, got %v", err)
	}
	// The error message MUST include a preview of the offending
	// content so operators can debug schema drift.
	if !strings.Contains(err.Error(), "content_preview=") {
		t.Errorf("expected content_preview in error message, got %q", err.Error())
	}
}

// Test 4: valid JSON with empty Category returns the canonical
// terminal sentinel. Category is the "required" 6-field marker
// per the system message; a successful ollama call that returns
// empty Category is a schema-drift signal.
func TestOllamaEnrichment_EmptyCategory_ReturnsTypedSentinel(t *testing.T) {
	canned := `{"message":{"role":"assistant","content":"{\"category\":\"\",\"event\":\"x\",\"subject\":\"y\"}"},"done":true}`
	srv, _, _ := newOllamaTestServer(t, canned, 0)

	cli := newOllamaClient(t, srv.URL, "gemma4:e4b")
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "gemma4:e4b", "")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	_, err = adapter.Enrich(context.Background(), EnrichmentRequest{
		ChunkID: "stock:abc:chunk:0",
	})
	if !errors.Is(err, ErrEnrichmentInvalidLLMResponse) {
		t.Errorf("expected ErrEnrichmentInvalidLLMResponse, got %v", err)
	}
	if !strings.Contains(err.Error(), "empty Category") {
		t.Errorf("expected 'empty Category' in error message, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "schema-drift") {
		t.Errorf("expected 'schema-drift' in error message, got %q", err.Error())
	}
}

// Test 5: HTTP 500 from the ollama server is wrapped as
// ErrEnrichmentLLMUnavailable (retryable per godlike/07).
//
// Note: the ollama client has built-in retry with
// types.MaxRetries=5 attempts and 2s backoff; this test
// takes ~4s on loopback (httptest) because every retry
// hits the 500 endpoint. The test is correct; the slowness
// is the cost of exercising the real retry path. Future
// forward-pointer: extract the retry config into a
// constructor option so tests can pass MaxAttempts=1.
func TestOllamaEnrichment_NetworkError_ReturnsRetryableSentinel(t *testing.T) {
	srv, _, _ := newOllamaTestServer(t, "", http.StatusInternalServerError)

	cli := newOllamaClient(t, srv.URL, "gemma4:e4b")
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "gemma4:e4b", "")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	_, err = adapter.Enrich(context.Background(), EnrichmentRequest{
		ChunkID: "stock:abc:chunk:0",
	})
	if !errors.Is(err, ErrEnrichmentLLMUnavailable) {
		t.Errorf("expected ErrEnrichmentLLMUnavailable, got %v", err)
	}
}

// Test 6: per-capability modelName override is threaded into the
// wire's options["model"] field. The composition root uses this
// to pin cfg.External.ParseArenaLLM as the per-capability model.
func TestOllamaEnrichment_ModelOverride_PassedInOptions(t *testing.T) {
	canned := `{"message":{"role":"assistant","content":"{\"category\":\"Boxe\"}"},"done":true}`
	srv, lastBody, _ := newOllamaTestServer(t, canned, 0)

	cli := newOllamaClient(t, srv.URL, "fallback-model")
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "parse-arena-llm", "")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	resp, err := adapter.Enrich(context.Background(), EnrichmentRequest{
		ChunkID: "stock:abc:chunk:0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Model != "parse-arena-llm" {
		t.Errorf("expected model=parse-arena-llm in response, got %q", resp.Model)
	}
	// Verify the wire request body has the model override.
	var got map[string]any
	if err := json.Unmarshal(*lastBody, &got); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if got["model"] != "parse-arena-llm" {
		t.Errorf("expected model=parse-arena-llm in request body, got %v", got["model"])
	}
	// format must be "json" (top-level body field).
	if got["format"] != "json" {
		t.Errorf("expected format=json in request body, got %v", got["format"])
	}
}

// Test 7: empty modelName falls back to the underlying ollama
// client's default model (canonical: empty ParseArenaLLM →
// cfg.External.OllamaModel → client.NewClient(model)).
func TestOllamaEnrichment_ModelFallback_UsesClientDefault(t *testing.T) {
	canned := `{"message":{"role":"assistant","content":"{\"category\":\"Boxe\"}"},"done":true}`
	srv, lastBody, _ := newOllamaTestServer(t, canned, 0)

	// Pass empty modelName to the adapter; the underlying
	// ollama client has model="default-ollama-model".
	cli := newOllamaClient(t, srv.URL, "default-ollama-model")
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "", "")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	resp, err := adapter.Enrich(context.Background(), EnrichmentRequest{
		ChunkID: "stock:abc:chunk:0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Model != "default-ollama-model" {
		t.Errorf("expected model=default-ollama-model (fallback), got %q", resp.Model)
	}
	if adapter.Model() != "default-ollama-model" {
		t.Errorf("adapter.Model() should return client default, got %q", adapter.Model())
	}
	// Verify the wire request body has the fallback model.
	var got map[string]any
	if err := json.Unmarshal(*lastBody, &got); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if got["model"] != "default-ollama-model" {
		t.Errorf("expected model=default-ollama-model in request body, got %v", got["model"])
	}
}

// Test 8: empty chunk_id returns the composition-time sentinel.
func TestOllamaEnrichment_EmptyChunkID_ReturnsTypedSentinel(t *testing.T) {
	canned := `{"message":{"role":"assistant","content":"{\"category\":\"Boxe\"}"},"done":true}`
	srv, _, _ := newOllamaTestServer(t, canned, 0)

	cli := newOllamaClient(t, srv.URL, "gemma4:e4b")
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "gemma4:e4b", "")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	_, err = adapter.Enrich(context.Background(), EnrichmentRequest{
		ChunkID: "", // missing
	})
	if !errors.Is(err, ErrEnrichmentHandlerNotConfigured) {
		t.Errorf("expected ErrEnrichmentHandlerNotConfigured, got %v", err)
	}
}

// Test 9 (bonus): the adapter's Enrich() respects ctx cancellation
// — when the context is cancelled mid-call, the HTTP request is
// aborted and the error surfaces as ErrEnrichmentLLMUnavailable
// (retryable per godlike/07; the ollama server may come back
// within the worker's exponential backoff window).
func TestOllamaEnrichment_ContextCancelled_ReturnsRetryableSentinel(t *testing.T) {
	// Server that blocks on a channel until the test releases it.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"category\":\"x\"}"},"done":true}`))
	}))
	defer srv.Close()
	defer close(release)

	cli := newOllamaClient(t, srv.URL, "gemma4:e4b")
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "gemma4:e4b", "")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = adapter.Enrich(ctx, EnrichmentRequest{
		ChunkID: "stock:abc:chunk:0",
	})
	if err == nil {
		t.Fatal("expected error on ctx cancellation")
	}
	// godlike/07 NO-FAKE-AVAILABILITY: a ctx-cancelled HTTP
	// call is the canonical "transient infrastructure" class
	// and surfaces as ErrEnrichmentLLMUnavailable (retryable
	// per worker exponential backoff). WrapLLMUnavailable uses
	// a single-%w wrap (sentinel only), so the underlying
	// context.DeadlineExceeded is rendered as text — not
	// reachable via errors.Is chain.
	if !errors.Is(err, ErrEnrichmentLLMUnavailable) {
		t.Errorf("expected ErrEnrichmentLLMUnavailable, got %v", err)
	}
}

// Test 10 (i18n/prompt-iteration seam, PR-011B follow-up): when
// the adapter is constructed with promptVersion="v2", the wire
// request body's `messages[0].content` MUST contain the canonical
// V2 Italian prompt (detected by the substring "Sei un assistente"
// + "categoria" which are unique to V2 and absent from V1). This
// is the canonical SSOT contract for the i18n seam: the
// composition root can swap cfg.External.EnrichmentPromptVersion
// to switch the LLM's vocabulary without touching the call site.
func TestOllamaEnrichment_V2PromptSelected_WhenLocaleConfigured(t *testing.T) {
	canned := `{"message":{"role":"assistant","content":"{\"category\":\"Boxe\"}"},"done":true}`
	srv, lastBody, _ := newOllamaTestServer(t, canned, 0)

	cli := newOllamaClient(t, srv.URL, "gemma4:e4b")
	// Construct the adapter with promptVersion="v2" — this is
	// the canonical "use Italian" signal from the composition
	// root (mirrors cfg.External.EnrichmentPromptVersion="v2").
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "gemma4:e4b", "v2")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	_, err = adapter.Enrich(context.Background(), EnrichmentRequest{
		ChunkID: "stock:abc:chunk:0",
		Title:   "Pacquiao Broner",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse the wire request body. The ollama /api/chat
	// contract is: {"model": "...", "messages": [{"role":
	// "system", "content": "<prompt>"}, {"role": "user",
	// "content": "<user_prompt>"}], "format": "json", ...}.
	var got struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(*lastBody, &got); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if len(got.Messages) < 1 {
		t.Fatal("expected at least 1 message (system) in request body")
	}
	systemContent := got.Messages[0].Content

	// V2 contract: the system message contains the canonical
	// Italian vocabulary. The substring "Sei un assistente" is
	// unique to V2 (V1 uses "You are a video metadata"). The
	// substring "categoria" is the Italian word for "category"
	// and also unique to V2. Asserting both locks the contract
	// against future regressions where V2 is silently swapped
	// back to V1 (e.g., a typo in selectSystemPrompt's case
	// statement).
	if !strings.Contains(systemContent, "Sei un assistente") {
		t.Errorf("V2 prompt NOT selected: expected 'Sei un assistente' in system content, got: %q", systemContent)
	}
	if !strings.Contains(systemContent, "categoria") {
		t.Errorf("V2 prompt NOT selected: expected 'categoria' in system content, got: %q", systemContent)
	}
	// Defensive: V1 vocabulary MUST NOT appear (catches a
	// future regression where V2 const is accidentally
	// overwritten with V1 text — godlike/07 NO-FAKE-AVAILABILITY).
	if strings.Contains(systemContent, "You are a video metadata") {
		t.Errorf("V1 vocabulary leaked into V2 prompt: %q", systemContent)
	}
}

// Test 11 (godlike/07 fail-closed contract, PR-011B follow-up):
// when the adapter is constructed with an UNKNOWN promptVersion
// (e.g., a typo in cfg.External.EnrichmentPromptVersion), the
// adapter MUST fall back to V1 (godlike/07 fail-closed at the
// language-default level) rather than silently breaking the
// enrichment pass. The wire body must contain V1 vocabulary
// ("You are a video metadata") and MUST NOT contain V2 vocabulary.
func TestOllamaEnrichment_UnknownVersion_FallsBackToV1(t *testing.T) {
	canned := `{"message":{"role":"assistant","content":"{\"category\":\"Boxe\"}"},"done":true}`
	srv, lastBody, _ := newOllamaTestServer(t, canned, 0)

	cli := newOllamaClient(t, srv.URL, "gemma4:e4b")
	// Construct with a typo'd version string — mirrors an
	// operator who mis-configured cfg.External.EnrichmentPromptVersion.
	adapter, err := NewOllamaEnrichmentLLMClient(cli, "gemma4:e4b", "v999-typo")
	if err != nil {
		t.Fatalf("NewOllamaEnrichmentLLMClient: %v", err)
	}

	_, err = adapter.Enrich(context.Background(), EnrichmentRequest{
		ChunkID: "stock:abc:chunk:0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(*lastBody, &got); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if len(got.Messages) < 1 {
		t.Fatal("expected at least 1 message (system) in request body")
	}
	systemContent := got.Messages[0].Content

	// Unknown version MUST fall back to V1 (canonical English).
	if !strings.Contains(systemContent, "You are a video metadata") {
		t.Errorf("unknown version fallback FAILED: expected V1 vocabulary in system content, got: %q", systemContent)
	}
	// And MUST NOT contain V2 vocabulary (catches a regression
	// where the default case accidentally routes to V2).
	if strings.Contains(systemContent, "Sei un assistente") {
		t.Errorf("unknown version fallback leaked V2: %q", systemContent)
	}
}
