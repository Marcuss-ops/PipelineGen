// Package scriptassets — provider.go: the canonical
// providers.SearchProvider adapter for the script_assets capability.
//
// Capability Standard (June 2026) DescriptorProviders slot migration:
//
//   - This file defines ScriptAssetsProvider as a providers.SearchProvider
//     (so providers.Registry accepts it via Register / RegisterSearch).
//   - The composition root type-asserts ScriptAssetsDescriptor for
//     api.DescriptorProviders and calls RegisterProviders(reg) during
//     WireRegistry, BEFORE the existing pr.Freeze().
//
// The provider exposes a deterministic, script-style-keyed catalog so
// downstream search callers can find script-to-asset mappings. This
// is the stand-up implementation: production enrichment (per-topic
// mappings, language-specific entries, voice preferences) lands in a
// follow-up PR. The DescriptorProviders slot contract is the focus
// of this migration; the runtime catalog is intentionally minimal.
package scriptassets

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Compile-time assertion: *ScriptAssetsProvider satisfies
// providers.SearchProvider. Drift in the providers package surfaces
// here as a build error, not at first composition.
var _ providers.SearchProvider = (*ScriptAssetsProvider)(nil)

// ScriptAssetsProvider is the canonical catalog-publishing entry for
// the script_assets capability. Statically bound — no background
// goroutines, no per-call state, no late-bindings. Composition-time
// registration via the DescriptorProviders slot is its only effect.
type ScriptAssetsProvider struct {
	log Logger
}

// Logger is the minimal logger surface ScriptAssetsProvider needs.
// Mirrors the *zap.Logger subset rather than importing zap directly
// so tests can substitute a no-op (production wires *zap.Logger;
// tests pass a struct implementing the Logger contract).
type Logger interface {
	Info(string, ...any)
	Warn(string, ...any)
}

// NewScriptAssetsProvider constructs a ready-to-register provider.
// log may be nil (tests substitute nil-friendly; production sets a
// *zap.Logger).
func NewScriptAssetsProvider(log Logger) *ScriptAssetsProvider {
	return &ScriptAssetsProvider{log: log}
}

// Name implements providers.Provider. Stable across calls; identical
// to the module's route prefix slug so operators can correlate
// routes ↔ catalog entries.
func (s *ScriptAssetsProvider) Name() string { return "script_assets" }

// Capabilities implements providers.Provider. The provider advertises
// CapabilitySearch (so the registry accepts it via RegisterSearch)
// AND CapabilityScript (the new capability tag identifying script-
// family catalog entries — distinct from video/image/music/voice
// because script output is a textual artifact, not a media asset).
//
// CapabilityFetch is INTENTIONALLY NOT advertised: script-to-asset
// mapping has no download stage (downstream media composition
// fetches the resolved assets separately).
func (s *ScriptAssetsProvider) Capabilities() []providers.Capability {
	return []providers.Capability{
		providers.CapabilitySearch,
		providers.CapabilityScript,
	}
}

// Search implements providers.SearchProvider. Today's stand-up catalog
// is deterministic per query: one placeholder candidate per query,
// owned by the script_assets source, with MediaType MediaTypeScript.
//
// Production enrichment (per-topic rankings, language-specific
// weighting, voice preference tags) lives in a follow-up PR.
func (s *ScriptAssetsProvider) Search(ctx context.Context, req providers.SearchRequest) (providers.SearchResult, error) {
	if s == nil {
		return providers.SearchResult{}, fmt.Errorf("script_assets: provider not initialized")
	}
	if req.Query == "" {
		return providers.SearchResult{}, fmt.Errorf("script_assets: query is required")
	}
	if s.log != nil {
		s.log.Info("script_assets: search", "query", req.Query, "limit", req.Limit)
	}
	_ = ctx // future enrichment may use ctx for cancellation; today no I/O.
	return providers.SearchResult{
		Candidates: []providers.Candidate{
			{
				SourceName: s.Name(),
				SourceRef:  "script_assets://" + req.Query,
				Title:      req.Query,
				MediaType:  asset.MediaTypeScript,
				Score:      1.0,
			},
		},
		// NextPageToken intentionally empty: today's stand-up catalog
		// is one-candidate-per-query with no pagination. Follow-up PRs
		// add cursors once the catalog grows multi-entry.
		NextPageToken: "",
	}, nil
}
