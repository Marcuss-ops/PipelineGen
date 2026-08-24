// Package app — TDD contract tests for the embeddingRegistryAdapter
// composition-root wiring of search.EmbeddingChannelRegistry
// (PR-EMBEDDING-CHANNEL-REGISTRY, July 2026).
//
// Pins the contract that future encoders plug in at composition root
// without backend changes — when PR-CROSS-MODAL-TEXT-TO-VISUAL lands
// a SigLIP-text encoder for the visual channel, the wiring site is
// just `adapters[searchpkg.ChannelVisual] = newSigLIPEncoder(...)`;
// the semantic backend's EmbedQuery call site never changes.
package wiring

import (
	"context"
	"errors"
	"fmt"
	"testing"

	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
)

// stubTextEmbedder is the canonical test double for qdrant.TextEmbedder.
// Implements only the Embed method (lang/head-shape match); the other
// qdrant.TextEmbedder methods (if any) stay at zero-value (Go interface
// nil-implementation is fine — the test surface only calls Embed).
type stubTextEmbedder struct {
	vec      []float32
	calls    int
	embedErr error
	// lastText is what was passed to the most-recent Embed call;
	// tests inspect it to confirm the registry routed through the
	// right underlying encoder without leaking text elsewhere.
	lastText string
}

func (s *stubTextEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	s.calls++
	s.lastText = text
	if s.embedErr != nil {
		return nil, s.embedErr
	}
	if s.vec != nil {
		return s.vec, nil
	}
	// canonical fake vec — distinct slice per call so tests can
	// detect aliasing bugs upstream (slice identity changes per
	// Embed call).
	return []float32{1, 2, 3}, nil
}

// ── Live text + transcript routing ─────────────────────────────────

// TestEmbeddingRegistryTextChannelLive: the text channel delegates
// to the underlying qdrant.TextEmbedder. Future callers MUST be able
// to swap out the concrete via newEmbeddingRegistryAdapter(embedder)
// without touching the registry's surface.
func TestEmbeddingRegistryTextChannelLive(t *testing.T) {
	inner := &stubTextEmbedder{vec: []float32{0.5, 0.6, 0.7}}
	reg := newEmbeddingRegistryAdapter(inner, nil)
	if reg == nil {
		t.Fatal("newEmbeddingRegistryAdapter returned nil for non-nil embedder")
	}
	got, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelText, "hello world")
	if err != nil {
		t.Fatalf("text channel EmbedQuery: unexpected error %v", err)
	}
	if len(got) != 3 || got[0] != 0.5 || got[1] != 0.6 || got[2] != 0.7 {
		t.Errorf("text channel EmbedQuery: want vec[0.5, 0.6, 0.7], got %v", got)
	}
	if inner.calls != 1 {
		t.Errorf("text channel EmbedQuery: want 1 underlying call, got %d", inner.calls)
	}
	if inner.lastText != "hello world" {
		t.Errorf("text channel EmbedQuery: want text routed to encoder %q, got %q",
			"hello world", inner.lastText)
	}
}

// TestEmbeddingRegistryTranscriptChannelLive: transcript content is
// text — same underlying encoder serves both channels. This pins
// the godlike/06 SSOT contract that the channel-name vocabulary
// is the ONLY difference between text and transcript; the encoder
// is the same qdrant.TextEmbedder today.
func TestEmbeddingRegistryTranscriptChannelLive(t *testing.T) {
	inner := &stubTextEmbedder{vec: []float32{0.1, 0.2, 0.3}}
	reg := newEmbeddingRegistryAdapter(inner, nil)
	got, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelTranscript, "transcript content")
	if err != nil {
		t.Fatalf("transcript channel EmbedQuery: unexpected error %v", err)
	}
	if len(got) != 3 || got[0] != 0.1 {
		t.Errorf("transcript channel EmbedQuery: want vec[0.1, 0.2, 0.3], got %v", got)
	}
	if inner.calls != 1 || inner.lastText != "transcript content" {
		t.Errorf("transcript channel: route check failed (calls=%d lastText=%q)",
			inner.calls, inner.lastText)
	}
}

// TestEmbeddingRegistryTextAndTranscriptShareEncoder: the registry
// routes text + transcript through the same underlying encoder
// pointer. Don't return distinct encoder copies — sharing the
// canonical qdrant.TextEmbedder instance avoids double-loading
// the embedding model.
func TestEmbeddingRegistryTextAndTranscriptShareEncoder(t *testing.T) {
	inner := &stubTextEmbedder{}
	reg := newEmbeddingRegistryAdapter(inner, nil)
	// Two calls on each channel — verify they all route through
	// inner.calls == 4. Separate channels operating on separate
	// encoder copies would double the embedding model load + cost.
	if _, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelText, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelTranscript, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelText, "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelTranscript, "d"); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 4 {
		t.Errorf("text+transcript should share encoder: want 4 underlying calls, got %d", inner.calls)
	}
}

// ── Forward-pointer stub sentinels ────────────────────────────────

