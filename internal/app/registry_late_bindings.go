// Package app — late-binding handlers + provider registration queue
// (Blocco C1-Step 2 refactor, June 2026).
//
// Replaces the pre-Step-2 shape (Outbox handler + Mediasearch
// handler + direct pr.RegisterSearch/RegisterFetch inline).
// Today the file owns ONLY:
//   - applyLateBindings: builds the QDRANT-002 outbox handler +
//     QDRANT-004 mediasearch handler + COLLECTS provider
//     registration entries (artlist + youtube + stock) into a
//     returned slice, AND publishes the script_assets descriptor
//     (HTTP module via tryRegisterModuleStrict + the gate-safe
//     descriptor RegisterProviders slot).
//
// The provider entries are passed by the orchestrator
// (WireRegistry Step 7) into registerCapabilities which is the
// ONLY function in internal/app/** allowed to call
// providers.Registry.RegisterSearch/RegisterFetch (gate enforced
// by capability_registry_gate_test.go).
//
// The pre-Step-2 finalizeProviderRegistry + freezeRegistries were
// merged into registerProviders inside capability_registry.go:
// the canonical Freeze() now lands at the absolute last mutation
// of WireRegistry (right after the last RegisterFetch call),
// preserving the Reviewer Q8 invariant without an extra
// orchestrate step in registry.go.
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
//   - QDRANT-002 outbox events handler (separation-of-routes).
//   - QDRANT-004 mediasearch handler (separation-of-routes).
//   - ProviderRegistry adapter entry collection (artlist / youtube
//     search + stock fetch) — entries are returned to the
//     orchestrator and registered by registerCapabilities
//     (capability_registry.go).
//   - script_assets capability (HTTP module via canonical
//     tryRegisterModuleStrict + Descriptor.RegisterProviders slot,
//     gate-safe by design because RegisterProviders is a
//     Descriptor-level method, NOT a typed providers.Registry
//     .Register call).
//
// Returns:
//   - []TrackedProviderEntry to feed into CapabilityDeps.Providers
//     (capacity = number of adapters registered; nil-safe when
//     root.Search.ProviderRegistry is nil).
//   - error: first non-nil error from any sub-step (composition-time
//     fail-closed for HTTP route registration + script_assets slot).
//
// Run AFTER all module registrations because:
//  1. The outbox handler must NOT be registered through /api/*
//     (separation-of-routes invariant pinned by
//     internal/api/routes_test.go).
//  2. The mediasearch handler depends on
//     wiring.Assets.SearchAggregator, populated by Step 5.
//  3. The script_assets descriptor RegisterProviders publishes
//     into the providers.Registry; the Freeze gate (from
//     registerProviders) MUST land AFTER this call.
func applyLateBindings(registry *module.Registry, log *zap.Logger, root *ComposeRoot, wiring *RegistryWiring) ([]TrackedProviderEntry, error) {
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

	// ── ProviderRegistry — collect adapter entries (no inline
	// Register*) so capability_registry.go's registerProviders
	// remains the canonical single composition point for
	// providers.Registry mutation surface (capability_registry_gate_test.go
	// enforces the invariant). Script_assets also lives inside the
	// same gate so its descriptor-level RegisterProviders slot
	// lands BEFORE the canonical Freeze() inside registerProviders
	// (Step 7); the slot is gate-safe by design (RegisterProviders
	// is a Descriptor-level method, NOT a typed providers.Registry
	// .Register call).
	var providerEntries []TrackedProviderEntry
	if root.Search != nil && root.Search.ProviderRegistry != nil {
		if wiring.ArtlistSvc != nil && wiring.ArtlistSvc.Service != nil {
			providerEntries = append(providerEntries, TrackedProviderEntry{
				Id:     "artlist",
				Kind:   ProviderKindSearch,
				Search: artlistadapter.NewAdapter(wiring.ArtlistSvc.Service),
			})
			log.Info("queued artlist provider for canonical registration via registerCapabilities")
		} else {
			log.Info("artlist service unavailable \u2014 skipping provider registration")
		}
		if wiring.YouTubeClip != nil && wiring.YouTubeClip.Service != nil {
			providerEntries = append(providerEntries, TrackedProviderEntry{
				Id:     "youtube",
				Kind:   ProviderKindSearch,
				Search: youtubeadapter.NewAdapter(wiring.YouTubeClip.Service),
			})
			log.Info("queued youtube provider for canonical registration via registerCapabilities")
		} else {
			log.Info("youtube clip service unavailable \u2014 skipping provider registration")
		}
		if wiring.StockPipeline != nil && wiring.StockPipeline.Service != nil {
			providerEntries = append(providerEntries, TrackedProviderEntry{
				Id:    "stock",
				Kind:  ProviderKindFetch,
				Fetch: stockadapter.NewAdapter(wiring.StockPipeline.Service),
			})
			log.Info("queued stock fetch provider for canonical registration via registerCapabilities")
		} else {
			log.Info("stock pipeline service unavailable \u2014 skipping fetch provider registration")
		}

		// ── ScriptAssets capability — RegisterProviders must run
		// BEFORE the canonical Freeze() inside registerProviders
		// (Step 7), so this block lands in applyLateBindings (Step 6)
		// before the orchestrator's final-mutation step. The
		// gate-safe pattern: HTTP module via the canonical
		// tryRegisterModuleStrict + descriptor's RegisterProviders
		// slot (NOT providers.Registry-equivalent surface).
		//
		// scriptassets.Build returns a single Descriptor that
		// carries both the api.Module (for /script-assets routes)
		// and the api.DescriptorProviders slot (which the
		// composition root uses to publish the script_assets
		// catalog entry — provider identity + capabilities — into
		// the canonical providers.Registry).
		scDesc, scErr := scriptassets.Build(scriptassets.Dependencies{
			Logger: log,
		})
		if scErr != nil {
			log.Warn("failed to wire module", zap.String("module", "script-assets"), zap.Error(scErr))
		} else {
			if err := tryRegisterModuleStrict(registry, log, scDesc, WithRegistrationPoint("register.ScriptAssets")); err != nil {
				return nil, err
			}
			// *ScriptAssetsDescriptor satisfies api.Descriptor via
			// the three explicit delegation methods and
			// api.DescriptorProviders via RegisterProviders.
			if dp, ok := scDesc.(*scriptassets.ScriptAssetsDescriptor); ok {
				if err := dp.RegisterProviders(root.Search.ProviderRegistry); err != nil {
					return nil, err
				}
				log.Info("registered script_assets catalog entry in providers.Registry",
					zap.String("name", "script_assets"),
					zap.Strings("capabilities", []string{"search", "script"}))
			}
		}
	}

	return providerEntries, nil
}

// ── Pre-Step-2 helpers removed in this PR ──
//
// The pre-Step-2 finalizeProviderRegistry + freezeRegistries
// helpers were deleted and merged into capability_registry.go's
// registerProviders (the canonical Freeze() lands at the
// absolute final mutation of WireRegistry via
// registerCapabilities Step 7). The functions were removed
// because they would now leak the pr.Freeze() call outside the
// canonical composition point — a strict-failure-mode hazard
// for the gate.
//
// If a future PR re-introduces a helper for diagnostics (e.g.
// a "pretend-freeze for boot-time config audit"), it MUST be
// renamed to avoid confusion with the canonical path.
