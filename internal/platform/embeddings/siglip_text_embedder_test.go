// Package embeddings — TDD contract tests for the SigLIPTextEmbedder
// (PR-CROSS-MODAL-TEXT-TO-VISUAL, August 2026).
//
// Pins the canonical cross-modal contract:
//   - Text queries encoded through SigLIP-text land in the SAME 768d
//     vector space as image-encoded video frames indexed under the
//     `visual` channel of Qdrant v3 (DefaultV3Schema).
//   - Dimension assertions are fail-closed: non-768d responses surface
//     ErrSigLIPDimensionMismatch so a misconfigured sidecar cannot
//     silently corrupt the index with non-matching vectors.
//   - Model identity cross-validates against the canonical
//     "siglip-so400m-patch14-384" from IndexSchema per QDRANT-001
//     (vendor-prefix variants handled via siglipModelNameMatches).
//
// These tests use httptest.NewServer for the sidecar mock — no real
// Python sidecar needed (godlike/06 testability contract).
package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

// helper: build a 768d canonical fake vector (deterministic so byte-level
// shape is predictable).
func makeSiglipFakeVec() []float64 {
	out := make([]float64, SigLIPTextDimension)
	for i := range out {
		out[i] = float64(i) / float64(SigLIPTextDimension)
	}
	return out
}

// TestSigLIPTextDimensionConstant pins the canonical 768d dim against
// the Qdrant v3 IndexSchema visual channel (DefaultV3Schema). Changing
// this constant without coordinating the IndexSchema is a
// cross-modal-breaking event — the test pins the canonical value.
func TestSigLIPTextDimensionConstant(t *testing.T) {
	if SigLIPTextDimension != 768 {
		t.Errorf("SigLIPTextDimension: want 768d (canonical Qdrant visual channel), got %d", SigLIPTextDimension)
	}
}

// TestSigLIPTextEndpointConstant pins the canonical sidecar endpoint
// name. The Python embedding server (scripts/services/embedding_server/)
// MUST expose a handler at this exact path.
func TestSigLIPTextEndpointConstant(t *testing.T) {
	if SigLIPTextEndpoint != "/embed_visual_from_text" {
		t.Errorf("SigLIPTextEndpoint: want /embed_visual_from_text, got %q", SigLIPTextEndpoint)
	}
}

// TestSentinelsAreDistinct pins the godlike/07 typed-error contract:
// 4 distinct sentinels, errors.Is probe MUST NOT match a sibling.
func TestSentinelsAreDistinct(t *testing.T) {
	if errors.Is(ErrSigLIPSidecarUnavailable, ErrSigLIPDimensionMismatch) {
		t.Error("ErrSigLIPSidecarUnavailable MUST NOT be errors.Is-equivalent to ErrSigLIPDimensionMismatch")
	}
	if errors.Is(ErrSigLIPDimensionMismatch, ErrSigLIPModelIdentityMismatch) {
		t.Error("ErrSigLIPDimensionMismatch MUST NOT be errors.Is-equivalent to ErrSigLIPModelIdentityMismatch")
	}
	if errors.Is(ErrSigLIPModelIdentityMismatch, ErrSigLIPEmptyResponse) {
		t.Error("ErrSigLIPModelIdentityMismatch MUST NOT be errors.Is-equivalent to ErrSigLIPEmptyResponse")
	}
	if errors.Is(ErrSigLIPEmptyResponse, ErrSigLIPSidecarUnavailable) {
		t.Error("ErrSigLIPEmptyResponse MUST NOT be errors.Is-equivalent to ErrSigLIPSidecarUnavailable")
	}
}

// TestSentinelMessageKeywords pin the message surfacing for log-scrapers
// to dispatch on the specific failure mode without unwrapping.
func TestSentinelMessageKeywords(t *testing.T) {
	cases := []struct {
		sentinel error
		keyword  string
	}{
		{ErrSigLIPSidecarUnavailable, "siglip text sidecar unavailable"},
		{ErrSigLIPDimensionMismatch, "non-768d vector"},
		{ErrSigLIPModelIdentityMismatch, "model identity mismatch"},
		{ErrSigLIPEmptyResponse, "empty vector"},
	}
	for _, c := range cases {
		if !strings.Contains(c.sentinel.Error(), c.keyword) {
			t.Errorf("sentinel %v: message %q does not contain %q",
				c.sentinel, c.sentinel.Error(), c.keyword)
		}
	}
}