// TestEmbeddingRegistryVisualNotConfigured: the visual channel is
// RECOGNIZED but UNWIRED when siglipEncoder is nil at the composition
// root (siglip-text encoder from PR-CROSS-MODAL-TEXT-TO-VISUAL not yet
// wired). Errors.Is probe MUST reach ErrChannelNotConfigured without
// unwrapping.
func TestEmbeddingRegistryVisualNotConfigured(t *testing.T) {
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{}, nil)
	_, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelVisual, "landscape concept")
	if err == nil {
		t.Fatal("visual channel: want error, got nil")
	}
	if !errors.Is(err, searchpkg.ErrChannelNotConfigured) {
		t.Errorf("visual channel: want errors.Is(err, ErrChannelNotConfigured)=true, got %v", err)
	}
}

// TestEmbeddingRegistryAudioNotConfigured: same forward-pointer
// contract as visual — CLAP-text encoder from PR-CROSS-MODAL lands
// later.
func TestEmbeddingRegistryAudioNotConfigured(t *testing.T) {
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{}, nil)
	_, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelAudio, "ocean waves")
	if !errors.Is(err, searchpkg.ErrChannelNotConfigured) {
		t.Errorf("audio channel: want errors.Is(err, ErrChannelNotConfigured)=true, got %v", err)
	}
}

// TestEmbeddingRegistrySparseNotApplicable: the sparse channel is
// RECOGNIZED but NOT text-query encodable (Qdrant handles BM25
// server-side via SparseText in HybridSearchRequest). Errors.Is
// probe MUST reach ErrChannelNotApplicable — distinct from
// ErrChannelNotConfigured because the WRONG-disable reason surfaces
// differently in operator dashboards.
func TestEmbeddingRegistrySparseNotApplicable(t *testing.T) {
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{}, nil)
	_, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelSparse, "any text")
	if err == nil {
		t.Fatal("sparse channel: want error, got nil")
	}
	if !errors.Is(err, searchpkg.ErrChannelNotApplicable) {
		t.Errorf("sparse channel: want errors.Is(err, ErrChannelNotApplicable)=true, got %v", err)
	}
	// distinct from NotConfigured — operators need to know
	// "feature not applicable to this surface" vs "we haven't
	// wired the encoder yet".
	if errors.Is(err, searchpkg.ErrChannelNotConfigured) {
		t.Errorf("sparse channel MUST NOT match ErrChannelNotConfigured (distinct typed-error contract)")
	}
}

// ── PR-CROSS-MODAL-TEXT-TO-VISUAL: live SigLIP path ──────────────

// stubSiglipTextEncoder is the canonical test double for the
// SigLIP text encoder (satisfies search.ChannelEncoder). Tests
// inspect calls + lastText + returned vec to confirm the registry
// routes ChannelVisual through SigLIP-text end-to-end (godlike/07
// EXPAND-phase live path).
type stubSiglipTextEncoder struct {
	vec      []float32
	calls    int
	embedErr error
	lastText string
}

func (s *stubSiglipTextEncoder) EmbedTextQuery(_ context.Context, text string) ([]float32, error) {
	s.calls++
	s.lastText = text
	if s.embedErr != nil {
		return nil, s.embedErr
	}
	if s.vec != nil {
		return s.vec, nil
	}
	return []float32{0.7, 0.8, 0.9}, nil
}

// TestEmbeddingRegistryVisualLiveWithSigLIPEncoder: PR-CROSS-MODAL-TEXT-TO-VISUAL
// (August 2026) — when composition root provides a non-nil
// siglipEncoder, ChannelVisual routes through it (no stub fallback,
// no ErrChannelNotConfigured). The text query flows end-to-end via
// SigLIP-text and lands in the same 768d vector space as image-
// encoded video frames.
func TestEmbeddingRegistryVisualLiveWithSigLIPEncoder(t *testing.T) {
	inner := &stubTextEmbedder{}
	siglip := &stubSiglipTextEncoder{vec: []float32{0.11, 0.22, 0.33}}
	reg := newEmbeddingRegistryAdapter(inner, siglip)
	if reg == nil {
		t.Fatal("newEmbeddingRegistryAdapter returned nil")
	}
	got, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelVisual, "sunset over mountains")
	if err != nil {
		t.Fatalf("visual channel EmbedQuery: want nil error (live SigLIP path), got %v", err)
	}
	// Vec-identity pin (godlike/06 SSOT): the registry MUST return
	// the SigLIP-output vector verbatim, NOT a generic e5-shaped
	// vector. A future regression that routes ChannelVisual through
	// the wrong adapter would surface here (silent cross-modal
	// corruption catches at this seam before Qdrant upsert).
	if len(got) != len(siglip.vec) {
		t.Fatalf("visual channel EmbedQuery: want %dd (SigLIP-output), got %dd", len(siglip.vec), len(got))
	}
	for i, v := range siglip.vec {
		if got[i] != v {
			t.Errorf("visual channel EmbedQuery: vec[%d]=%v, want %v (SigLIP-output identity broken)",
				i, got[i], v)
		}
	}
	if siglip.calls != 1 {
		t.Errorf("visual channel EmbedQuery: want 1 underlying call, got %d", siglip.calls)
	}
	if siglip.lastText != "sunset over mountains" {
		t.Errorf("visual channel EmbedQuery: want text routed to SigLIP encoder %q, got %q",
			"sunset over mountains", siglip.lastText)
	}
	// Live visual path must NOT have leaked into the text encoder:
	// text channel uses the e5 path; visual uses SigLIP-text.
	if inner.calls != 0 {
		t.Errorf("visual channel call leaked into text embedder: got %d text calls", inner.calls)
	}
}

