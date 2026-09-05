// Package app — slim composition orchestrator (PR4 mechanical split
// + Blocco C1-Step 2 centralisation, June 2026).
//
// PR4 mechanical split (June 2026): WireRegistry is now an ordered
// 8-step orchestrator that delegates each concern to a dedicated helper
// file in the same package `app`. The original 819-line file has
// been split into 10 files:
//
//   - registry.go                 (this file) — slim orchestrator
//   - RegistryWiring struct + WireRegistry function + package doc.
//   - capability_registry.go      registerCapabilities +
//     registerHTTPModules + registerProviders +
//     tryRegisterModule + tryRegisterModuleStrict + strictOption +
//     WithRegistrationPoint + collectRegPoint (the canonical
//     single composition point for typed-punctuated
//     Registry.Register mutations — Blocco C1-Step 2).
//     (PR-AUDIT-7: registerJobs removed — handler binding
//     is via c3ValidateRuntimeGraph, not this surface.)
//   - registry_public_modules.go  registerSystem + registerJobs +
//     registerImages + registerScriptHistory + registerUtility +
//     registerRealtime + registerAdminModule
//     (thin route modules exposed via /api/* paths).
//   - registry_internal_modules.go registerInternalModules wrapper
//   - registerArtlist + registerYouTubeClip + registerMediaIngest
//   - registerScraper + registerStockPipeline
//     (bundle-driven modules).
//   - search leaf (internal/app/wiring/search)  search.Build helper
//     (BuildCanonicalSearchFanOut + explicit search capability value).
//   - registry_assets.go          registerAssets module
//     (maintenanceSvc + voiceoverSvc + assetsBundle + WireAssets
//   - SetDeletionService cycle).
//   - registry_script.go          registerScripts wrapper
//     (calls wireScriptFlow + registerScriptHistory).
//   - registry_late_bindings.go   applyLateBindings (pure handler/module
//     preparation; provider registration completes before search composition).
//   - registry_mediamemory.go    registerMediaMemory (Step 5c helper —
//     wires the canonical MediaMemory resolve + bindings + feedback
//     surface).
//   - registry_runtime_graph.go  c3ValidateRuntimeGraph (Step 8 helper —
//     C3 startup validation of the canonical job runtime graph).
//
// The pre-Step-2 registry_registration.go was deleted in
// this PR (its contents relocated into capability_registry.go
// per the Wave C1 centralisation mandate).
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
// Blocco C1-Step 2 changes to the orchestrator:
//
//   - Step 6 (applyLateBindings) prepares handlers and the already-built
//     script-assets route module. Provider registration is complete before
//     search graph composition.
//
//   - Step 7 (registerCapabilities) publishes prepared HTTP modules and
//     validates the graph. It cannot mutate providers.Registry.
//
// Note (a): per-step registerX functions in
// registry_internal_modules.go + registry_public_modules.go +
// registry_assets.go + wire_script.go still call
// tryRegisterModuleStrict inline for HTTP module publications
// (Steps 2-5). Those callsites do NOT match the Blocco C1-Step
// 2 gate pattern (the literal substring api.Registry-equivalent Register
// is only present INSIDE tryRegisterModuleStrict's body, which
// lives in capability_registry.go). The Step-7 registerHTTPModules
// path is provided for forward-only use cases (caller accumulates
// modules in deps.HTTPModules instead of calling
// tryRegisterModuleStrict inline).
//
// PR4 spec notes:
//
//   - Data-flow ordering: registerInternalModules runs BEFORE
//     registerImages AND registerAssets because both consume
//     MediaIngest.Service (Images) and the explicit search capability
//     passed to the Assets phase.
//     The user-stated orchestrator listing (assets before internal)
//     is illustrative; the executable order is internal → scripts →
//     images → assets → late-bindings → registerCapabilities.
//
//   - Strict-uniqueness invariant: every public_modules + internal_modules
//
//   - registerX call goes through tryRegisterModuleStrict
//     (PR2 codex/registry-strict-uniqueness). The strict path has
//     no shortcut for "we tried the old permissive helper" so the
//     composition-time guarantee is identical to the pre-PR4 code.
//
//   - Cross-step capability state is returned explicitly by
//     registerInternalModules and passed to its consumers. RegistryWiring
//     contains only final graph outputs, not temporal wiring state.
package wiring

import (
	"context"
	"fmt"

	infraassets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"go.uber.org/zap"
)

