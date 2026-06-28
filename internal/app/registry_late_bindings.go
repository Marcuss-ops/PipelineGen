// Package app — late-binding handlers + registry freeze (PR4 split).
//
// PR4 mechanical split (June 2026): relocated from registry.go without
// signature or behaviour changes. This file owns the LAST two steps of
// the orchestrator:
//
//   - applyLateBindings constructs the QDRANT-002 outbox handler,
//     the QDRANT-004 mediasearch handler, and the ProviderRegistry
//     finalization (RegisterSearch × 2 + RegisterFetch × 1 +
//     ScriptAssets descriptor publish). These run AFTER all module
//     registrations because:
//     1. The outbox handler must NOT be registered through /api/*
//     (separation-of-routes invariant pinned by
//     internal/api/routes_test.go).
//     2. The mediasearch handler depends on
//     wiring.Assets.SearchAggregator, populated by Step 5.
//     3. The ProviderRegistry finalization must run AFTER each
//     provider's primary module registration so the providers
//     map is non-empty when Freeze is called.
//
//   - freezeRegistries calls ProviderRegistry.Freeze(), the
//     composition-time close of the registry write side. Reviewer Q8
//     fix: must be the final mutation in WireRegistry.
package app

import (
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	mediasearchapi "github.com/Marcuss-ops/PipelineGen/internal/api/mediasearch"
	outboxapi "github.com/Marcuss-ops/PipelineGen/internal/api/outbox"
	artlistadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	stockadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock"
	youtubeadapter "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/youtube"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scriptassets"

	"go.uber.org/zap"
)

