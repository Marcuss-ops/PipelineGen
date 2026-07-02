// Package app — TDD contract tests for the embeddingRegistryAdapter
// composition-root wiring of search.EmbeddingChannelRegistry
// (PR-EMBEDDING-CHANNEL-REGISTRY, July 2026).
//
// Pins the contract that future encoders plug in at composition root
// without backend changes — when PR-CROSS-MODAL-TEXT-TO-VISUAL lands
// a SigLIP-text encoder for the visual channel, the wiring site is
// just `adapters[searchpkg.ChannelVisual] = newSigLIPEncoder(...)`;
// the semantic backend's EmbedQuery call site never changes.
package app

import (
	"context"
	"errors"
	"testing"

	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/application/search"
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
	reg := newEmbeddingRegistryAdapter(inner)
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
	reg := newEmbeddingRegistryAdapter(inner)
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
	reg := newEmbeddingRegistryAdapter(inner)
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
// RECOGNIZED but UNWIRED (forward-pointer: SigLIP-text encoder from
// PR-CROSS-MODAL-TEXT-TO-VISUAL). Errors.Is probe MUST reach
// ErrChannelNotConfigured without unwrapping.
func TestEmbeddingRegistryVisualNotConfigured(t *testing.T) {
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{})
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
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{})
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
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{})
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

// ── Fail-closed surface ──────────────────────────────────────────

// TestEmbeddingRegistryUnknownChannelRejected: off-vocabulary
// channel names return ErrChannelUnknown (programming error at the
// orchestrator level). The wrapped %w is the godlike/07 contract.
func TestEmbeddingRegistryUnknownChannelRejected(t *testing.T) {
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{})
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
	reg := newEmbeddingRegistryAdapter(&stubTextEmbedder{})
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
	reg := newEmbeddingRegistryAdapter(nil)
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
