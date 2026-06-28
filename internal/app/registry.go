// Package app — slim composition orchestrator (PR4 mechanical split).
//
// PR4 mechanical split (June 2026): WireRegistry is now a 7-step
// orchestrator that delegates each concern to a dedicated helper
// file in the same package `app`. The original 819-line file has
// been split into 8 files:
//
//   - registry.go                 (this file) — slim orchestrator
//   - RegistryWiring struct + WireRegistry function + package doc.
//   - registry_registration.go    tryRegisterModule + tryRegisterModuleStrict
//   - strictOption + WithRegistrationPoint + collectRegPoint
//     (fail-fast registration helpers).
//   - registry_public_modules.go  registerSystem + registerJobs +
//     registerImages + registerScriptHistory + registerUtility +
//     registerRealtime + registerGenerationCapability +
//     registerChannelsCapability + registerSearchQueriesCapability
//     (thin route modules exposed via /api/* paths).
//   - registry_internal_modules.go registerInternalModules wrapper
//     covering registerIdempotencyMiddleware + registerSearchBackend
//   - registerArtlist + registerYouTubeClip + registerMediaIngest
//   - registerScraper + registerFullImages + registerStockPipeline
//     (bundle-driven modules).
//   - registry_search.go          registerSearchBackend helper
//     (BuildCanonicalSearchFanOut + SearchAggregator wiring).
//   - registry_assets.go          registerAssets module
//     (maintenanceSvc + voiceoverSvc + assetsBundle + WireAssets
//   - SetDeletionService cycle).
//   - registry_script.go          registerScripts wrapper
//     (calls wireScriptFlow + registerScriptHistory).
//   - registry_late_bindings.go   applyLateBindings + freezeRegistries
//     (QDRANT-002 outbox handler + QDRANT-004 mediasearch handler +
//     ProviderRegistry RegisterSearch/RegisterFetch/ScriptAssets.publish
//   - pr.Freeze).
//
// Co-located files (NOT touched by PR4): registry_helpers.go
// (composition helpers like initAssetServices used by composition.go
// — NOT by WireRegistry directly), registry_adapters.go
// (mutation-dispatcher adapter for the API ↔ application port),
// composition*.go (BuildSystemBundle / BuildRepositoryBundle etc.),
// wire_script.go (wireScriptFlow definition lives here, called
// from registerScripts), script_feature_flags.go (anyScriptFeatureEnabled),
// module_media.go (toolCheckerAdapter / processRunnerAdapter /
// dbHealthCheckerAdapter / Wire* fns), adapters_infra.go,
// outbox_monitor_adapter.go.
//
// PR4 spec notes:
//
//   - Data-flow ordering: registerInternalModules runs BEFORE
//     registerImages AND registerAssets because both consume
//     wiring.MediaIngest.Service (Images) and wiring.searchFanOut
//
//   - wiring.searchBackends + wiring.idempotencyHandler (Assets).
//     The user-stated orchestrator listing (assets before internal)
//     is illustrative; the executable order is internal → scripts →
//     images → assets → late-bindings → freeze.
//
//   - Strict-uniqueness invariant: every public_modules + internal_modules
//
//   - registerX call goes through tryRegisterModuleStrict
//     (PR2 codex/registry-strict-uniqueness). The strict path has
//     no shortcut for "we tried the old permissive helper" so the
//     composition-time guarantee is identical to the pre-PR4 code.
//
//   - Cross-step state lives on RegistryWiring as 3 unexported
//     fields (searchFanOut, searchBackends, idempotencyHandler).
//     These are populated by registerInternalModules and consumed
//     by registerAssets; they are NOT part of the public surface
//     and are NOT read by tests (no test asserts on them).
package app

