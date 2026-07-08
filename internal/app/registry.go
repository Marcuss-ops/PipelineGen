// Package app — slim composition orchestrator (PR4 mechanical split
// + Blocco C1-Step 2 centralisation, June 2026).
//
// PR4 mechanical split (June 2026): WireRegistry is now a 7-step
// orchestrator that delegates each concern to a dedicated helper
// file in the same package `app`. The original 819-line file has
// been split into 8 files:
//
//   - registry.go                 (this file) — slim orchestrator
//   - RegistryWiring struct + WireRegistry function + package doc.
//   - capability_registry.go      registerCapabilities +
//     registerHTTPModules + registerProviders + registerJobs +
//     tryRegisterModule + tryRegisterModuleStrict + strictOption +
//     WithRegistrationPoint + collectRegPoint (the canonical
//     single composition point for typed-punctuated
//     Registry.Register mutations — Blocco C1-Step 2).
//   - registry_public_modules.go  registerSystem + registerJobs +
//     registerImages + registerScriptHistory + registerUtility +
//     registerRealtime + registerGenerationCapability +
//     registerChannelsCapability + registerSearchQueriesCapability
//     (thin route modules exposed via /api/* paths).
//   - registry_internal_modules.go registerInternalModules wrapper
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
//   - registry_late_bindings.go   applyLateBindings (returns
//     []TrackedProviderEntry to feed capability_registry.go's
//     registerProviders step).
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
//   - Step 6 (applyLateBindings) now RETURNS []TrackedProviderEntry
//     instead of incurring providers.Registry mutation inline. The
//     inline calls were the gate violation: typed-punctuated
//     provider Registry mutation outside the canonical point.
//
//   - Step 7 is registerCapabilities — the canonical single
//     composition point — which routes HTTP modules (already
//     registered inline during Steps 2-5, see Note (a) below)
//     through registerHTTPModules' strict-uniqueness gate AND
//     registers the providers slice AND freezes the
//     providers.Registry. The pre-Step-2 freezeRegistries step
//     is gone (the canonical Freeze lives inside registerProviders
//     inside capability_registry.go — Reviewer Q8 invariant
//     preserved by plumbing the Freeze as the LAST mutation
//     of registerCapabilities).
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
//     wiring.MediaIngest.Service (Images) and wiring.searchFanOut
//
//   - wiring.searchBackends + wiring.idempotencyHandler (Assets).
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
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/search"
	jobpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
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

	// SearchFanOut is the public accessor for the canonical search
	// aggregator (PR-AGENTE2-READINESS). Populated by WireRegistry
	// after registerInternalModules sets the unexported field.
	SearchFanOut search.SearchFanOut
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

	if err := registerJobsRoute(registry, log, root); err != nil {
		return nil, fmt.Errorf("wire registry: jobs: %w", err)
	}

	// Admin module — Drive canary and operational readiness endpoints.
	// Uses cfg as AuthSecurityPort for RequireAdminToken middleware.
	if err := registerAdminModule(registry, log, cfg, root); err != nil {
		return nil, fmt.Errorf("wire registry: admin: %w", err)
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

	// Expose search fanout for readiness gates (PR-AGENTE2-READINESS).
	wiring.SearchFanOut = wiring.searchFanOut

	// Step 3 — Scripts: wireScriptFlow orchestration + ScriptHistory module.
	if err := registerScripts(ctx, registry, log, cfg, root); err != nil {
		return nil, fmt.Errorf("wire registry: scripts: %w", err)
	}

	// Step 3a — ScriptDocs: PR-SCRIPT-DOCS-DRIFT-2026-07-08 closure.
	// Mounts /api/script-docs/* (POST /generate today; future endpoints
	// for /reset + /state land in lockstep with ReActPort surface
	// extensions). Gated on cfg.Features.ScriptDocsEnabled. ReAct
	// typed port is nil-tolerant; composition root passes nil today.
	if err := registerScriptDocs(registry, log, cfg); err != nil {
		return nil, fmt.Errorf("wire registry: script-docs: %w", err)
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

	// Step 6 — Late bindings: builds the QDRANT-002 outbox handler +
	// QDRANT-004 mediasearch handler + COLLECTS provider registration
	// entries (artlist + youtube + stock); internally still publishes
	// the script_assets capability (HTTP module via the canonical
	// tryRegisterModuleStrict + ScriptAssetsDescriptor.RegisterProviders
	// slot — gate-safe because the slot is a descriptor-level method,
	// NOT a typed providers.Registry .Register call). Returns the
	// provider entry slice for Step 7 to register+freeze via the
	// canonical composition point.
	providerEntries, lbErr := applyLateBindings(registry, log, root, wiring)
	if lbErr != nil {
		return nil, fmt.Errorf("wire registry: late-bindings: %w", lbErr)
	}

	// Step 7 — registerCapabilities (Blocco C1-Step 2, June 2026):
	// the canonical single composition point for typed-prefix
	// mutated-by-Register style calls on the THREE canonical
	// registries. capability_registry_gate_test.go enforces the
	// invariant: NO production file outside capability_registry.go
	// may contain a typed-prefix Registry.Register(...) literal.
	// ProviderRegistry.Freeze() lands inside registerProviders as
	// the absolute last mutation (Reviewer Q8 invariant preserved
	// from Wave 14 close-out).
	//
	// HTTPModules is forward-only here — today empty because per-step
	// registerX functions register inline during Steps 2-5 (the
	// strict-uniqueness gate is preserved by the canonical relocation
	// of tryRegisterModuleStrict into capability_registry.go).
	var providerReg *providers.Registry
	if root.Search != nil {
		providerReg = root.Search.ProviderRegistry
	}
	if err := registerCapabilities(registry, nil, providerReg, CapabilityDeps{
		Providers: providerEntries,
	}); err != nil {
		return nil, fmt.Errorf("wire registry: register-capabilities: %w", err)
	}

	// Step 8 — P0 Commit 3 (July 2026) C3 runtime-graph validation.
	// Populate the C3 MutableJobRegistry with the 4 canonical
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

// ── Step 8 helper (P0 Commit 3, July 2026) ───────────────────────────

// c3ValidateRuntimeGraph constructs the C3 MutableJobRegistry,
// populates it with the 4 canonical JobDefinitions wired with
// placeholder JobHandlerFunc bindings, freezes the registry, and
// runs the §4.5 validator. Returns nil on a clean graph, or an
// error wrapping ErrInvalidRuntimeGraph when ANY check fails.
//
// Why placeholders? C3 ships registry + validator contracts only.
// The handlers' bodies are NOT yet routed through def.PayloadCodec —
// that is the explicit scope of C4 (Commit 4). For C3's purposes,
// HasHandler=true (post-BindHandler) is the only invariant the
// validator checks; full payload/result dispatch lands in C4.
//
// The wire-up target is job.StartupValidationInput with Workflow =
// {TypeScriptGenerate, TypeImagesGenerate, TypeDocumentGenerate,
// TypeAssetsResolve} — the canonical 4-family execution graph.
//
// A future contributor adding a 5th canonical job family must:
//  1. Append the literal to internal/domain/job/canonical_definitions.go.
//  2. Append the type constant to internal/domain/job/job.go.
//  3. Update workflowRefs below.
//  4. Update the per-family round-trip test in registry_codec_completeness_test.go.
//
// The compile-time assertions in startup_validator_test.go lock the
// canonical 4 literal references — adding a 5th and forgetting step
// (3) surfaces as a test failure rather than a runtime mismatch.
func c3ValidateRuntimeGraph() error {
	mutableReg := jobpkg.NewMutableJobRegistry()
	for _, def := range jobpkg.CanonicalJobDefinitions {
		if err := mutableReg.RegisterDefinition(def); err != nil {
			return fmt.Errorf("register %s: %w", def.Type, err)
		}
		// Placeholder JobHandlerFunc — replaced by C4 dispatch routing.
		// Read by the C3 validator's HasHandler check only; not invoked
		// at runtime until C4 wires def.PayloadCodec -> dispatcher.
		placeholder := func(_ context.Context, _ *jobpkg.Job, _ any) (any, error) {
			return nil, nil
		}
		if err := mutableReg.BindHandler(def.Type, placeholder); err != nil {
			return fmt.Errorf("bind handler %s: %w", def.Type, err)
		}
	}
	compiled, err := mutableReg.Freeze()
	if err != nil {
		return fmt.Errorf("freeze: %w", err)
	}
	workflowRefs := []string{
		jobpkg.TypeScriptGenerate,
		jobpkg.TypeImagesGenerate,
		jobpkg.TypeDocumentGenerate,
		jobpkg.TypeAssetsResolve,
	}
	validator := jobpkg.DefaultStartupValidator{}
	if err := validator.ValidateRuntimeGraph(jobpkg.StartupValidationInput{
		Registry: compiled,
		Workflow: workflowRefs,
	}); err != nil {
		return err
	}
	return nil
}
