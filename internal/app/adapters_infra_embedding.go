package app

import (
	"context"
	"fmt"

	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
)

// ── Search query embedder adapter (Fase 6 Spina Dorsale) ───────────────────
//
// searchEmbedAdapter bridges the infrastructure-layer search.TextEmbedder
// to the application-layer search.QueryEmbedder port.
//
// PR-ADAPTERS-SPLIT (July 2026): extracted from adapters_infra.go per
// AGENTS.md Pattern 5.

type searchEmbedAdapter struct {
	embedder search.TextEmbedder
}

// Compile-time assertion (see adapters_infra.go).
// var _ searchpkg.QueryEmbedder = (*searchEmbedAdapter)(nil)

func (a *searchEmbedAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	if a == nil || a.embedder == nil {
		return nil, fmt.Errorf("searchEmbedAdapter: underlying qdrant embedder not wired")
	}
	return a.embedder.Embed(ctx, text)
}

// ── Embedding channel registry adapter (PR-EMBEDDING-CHANNEL-REGISTRY, July 2026) ───
//
// Composition-only-seam concrete for search.EmbeddingChannelRegistry
// (Pattern 0).

type embeddingRegistryAdapter struct {
	adapters map[string]searchpkg.ChannelEncoder
}

// Compile-time assertion (see adapters_infra.go).
// var _ searchpkg.EmbeddingChannelRegistry = (*embeddingRegistryAdapter)(nil)

func newEmbeddingRegistryAdapter(textEmbedder search.TextEmbedder, siglipEncoder searchpkg.ChannelEncoder) searchpkg.EmbeddingChannelRegistry {
	adapters := make(map[string]searchpkg.ChannelEncoder, len(searchpkg.CanonicalChannelNames()))

	if textEmbedder != nil {
		enc := &textChannelEncoderAdapter{textEmbedder: textEmbedder}
		adapters[searchpkg.ChannelText] = enc
		adapters[searchpkg.ChannelTranscript] = enc
	} else {
		adapters[searchpkg.ChannelText] = notConfiguredAdapter{}
		adapters[searchpkg.ChannelTranscript] = notConfiguredAdapter{}
	}

	if siglipEncoder != nil {
		adapters[searchpkg.ChannelVisual] = siglipEncoder
	} else {
		adapters[searchpkg.ChannelVisual] = notConfiguredAdapter{}
	}

	adapters[searchpkg.ChannelAudio] = notConfiguredAdapter{}
	adapters[searchpkg.ChannelSparse] = notApplicableAdapter{}

	return &embeddingRegistryAdapter{adapters: adapters}
}

func (r *embeddingRegistryAdapter) EmbedQuery(ctx context.Context, channel string, text string) ([]float32, error) {
	if r == nil {
		return nil, fmt.Errorf("embeddingRegistryAdapter: registry not wired: %w", searchpkg.ErrChannelUnknown)
	}
	if !searchpkg.IsKnownChannel(channel) {
		return nil, fmt.Errorf("embeddingRegistryAdapter: channel %q: %w", channel, searchpkg.ErrChannelUnknown)
	}
	if text == "" {
		return nil, fmt.Errorf("embeddingRegistryAdapter: channel %q: empty text query: %w", channel, searchpkg.ErrChannelUnknown)
	}
	adapter, ok := r.adapters[channel]
	if !ok || adapter == nil {
		return nil, fmt.Errorf("embeddingRegistryAdapter: channel %q: %w", channel, searchpkg.ErrChannelNotConfigured)
	}
	return adapter.EmbedTextQuery(ctx, text)
}

type textChannelEncoderAdapter struct {
	textEmbedder search.TextEmbedder
}

func (a *textChannelEncoderAdapter) EmbedTextQuery(ctx context.Context, text string) ([]float32, error) {
	if a == nil || a.textEmbedder == nil {
		return nil, fmt.Errorf("textChannelEncoderAdapter: underlying search.TextEmbedder not wired: %w",
			searchpkg.ErrChannelNotConfigured)
	}
	return a.textEmbedder.Embed(ctx, text)
}

type notConfiguredAdapter struct{}

func (notConfiguredAdapter) EmbedTextQuery(_ context.Context, _ string) ([]float32, error) {
	return nil, searchpkg.ErrChannelNotConfigured
}

type notApplicableAdapter struct{}

func (notApplicableAdapter) EmbedTextQuery(_ context.Context, _ string) ([]float32, error) {
	return nil, searchpkg.ErrChannelNotApplicable
}