// TestSigLIPTextEmbedder_EmptyTextShortCircuit: empty text input
// short-circuits to (nil, nil) per canonical contract — the
// orchestrator MUST prevent this call, but the adapter is
// nil-tolerant for the rare case where a caller slips through.
func TestSigLIPTextEmbedder_EmptyTextShortCircuit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Errorf("empty text should short-circuit BEFORE reaching sidecar")
	}))
	defer server.Close()

	enc := NewSigLIPTextEmbedder(server.URL, "siglip-so400m-patch14-384")
	got, err := enc.EmbedTextQuery(context.Background(), "")
	if err != nil {
		t.Errorf("empty text: want nil error, got %v", err)
	}
	if got != nil {
		t.Errorf("empty text: want nil vec, got %v", got)
	}
}

// TestSigLIPTextEmbedder_ServerURLEmptyFailClosed: when composition
// root passes serverURL="" (composition deferred), EmbedTextQuery
// returns ErrSigLIPSidecarUnavailable — fail-closed, no panic.
func TestSigLIPTextEmbedder_ServerURLEmptyFailClosed(t *testing.T) {
	enc := NewSigLIPTextEmbedder("", "")
	_, err := enc.EmbedTextQuery(context.Background(), "any text")
	if err == nil {
		t.Fatal("empty serverURL: want error, got nil")
	}
	if !errors.Is(err, ErrSigLIPSidecarUnavailable) {
		t.Errorf("empty serverURL: want errors.Is(err, ErrSigLIPSidecarUnavailable)=true, got %v", err)
	}
}

// TestSigLIPTextEmbedder_HappyPathRoundtrip: a 768d response from
// a canonical sidecar round-trips to a 768d []float32 output.
// The JSON body shape pins QDRANT-001 envelope contract.
func TestSigLIPTextEmbedder_HappyPathRoundtrip(t *testing.T) {
	fakeVec := makeSiglipFakeVec()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != SigLIPTextEndpoint {
			t.Errorf("sidecar endpoint: want %s, got %s", SigLIPTextEndpoint, r.URL.Path)
		}
		// Validate canonical request envelope.
		var req struct {
			Text  string `json:"text"`
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("sidecar request decode: %v", err)
			http.Error(w, "decode", http.StatusBadRequest)
			return
		}
		if req.Text == "" {
			t.Errorf("sidecar request: text must not be empty")
		}
		if req.Model != "google/siglip-so400m-patch14-384" {
			t.Errorf("sidecar request: model=%q, want google/siglip-so400m-patch14-384", req.Model)
		}
		// Canonical QDRANT-001 envelope response.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding":     fakeVec,
			"dimensions":    SigLIPTextDimension,
			"model":         "google/siglip-so400m-patch14-384", // vendor-prefix form to test modelNameMatches
			"model_version": "2026-06-16-v1",
			"error":         "",
		})
	}))
	defer server.Close()

	enc := NewSigLIPTextEmbedder(server.URL, "siglip-so400m-patch14-384")
	got, err := enc.EmbedTextQuery(context.Background(), "a mountain range at sunset")
	if err != nil {
		t.Fatalf("happy-path: unexpected error %v", err)
	}
	if len(got) != SigLIPTextDimension {
		t.Fatalf("happy-path: want 768d vec, got %dd", len(got))
	}
	// Spot-check the vec identity (first/last) so dimension-only
	// tests can't pass through truncation.
	if got[0] != float32(0.0/SigLIPTextDimension) {
		t.Errorf("happy-path: got[0]=%v, want 0.0", got[0])
	}
	if got[SigLIPTextDimension-1] != float32(float64(SigLIPTextDimension-1)/float64(SigLIPTextDimension)) {
		t.Errorf("happy-path: got[767]=%v, want ~0.999", got[SigLIPTextDimension-1])
	}
}

// TestSigLIPTextEmbedder_DimensionMismatchFailClosed: a 512d
// response (e.g. sidecar misconfigured) trips
// ErrSigLIPDimensionMismatch — fail-closed per godlike/07 so a
// misconfigured sidecar cannot corrupt the Qdrant index with
// non-matching 512d vectors.
func TestSigLIPTextEmbedder_DimensionMismatchFailClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fakeVec := make([]float64, 512) // NOT 768 — the canonical dimension
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding":     fakeVec,
			"dimensions":    512,
			"model":         "siglip-so400m-patch14-384",
			"model_version": "2026-06-16-v1",
		})
	}))
	defer server.Close()

	enc := NewSigLIPTextEmbedder(server.URL, "siglip-so400m-patch14-384")
	_, err := enc.EmbedTextQuery(context.Background(), "any text")
	if err == nil {
		t.Fatal("512d sidecar response: want error, got nil")
	}
	if !errors.Is(err, ErrSigLIPDimensionMismatch) {
		t.Errorf("512d sidecar response: want errors.Is(err, ErrSigLIPDimensionMismatch)=true, got %v", err)
	}
}