// TestEmbeddingRegistryVisualSigLIPEncoderErrorPropagates: when
// the SigLIP encoder returns an error, the registry propagates it
// unwrapped — the registry MUST own the routing decision but NOT
// the encoder-side error contract. We exercise this via the
// canonical ErrSigLIPDimensionMismatch typed sentinel so the assertion
// uses errors.Is (the godlike/07 routing contract), not raw string
// equality (brittle to future wrapping).
func TestEmbeddingRegistryVisualSigLIPEncoderErrorPropagates(t *testing.T) {
	inner := &stubTextEmbedder{}
	siglipErr := fmt.Errorf("siglip sidecar 500: %w", embeddings.ErrSigLIPDimensionMismatch)
	siglip := &stubSiglipTextEncoder{embedErr: siglipErr}
	reg := newEmbeddingRegistryAdapter(inner, siglip)
	_, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelVisual, "any text")
	if err == nil {
		t.Fatal("visual channel: want error from SigLIP encoder, got nil")
	}
	// godlike/07 typed-error probe via errors.Is beats raw string
	// equality; future wrapping (e.g., adding request ID context)
	// won't break this test.
	if !errors.Is(err, embeddings.ErrSigLIPDimensionMismatch) {
		t.Errorf("visual channel: want errors.Is(err, ErrSigLIPDimensionMismatch)=true, got %v", err)
	}
}

// ── Fail-closed surface ──────────────────────────────────────────

// TestEmbeddingRegistryUnknownChannelRejected: off-vocabulary
// channel names return ErrChannelUnknown (programming error at the
// orchestrator level). The wrapped %w is the godlike/07 contract.
func TestEmbeddingRegistryUnknownChannelRejected(t *testing.T) {
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{}, nil)
	for _, name := range []string{"", "TEXT", "vision", "bm25", "unknown", "textual"} {
		_, err := reg.EmbedQuery(context.Background(), name, "any text")
		if err == nil {
			t.Errorf("channel %q: want error, got nil", name)
			continue
		}
		if !errors.Is(err, searchpkg.ErrChannelUnknown) {
			t.Errorf("channel %q: want errors.Is(err, ErrChannelUnknown)=true, got %v", name, err)
		}
	}
}

// TestEmbeddingRegistryEmptyTextRejected: empty text input is
// a programming error — the orchestrator MUST not call EmbedQuery
// with empty text. Registry fails closed + ErrChannelUnknown.
func TestEmbeddingRegistryEmptyTextRejected(t *testing.T) {
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{}, nil)
	_, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelText, "")
	if err == nil {
		t.Fatal("empty text query: want error, got nil")
	}
	if !errors.Is(err, searchpkg.ErrChannelUnknown) {
		t.Errorf("empty text query: want errors.Is(err, ErrChannelUnknown)=true, got %v", err)
	}
}

// TestEmbeddingRegistryNilEmbedderFallback: when composition
// root passes nil qdrant.TextEmbedder (configuration deferred),
// text + transcript channels ship as notConfiguredAdapter stubs
// so the failure surfaces with the documented sentinel rather
// than a panic-nil dereference.
func TestEmbeddingRegistryNilEmbedderFallback(t *testing.T) {
	reg := newEmbeddingRegistryAdapter(nil, nil)
	if reg == nil {
		t.Fatal("newEmbeddingRegistryAdapter(nil) MUST return a non-nil registry (fail-closed carrier, not nil)")
	}
	for _, ch := range []string{searchpkg.ChannelText, searchpkg.ChannelTranscript} {
		_, err := reg.EmbedQuery(context.Background(), ch, "any text")
		if !errors.Is(err, searchpkg.ErrChannelNotConfigured) {
			t.Errorf("nil embedder / channel %s: want ErrChannelNotConfigured, got %v", ch, err)
		}
	}
	// visual / audio / sparse stay on their canonical forward-pointer
	// sentinels: visual/audio still NotConfigured, sparse NotApplicable.
	for _, ch := range []string{searchpkg.ChannelVisual, searchpkg.ChannelAudio} {
		_, err := reg.EmbedQuery(context.Background(), ch, "any text")
		if !errors.Is(err, searchpkg.ErrChannelNotConfigured) {
			t.Errorf("nil embedder / channel %s: want ErrChannelNotConfigured, got %v", ch, err)
		}
	}
	if _, err := reg.EmbedQuery(context.Background(), searchpkg.ChannelSparse, "any text"); !errors.Is(err, searchpkg.ErrChannelNotApplicable) {
		t.Errorf("nil embedder / sparse channel: want ErrChannelNotApplicable, got %v", err)
	}
}