// Package-level adapter vars originally lived in module_adapters.go;
// consolidated here to keep the composition-root surface in one place.
var (
	// processRunnerAdapter is a package-level adapter for the infrastructure ProcessRunner port.
	// Used by ScraperHandler and other handlers in registry.go that need subprocess execution.
	processRunnerAdapter = infraassets.NewProcessRunnerAdapter()

	// toolCheckerAdapter is a package-level adapter for the infrastructure ToolChecker port.
	// Used by YouTubeClipHandler and system handler to check external tool availability.
	toolCheckerAdapter = infraassets.NewToolCheckerAdapter()

	// dbHealthCheckerAdapter is a package-level adapter for the infrastructure DBHealthChecker port.
	// Used by system handler to check database health.
	dbHealthCheckerAdapter = infraassets.NewDBHealthCheckerAdapter(nil)
)

// RegistryWiring holds the registry and all wired modules.
//
// PR2 (June 2026): removed System, Jobs, Images, Drive, Scraper — those were
// thin Wire wrappers inlined directly in WireRegistry below.
//
// Cross-step capability state is intentionally not stored here. The
// internal-module phase returns registryCrossStepState and the orchestrator
// passes it explicitly to later phases; this struct contains only final graph
// outputs exposed to the server and tests.
type RegistryWiring struct {
	Registry      *module.Registry
	ArtlistSvc    *ArtlistWiring
	YouTubeClip   *YouTubeClipWiring
	MediaIngest   *MediaIngestWiring
	Assets        *AssetsWiring
	StockPipeline *StockPipelineWiring

	// QDRANT-002 + QDRANT-004 separation-of-routes (June 2026):
	// These handlers are constructed by WireRegistry but NOT registered
	// in the public /api registry. They are plumbed through AppDeps and
	// mounted on the /internal/v1 WorkerAuth-protected internalGroup
	// by cmd/server/main.go. The split is enforced by the
	// anti-regression test internal/api/routes_test.go.
	OutboxHandler      RouteRegistrar
	MediasearchHandler RouteRegistrar

	// PG-M2M (Aug 2026): the M2M job surface — POST + GET /:id on the
	// /api/v1/jobs group protected by JobClientAuthMiddleware. Like
	// Outbox/Mediasearch above, it is constructed by WireRegistry but
	// NOT registered in the public /api registry; it is plumbed through
	// AppDeps and mounted on its own group by the server composition.
	// nil when the M2M store is not wired (the group is then skipped).
	M2MJobsHandler RouteRegistrar

	// SearchFanOut is the public accessor for the canonical search
	// aggregator (PR-AGENTE2-READINESS). Populated by WireRegistry
	// from the explicit cross-step capability state.
	SearchFanOut search.SearchFanOut
}

