package wiring

import (
	"context"
	"fmt"

	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	systemapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/system"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/embeddings"
)

// searchEmbedAdapter was DELETED here on 2026-09-20. It bridged the
// infrastructure embeddings.TextEmbedder to the application search.QueryEmbedder
// port, but was never constructed anywhere in the tree, so its Embed method was
// unreachable. The production semantic-search path no longer routes through the
// single-text QueryEmbedder seam: it goes through the per-channel
// EmbeddingChannelRegistry below (embeddingRegistryAdapter.EmbedQuery +
// textChannelEncoderAdapter.EmbedTextQuery), which is why that registry is the
// canonical surface and gets constructed while this one never was.

// embeddingRegistryAdapter is the composition-only implementation of the
// canonical embedding-channel registry.
type embeddingRegistryAdapter struct {
	adapters map[string]searchpkg.ChannelEncoder
}

var _ searchpkg.EmbeddingChannelRegistry = (*embeddingRegistryAdapter)(nil)

func newEmbeddingRegistryAdapter(textEmbedder embeddings.TextEmbedder, siglipEncoder searchpkg.ChannelEncoder) searchpkg.EmbeddingChannelRegistry {
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
	textEmbedder embeddings.TextEmbedder
}

func (a *textChannelEncoderAdapter) EmbedTextQuery(ctx context.Context, text string) ([]float32, error) {
	if a == nil || a.textEmbedder == nil {
		return nil, fmt.Errorf("textChannelEncoderAdapter: underlying embeddings.TextEmbedder not wired: %w",
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

// ── DoctorConfig snapshot factory ────────────────────────────────────────────

// doctorConfigFrom reads the diagnostic-relevant fields off *config.Config
// and packs them into a value-typed snapshot. Eager path resolution
// (AssetsPath(), ImagesPath(), TempDir() etc.) means the handler holds
// plain strings, not method receivers — easier to test, easier to fake.
// Returns the zero-value DoctorConfig if cfg is nil so callers don't need
// to nil-check before passing it into NewModule.
func doctorConfigFrom(cfg *config.Config) systemapi.DoctorConfig {
	if cfg == nil {
		return systemapi.DoctorConfig{}
	}
	return systemapi.DoctorConfig{
		DataDir:                   cfg.Storage.DataDir,
		AssetsPath:                cfg.Storage.AssetsPath(),
		ImagesPath:                cfg.Storage.ImagesPath(),
		TempPath:                  cfg.Storage.TempPath(),
		AnimationsPath:            cfg.Storage.AnimationsPath(),
		YoutubeClipsPath:          cfg.Storage.YoutubeClipsPath(),
		PythonScriptsDir:          cfg.Paths.PythonScriptsDir,
		GoogleAccountingEnabled:   cfg.GoogleAccounting.Enabled,
		GoogleAccountingServerURL: cfg.GoogleAccounting.ServerURL,
		// The doctor must probe the SAME executables the runtime resolves
		// (see DoctorConfig.YtdlpPath). Resolved here so the handler keeps
		// holding plain strings and stays a thin transport.
		YtdlpPath:  cfg.External.ResolvedYtdlpPath(),
		FfmpegPath: cfg.External.FfmpegPath,
	}
}