// TestSigLIPTextEmbedder_HTTP501ChannelUnavailable: HTTP 501 from the
// sidecar means the SigLIP model failed to load. The adapter
// surfaces ErrSigLIPSidecarUnavailable — distinct from the dimension-
// mismatch case so operator dashboards can tell "channel not wired"
// from "channel wired but returning wrong dimensions".
func TestSigLIPTextEmbedder_HTTP501ChannelUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte("SigLIP model not loaded"))
	}))
	defer server.Close()

	enc := NewSigLIPTextEmbedder(server.URL, "siglip-so400m-patch14-384")
	_, err := enc.EmbedTextQuery(context.Background(), "any text")
	if err == nil {
		t.Fatal("HTTP 501: want error, got nil")
	}
	if !errors.Is(err, ErrSigLIPSidecarUnavailable) {
		t.Errorf("HTTP 501: want errors.Is(err, ErrSigLIPSidecarUnavailable)=true, got %v", err)
	}
}

// TestSigLIPTextEmbedder_ModelIdentityMismatchFailClosed: a sidecar
// that reports a NON-canonical model identity surfaces
// ErrSigLIPModelIdentityMismatch per QDRANT-001 prod-grade guards.
// The mismatch is unrecoverable (you cannot merge a Clip-ViT-B/32
// 512d vector with a SigLIP 768d vector).
func TestSigLIPTextEmbedder_ModelIdentityMismatchFailClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fakeVec := makeSiglipFakeVec()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding":     fakeVec,
			"dimensions":    SigLIPTextDimension,
			"model":         "openai/clip-vit-base-patch32", // WRONG; should be SigLIP
			"model_version": "2026-06-16-v1",
		})
	}))
	defer server.Close()

	enc := NewSigLIPTextEmbedder(server.URL, "siglip-so400m-patch14-384")
	_, err := enc.EmbedTextQuery(context.Background(), "any text")
	if err == nil {
		t.Fatal("model mismatch: want error, got nil")
	}
	if !errors.Is(err, ErrSigLIPModelIdentityMismatch) {
		t.Errorf("model mismatch: want errors.Is(err, ErrSigLIPModelIdentityMismatch)=true, got %v", err)
	}
}

// TestSigLIPTextEmbedder_EmptyVecFailClosed: a 200 OK with empty
// embedding array trips ErrSigLIPEmptyResponse — distinct so
// operators can tell "sidecar sent nothing" from "wrong dimensions".
func TestSigLIPTextEmbedder_EmptyVecFailClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding":     []float64{},
			"dimensions":    0,
			"model":         "siglip-so400m-patch14-384",
			"model_version": "2026-06-16-v1",
		})
	}))
	defer server.Close()

	enc := NewSigLIPTextEmbedder(server.URL, "siglip-so400m-patch14-384")
	_, err := enc.EmbedTextQuery(context.Background(), "any text")
	if err == nil {
		t.Fatal("empty vec: want error, got nil")
	}
	if !errors.Is(err, ErrSigLIPEmptyResponse) {
		t.Errorf("empty vec: want errors.Is(err, ErrSigLIPEmptyResponse)=true, got %v", err)
	}
}

// TestSigLIPTextEmbedder_HTTP500WrapFailClosed: a 500 Internal Server
// Error from the sidecar surfaces a wrapped error (NOT a typed sentinel).
// Operators get the HTTP status detail in the message text.
func TestSigLIPTextEmbedder_HTTP500WrapFailClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("inference service crashed"))
	}))
	defer server.Close()

	enc := NewSigLIPTextEmbedder(server.URL, "siglip-so400m-patch14-384")
	_, err := enc.EmbedTextQuery(context.Background(), "any text")
	if err == nil {
		t.Fatal("HTTP 500: want error, got nil")
	}
	// HTTP 500 is NOT one of the 4 typed sentinels — it surfaces as a
	// wrapped net/HTTP error so operators see the upstream failure mode.
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("HTTP 500: want error message contains '500', got %v", err.Error())
	}
}

// TestSigLIPTextEmbedder_CompileTimeAssertion: the
// `var _ search.ChannelEncoder = (*SigLIPTextEmbedder)(nil)` line at
// the struct declaration site is the canonical Pattern 0 build-failure
// pin. Drift in either signature is a build failure, NOT a runtime
// panic. This test is a sentinel — if you delete the assertion, the
// test file fails to compile. The runtime call here is a no-op; the
// compile-time guarantee comes from the `var _` line in the
// production file.
func TestSigLIPTextEmbedder_CompileTimeAssertion(t *testing.T) {
	enc := NewSigLIPTextEmbedder("", "")
	_ = enc
	// Ensure the ChannelEncoder port surface is referenced by this
	// test (so the searchpkg import has a real consumer in the test
	// surface, not just at compile-time via the production file).
	var chEnc searchpkg.ChannelEncoder = enc
	_ = chEnc
}