// applyLateBindings finalizes the wiring composition surface:
//   - outbox events handler (QDRANT-002 separation-of-routes)
//   - mediasearch handler (QDRANT-004 separation-of-routes)
//   - ProviderRegistry adapter registrations (artlist / youtube +
//     stock fetch). Must run BEFORE pr.Freeze() so the registry is
//     still mutable.
//
// Returns nil on notification-only blocks; propagation is per-block
// because the orchestrator treats late-binding failures as fatal.
func applyLateBindings(registry *module.Registry, log *zap.Logger, root *ComposeRoot, wiring *RegistryWiring) error {
	// ── QDRANT-002: build canonical internal outbox handler ─────
	// Exposes GET /internal/v1/outbox/status and /events for
	// operator dashboard visibility into the outbox events pipeline.
	//
	// QDRANT-002 (June 2026) separation-of-routes fix: the handler
	// is constructed here but NOT registered in the public /api
	// registry — that caused /api/internal/v1/outbox/* to leak past
	// the WorkerAuth boundary. The handler is now passed to
	// AppDeps.OutboxHandler and mounted on the /internal/v1
	// WorkerAuth-protected internalGroup by cmd/server/main.go.
	if root.Outbox != nil && root.Outbox.EventsRepo != nil {
		outboxPort := newOutboxMonitorAdapter(root.Outbox.EventsRepo)
		outboxH := outboxapi.NewHandler(outboxPort, log)
		wiring.OutboxHandler = outboxH
		log.Info("QDRANT-002: outbox events handler BUILT (mounted on /internal/v1/outbox via AppDeps, NOT via /api)")
	}

	// ── QDRANT-004: build mediasearch handler ─────────────────────
	// Wires the unified media search API at POST /internal/v1/media/search
	// when Qdrant is enabled and the vector store adapter is available.
	//
	// QDRANT-004 (June 2026) separation-of-routes fix: same
	// reasoning as the outbox handler above — the handler is
	// constructed here but NOT registered through the public /api
	// registry. AppDeps.MediasearchHandler is mounted on
	// internalGroup by cmd/server/main.go.
	if root.Process.VectorSvc != nil && root.AI != nil && root.AI.OllamaClient != nil {
		// PR 10 (June 2026): the canonical *search.Aggregator is
		// the SOLE wire for media search results. The handler now
		// takes WireParams{Aggregator, Log}. wiring.Assets.SearchAggregator
		// is populated by WireAssets' BuildSearchBackends +
		// NewAggregator (search_backends.go). When nil, the
		// handler returns 503 on every Search call (fail-closed).
		var searchAgg mediasearchapi.AggregatorSearcher
		if wiring.Assets != nil && wiring.Assets.SearchAggregator != nil {
			searchAgg = wiring.Assets.SearchAggregator
		}
		searchH := mediasearchapi.NewHandler(mediasearchapi.WireParams{Aggregator: searchAgg, Log: log})
		wiring.MediasearchHandler = searchH
		log.Info("QDRANT-004: mediasearch handler BUILT (mounted on /internal/v1/media/search via AppDeps, NOT via /api)")
	}

	// ── ProviderRegistry — register adapters + FREEZE at the end
	// Lives on SearchBundle (PR4 review): it's an asset-search
	// dispatch registry, not a Drive-sync concern.
	if root.Search != nil && root.Search.ProviderRegistry != nil {
		pr := root.Search.ProviderRegistry
		if wiring.ArtlistSvc != nil && wiring.ArtlistSvc.Service != nil {
			if err := pr.RegisterSearch(artlistadapter.NewAdapter(wiring.ArtlistSvc.Service)); err != nil {
				log.Warn("failed to register artlist provider", zap.Error(err))
			} else {
				log.Info("registered artlist provider in providers.Registry")
			}
		} else {
			log.Info("artlist service unavailable \u2014 skipping provider registration")
		}
		if wiring.YouTubeClip != nil && wiring.YouTubeClip.Service != nil {
			if err := pr.RegisterSearch(youtubeadapter.NewAdapter(wiring.YouTubeClip.Service)); err != nil {
				log.Warn("failed to register youtube provider", zap.Error(err))
			} else {
				log.Info("registered youtube provider in providers.Registry")
			}
		} else {
			log.Info("youtube clip service unavailable \u2014 skipping provider registration")
		}
		if wiring.StockPipeline != nil && wiring.StockPipeline.Service != nil {
			if err := pr.RegisterFetch(stockadapter.NewAdapter(wiring.StockPipeline.Service)); err != nil {
				log.Warn("failed to register stock fetch provider", zap.Error(err))
			} else {
				log.Info("registered stock fetch provider in providers.Registry")
			}
		} else {
			log.Info("stock pipeline service unavailable \u2014 skipping fetch provider registration")
		}
		// ── ScriptAssets capability — RegisterProviders must run
		// BEFORE pr.Freeze() below; the registry must be mutable
		// when the descriptor publishes into it. Frozen registries
		// return ErrFrozen from Register, so ordering matters.
		//
		// The script_assets capability is wired via scriptassets.Build(deps).
		// Build returns a single Descriptor that carries:
		//   - the api.Module for /script-assets routes, AND
		//   - the api.DescriptorProviders slot which the composition
		//     root uses to publish the script_assets catalog entry
		//     (provider identity + capabilities) into the canonical
		//     providers.Registry.
		scDesc, scErr := scriptassets.Build(scriptassets.Dependencies{
			Logger: log,
		})
		if scErr != nil {
			log.Warn("failed to wire module", zap.String("module", "script-assets"), zap.Error(scErr))
		} else {
			if err := tryRegisterModuleStrict(registry, log, scDesc, WithRegistrationPoint("register.ScriptAssets")); err != nil {
				return err
			}
			// *ScriptAssetsDescriptor satisfies api.Descriptor via
			// the three explicit delegation methods (Name/Enabled/
			// RegisterRoutes), and api.DescriptorProviders via
			// RegisterProviders (same concrete pointer cast as the
			// generation block above).
			if dp, ok := scDesc.(*scriptassets.ScriptAssetsDescriptor); ok {
				if err := dp.RegisterProviders(pr); err != nil {
					return err
				}
				log.Info("registered script_assets catalog entry in providers.Registry",
					zap.String("name", "script_assets"),
					zap.Strings("capabilities", []string{"search", "script"}))
			}
		}
	}

	// Register + Freeze live in two distinct functions so the
	// orchestrator's wireframe stays sequential.
	return finalizeProviderRegistry(root, log)
}

// finalizeProviderRegistry calls ProviderRegistry.Freeze() after the
// late-binding blocks above have populated every adapter slot.
// Pulled out so the orchestrator's step 6 (applyLateBindings) and
// step 7 (freezeRegistries) are two distinct function calls, surfacing
// the Reviewer Q8 invariant that Freeze() is the canonical gate.
func finalizeProviderRegistry(root *ComposeRoot, log *zap.Logger) error {
	if root.Search == nil || root.Search.ProviderRegistry == nil {
		return nil
	}
	pr := root.Search.ProviderRegistry
	// FREEZE here, after all registrations. (Reviewer Q8 fix.)
	pr.Freeze()
	log.Info("providers.Registry frozen at end of WireRegistry",
		zap.Int("providers", len(pr.All())))
	return nil
}

// freezeRegistries is the publicly-callable freeze step. The orchestrator
// in registry.go calls this AFTER applyLateBindings; the implementation
// is a one-line passthrough so the orchestrator's wireframe stays
// symmetric (applyLateBindings returns err, freezeRegistries is fire-and-
// forget — the freeze is best-effort and its result is logged but not
// propagated to the orchestrator error path).
func freezeRegistries(root *ComposeRoot) {
	_ = finalizeProviderRegistry(root, zap.NewNop())
}
