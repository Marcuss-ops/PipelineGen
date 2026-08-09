// Package app — WireAssets search capability builder (PR-WIRE-ASSETS-CAPABILITY-SPLIT, July 2026).
//
// The search capability consumes the canonical *search.Aggregator
// (pre-built in WireAssets from searchBackends + log) — no other
// helpers needed. This is the thinnest of the 7 build*Bundle functions.
//
// godlike/06 SSOT: this file is the canonical owner of the search
// build pipeline. The canonical search handler lives in
// internal/api/assets/search/; this file is composition-root glue only.
//
// PR-WIRE-ASSETS-NIL-CLASSIFICATION (2026-07-25): the descriptor
// type-assertion goes through ClassifyDepGet (DepRequired, production
// fail-closed).
package app

import (
	"fmt"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/api/assets/search"
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	assetresolver "github.com/Marcuss-ops/PipelineGen/internal/application/assets/resolver"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"go.uber.org/zap"
)

// buildSearchBundle constructs the canonical *assetsearch.SearchDescriptor.
//
// Blocco C1-Step 11 (June 2026): the Handler is constructed inside
// Build and captured by the returned SearchDescriptor's Module closure.
// The composition site type-asserts ONCE to *assetsearch.SearchDescriptor
// (fail-closed) and reuses the concrete for the
// assetsapi.NewModule(..., Search: sd, ...) call (the concrete
// *SearchDescriptor satisfies api.Descriptor structurally). The search
// capability has no non-HTTP consumer in the codebase (POST /search is
// the entire public surface, reachable only via HTTP), so the
// Descriptor surface is the smallest possible — just `Module` field +
// forwarder methods (matches the stock / voiceover / soundeffect /
// register / diagnostics precedent exactly).
//
// The *search.Aggregator is constructed at the composition root from
// the pre-built SearchBackends + ZapLogAdapter per AGENTS.md Pattern 0
// — the api/ layer never builds it.
func buildSearchBundle(
	log *zap.Logger,
	searchAggregator *search.Aggregator,
	providerRegistry *providers.Registry,
) (*assetsearch.SearchDescriptor, error) {
	descriptor, err := assetsearch.Build(assetsearch.Dependencies{
		Aggregator:  searchAggregator,
		Resolver:    assetresolver.NewService(providerRegistry),
		EnabledFunc: func() bool { return true }, // search is always on in production
		ModuleOpts:  nil,                         // no per-feature middleware (matches pre-Step-11 wiring)
		Logger:      log,
	})
	if err != nil {
		return nil, err
	}
	desc, ok := descriptor.(*assetsearch.SearchDescriptor)
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: search (got %T, want *assetsearch.SearchDescriptor)", descriptor), !ok || desc == nil, DepRequired, log); err != nil {
		return nil, err
	}
	return desc, nil
}