// WireRegistry creates and populates the module registry with all modules.
//
// PR4d-final (June 2026): signature takes (ctx, cfg, log, root). The
// transitional cd parameter was removed. All reads source from root.<bundle>.
//
// PR4 (June 2026, codex/registry-composition-split): this function is
// now an 8-step orchestrator. Each step delegates to a cohesive helper
// in the same package; cross-step capability state flows as an explicit
// short-lived value rather than through RegistryWiring fields.
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

	if err := registerJobsRoute(registry, log, root, wiring); err != nil {
		return nil, fmt.Errorf("wire registry: jobs: %w", err)
	}

	// Admin module — Drive canary and operational readiness endpoints.
	// Uses cfg as AuthSecurityPort for RequireAdminToken middleware.
	if err := registerAdminModule(registry, log, cfg, root); err != nil {
		return nil, fmt.Errorf("wire registry: admin: %w", err)
	}

	// Research cache invalidation — operational admin surface, registered
	// independently of the Drive canary (only needs the media DB).
	if err := registerResearchCacheAdminModule(registry, log, cfg, root); err != nil {
		return nil, fmt.Errorf("wire registry: admin research cache: %w", err)
	}

	// Step 2 — Internal modules (bundle-driven). MUST run before
	// registerImages (consumes MediaIngest.Service) and
	// registerAssets (consumes explicit registryCrossStepState). Wraps
	// registerIdempotencyMiddleware +
	// registerSearchBackend + registerArtlist + registerYouTubeClip +
	// registerMediaIngest + registerScraper +
	// registerStockPipeline in the canonical DAG order.
	crossStep, err := registerInternalModules(ctx, registry, log, cfg, root, wiring)
	if err != nil {
		return nil, fmt.Errorf("wire registry: internal-modules: %w", err)
	}

	// Expose search fanout for readiness gates (PR-AGENTE2-READINESS).
	wiring.SearchFanOut = crossStep.SearchFanOut

	// Step 3 — Scripts: wireScriptFlow orchestration + ScriptHistory module.
	if err := registerScripts(ctx, registry, log, cfg, root, wiring.ArtlistSvc); err != nil {
		return nil, fmt.Errorf("wire registry: scripts: %w", err)
	}

	// Step 4 — Images: single route module; consumes MediaIngest.Service.
	if err := registerImages(registry, log, cfg, root, wiring); err != nil {
		return nil, fmt.Errorf("wire registry: images: %w", err)
	}

	// Step 5 — Assets: bundle-driven. Consumes the explicit cross-step
	// capability state; constructs
	// maintenanceSvc locally + calls WireAssets + performs the
	// DeletionService backfill on the maintenance service.
	if err := registerAssets(registry, log, cfg, root, wiring, crossStep); err != nil {
		return nil, fmt.Errorf("wire registry: assets: %w", err)
	}

	// Step 5a — Operator Console: admin-facing read-only API for the
	// operator console binary. Registered after assets (needs detail.Service)
	// and before late bindings (no cross-step state dependency).
	if err := registerOperatorAdminAPI(registry, log, cfg, root); err != nil {
		return nil, fmt.Errorf("wire registry: operator-admin-api: %w", err)
	}

	// Step 5b — Admin Console: schema-driven entity registry and
	// Database Explorer API surface. Registered after operator console
	// and before late bindings.
	if err := registerAdminConsoleAPI(registry, log, cfg, root); err != nil {
		return nil, fmt.Errorf("wire registry: adminconsole-api: %w", err)
	}

	// Step 5c — MediaMemory: canonical resolve + bindings + feedback
	// surface. Consumes the explicit search capability (Step 2) + root.DB +
	// root.Outbox for the binding dispatcher.
	if err := registerMediaMemory(registry, log, root, crossStep.SearchFanOut); err != nil {
		return nil, fmt.Errorf("wire registry: mediamemory: %w", err)
	}

	// Step 6 — Late bindings prepares internal handlers and the
	// script-assets route module. It performs no provider registration.
	preparedCapabilities, lbErr := applyLateBindings(registry, log, root, wiring, crossStep)
	if lbErr != nil {
		return nil, fmt.Errorf("wire registry: late-bindings: %w", lbErr)
	}

	// Step 7 — registerCapabilities publishes prepared HTTP modules and
	// validates the graph; the provider registry was already bootstrapped
	// and frozen before search composition.
	//
	// HTTPModules is forward-only here — today empty because per-step
	// registerX functions register inline during Steps 2-5 (the
	// strict-uniqueness gate is preserved by the canonical relocation
	// of tryRegisterModuleStrict into capability_registry.go).
	var providerReg *providers.Registry
	if root.Search != nil {
		providerReg = root.Search.ProviderRegistry
	}
	if err := registerCapabilities(registry, providerReg, CapabilityDeps{
		Providers: preparedCapabilities,
	}); err != nil {
		return nil, fmt.Errorf("wire registry: register-capabilities: %w", err)
	}

	// Step 8 — P0 Commit 3 (July 2026) C3 runtime-graph validation.
	// Populate the C3 MutableJobRegistry with the 5 canonical
	// JobDefinitions + placeholder handler bindings, freeze, and run
	// the §4.5 validator. If ANY check fails (creator-enabled without
	// handler, RequireManifest=true without ResultCodec, empty codec
	// SchemaVersion, missing RequiredCapabilities, etc.), the
	// validator surfaces all findings via errors.Join and WireRegistry
	// aborts the server boot — fail-closed posture.
	//
	// The placeholder handlers are deliberate: this commit (C3)
	// introduces the registry + validator contracts only. C4
	// (Dispatcher Enqueue through def.PayloadCodec) replaces the
	// placeholders with real dispatch routing. Validation locks the
	// SHAPE (HasHandler=true) so C4 can focus on the dispatch path
	// without re-checking the registry.
	if err := c3ValidateRuntimeGraph(); err != nil {
		return nil, fmt.Errorf("wire registry: c3 startup validation failed: %w", err)
	}

	return wiring, nil
}