import (
	"context"
	"fmt"

	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// RegistryWiring holds the registry and all wired modules.
//
// PR2 (June 2026): removed System, Jobs, Images, Drive, Scraper — those were
// thin Wire wrappers inlined directly in WireRegistry below.
//
// PR4 (June 2026, codex/registry-composition-split) cross-step state:
//   - searchFanOut / searchBackends / idempotencyHandler are populated
//     by registerInternalModules (specifically registerSearchBackend +
//     registerIdempotencyMiddleware) and consumed by registerAssets.
//   - These fields are unexported because they are an implementation
//     detail of the orchestration flow, NOT part of the public wiring
//     surface that callers (server bootstrap, tests) read.
type RegistryWiring struct {
	Registry      *module.Registry
	ArtlistSvc    *ArtlistWiring
	YouTubeClip   *YouTubeClipWiring
	MediaIngest   *MediaIngestWiring
	Assets        *AssetsWiring
	FullImages    *FullImagesWiring
	StockPipeline *StockPipelineWiring

	// QDRANT-002 + QDRANT-004 separation-of-routes (June 2026):
	// These handlers are constructed by WireRegistry but NOT registered
	// in the public /api registry. They are plumbed through AppDeps and
	// mounted on the /internal/v1 WorkerAuth-protected internalGroup
	// by cmd/server/main.go. The split is enforced by the
	// anti-regression test internal/api/routes_test.go.
	OutboxHandler      interface{ RegisterRoutes(*gin.RouterGroup) }
	MediasearchHandler interface{ RegisterRoutes(*gin.RouterGroup) }

	// PR4 (June 2026) cross-step state — populated by internal modules
	// registry, consumed by assets registration. Unexported.
	searchFanOut       search.SearchFanOut
	searchBackends     *search.BackendRegistry
	idempotencyHandler gin.HandlerFunc
}

// WireRegistry creates and populates the module registry with all modules.
//
// PR4d-final (June 2026): signature takes (ctx, cfg, log, root). The
// transitional cd parameter was removed. All reads source from root.<bundle>.
//
// PR4 (June 2026, codex/registry-composition-split): this function is
// now a 7-step orchestrator. Each step delegates to a dedicated helper
// file in the same package and returns responsibility for cross-step
// state via the unexported RegistryWiring fields (see type doc above).
func WireRegistry(ctx context.Context, cfg *config.Config, log *zap.Logger, root *ComposeRoot) (*RegistryWiring, error) {
	if root == nil {
		return nil, fmt.Errorf("wire registry: compose root is nil")
	}

	registry := module.NewRegistry()
	wiring := &RegistryWiring{Registry: registry}

	// Step 1 — System module: no bundle deps. Inlined from PR2-era
	// WireSystem into the orchestrator file by Wave 14 close.
	if err := registerSystem(registry, log, cfg, root); err != nil {
		return nil, fmt.Errorf("wire registry: system: %w", err)
	}

	// Step 2 — Internal modules (bundle-driven). MUST run before
	// registerImages (consumes wiring.MediaIngest.Service) and
	// registerAssets (consumes wiring.searchFanOut + wiring.searchBackends
	// + wiring.idempotencyHandler). Wraps registerIdempotencyMiddleware +
	// registerSearchBackend + registerArtlist + registerYouTubeClip +
	// registerMediaIngest + registerScraper + registerFullImages +
	// registerStockPipeline in the canonical DAG order.
	if err := registerInternalModules(ctx, registry, log, cfg, root, wiring); err != nil {
		return nil, fmt.Errorf("wire registry: internal-modules: %w", err)
	}

	// Step 3 — Scripts: wireScriptFlow orchestration + ScriptHistory module.
	if err := registerScripts(ctx, registry, log, cfg, root); err != nil {
		return nil, fmt.Errorf("wire registry: scripts: %w", err)
	}

	// Step 4 — Images: single route module; consumes wiring.MediaIngest.Service.
	if err := registerImages(registry, log, cfg, root, wiring); err != nil {
		return nil, fmt.Errorf("wire registry: images: %w", err)
	}

	// Step 5 — Assets: bundle-driven. Consumes wiring.searchFanOut +
	// wiring.searchBackends + wiring.idempotencyHandler; constructs
	// maintenanceSvc locally + calls WireAssets + performs the
	// DeletionService backfill on the maintenance service.
	if err := registerAssets(registry, log, cfg, root, wiring); err != nil {
		return nil, fmt.Errorf("wire registry: assets: %w", err)
	}

	// Step 6 — Late bindings: outbox handler + mediasearch handler +
	// ProviderRegistry RegisterSearch/RegisterFetch/ScriptAssets.publish.
	// Lives at the END because (a) the outbox handler must be ready
	// before any operator-dashboard route hits it, (b) the mediasearch
	// handler depends on wiring.Assets.SearchAggregator that Step 5
	// populated, and (c) ProviderRegistry.Freeze() is the very last
	// mutation of the registry — Reviewer Q8 fix.
	if err := applyLateBindings(registry, log, root, wiring); err != nil {
		return nil, fmt.Errorf("wire registry: late-bindings: %w", err)
	}

	// Step 7 — Freeze registries. ProviderRegistry.Freeze() is the
	// gate closing the composition-time write side and opening the
	// runtime read side.
	freezeRegistries(root)

	return wiring, nil
}
