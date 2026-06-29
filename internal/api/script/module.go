// Package script — module.go: the single canonical Build entrypoint for
// the ScriptFlow HTTP capability (the script-generation HTTP surface at
// /api/script/*).
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// The returned Descriptor is complete: missing mandatory dependencies
// return an error during composition; the capability does not create
// partially-initialized services. Once Build returns, the descriptor is
// ready to be registered into the api.Registry by the composition root.
//
// This file is part of Blocco C1-Step 14 (June 2026): every capability
// in `internal/api/**` and `internal/application/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/wire_script.go::wireScriptFlow` and threads the
// returned Descriptor into `tryRegisterModule(registry, log, desc, ...)`
// (Module name "script-flow" + prefix "/script" → final URL
// /api/script/*).
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5),
// `stock/module.go` (C1-Step 6), `voiceover/module.go` (C1-Step 7),
// `soundeffect/module.go` (C1-Step 8), `register/module.go`
// (C1-Step 9), `diagnostics/module.go` (C1-Step 10),
// `search/module.go` (C1-Step 11), `jobs/module.go` (C1-Step 13).
//
// UNIQUE TO SCRIPT: the ScriptFlowHandler is the MOST COMPLEX handler
// in the api/ tree (8 routes, 20+ field ScriptFlowDeps bag, 6
// non-HTTP methods: EnableAuth/AdminToken/GetVoiceoverService/
// GetGroupsResolver/ResolveDriveFolderID/MaybeCreateGoogleDoc).
// Despite the 6 non-HTTP methods, code-search at HEAD confirms ZERO
// external callers of these methods (`rg scriptFlow.EnableAuth` etc.
// returns 0 matches). The Descriptor surface is therefore the
// smallest in the tree today (tied with stock / voiceover /
// soundeffect / register / diagnostics / search / jobs) — only
// `Module` field, no `Handler` field. The 6 non-HTTP methods are
// scaffolding for the AdminTokenProvider port (EnableAuth/AdminToken
// are called from `registerJobRoutes` internally via RequireAdminToken
// middleware; the other 4 are future-facing accessor methods that no
// caller in the codebase currently uses). The Handler stays the
// internal worker captured by the Module closure; no caller reads a
// raw *ScriptFlowHandler from outside the package.
//
// The pre-Step-14 wire-up is in `internal/app/wire_script.go::wireScriptFlow`:
// the orchestrator constructs the handler via
// `scriptapi.NewScriptFlowHandler(scriptapi.ScriptFlowDeps{...})` and
// registers it via `module.NewRouteModule("script-flow", enabledFn,
// "/script", handler, log) + tryRegisterModule(registry, log, mod)`.
// Blocco C1-Step 14 replaces both with a single
// `scriptapi.Build(scriptapi.Dependencies{...})` + type-assertion call
// (fail-closed with the same `if !ok || sd == nil` check as the 11
// C1 precedents).
package script

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptdto "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Mirrors the
// existing ScriptFlowDeps bag (20+ fields) + the Build-time fields
// (EnabledFunc + ModuleOpts + Logger) the 11 C1 precedents require.
//
// MANDATORY fields return an error when nil; OPTIONAL fields fall
// through to the handler's existing nil-tolerance (the handler
// itself has per-route nil-guards — e.g. the /jobs/:id route
// returns 503 on nil JobsService; the /clips/search endpoint returns
// 503 on nil ClipsSearcher; the MaybeCreateGoogleDoc method
// short-circuits on nil DocumentCreator — preserving the pre-Step-14
// nil-tolerance). Logger nil → zap.NewNop() (composition-root-friendly
// default).
//
// Only ONE field is truly mandatory: Engine. The ScriptFlowHandler
// dereferences engine unconditionally in the Generate route + all
// 4 legacy routes. A nil engine would NPE at first request; Build
// is fail-closed on this one field to catch the most common wiring
// defect at startup.
type Dependencies struct {
	// ── Handler bag (mirrors ScriptFlowDeps) ─────────────────────────
	// Engine is the canonical script-generation engine
	// (root.AI.ScriptEngine). MANDATORY — Build returns an error
	// when nil. The ScriptFlowHandler dereferences engine
	// unconditionally in the Generate route + all 4 legacy routes.
	// A nil engine would NPE at first request; fail at startup
	// instead.
	Engine *usecase.Engine

	// Section, CacheEviction, Image, Realtime, Association,
	// Voiceover, AssetTree, ClipSourceBuilder, MediaCurator,
	// Harvest, ScriptsRepo, Memory — all OPTIONAL. The handler
	// nil-checks each at request time; the routes that need them
	// return 503-equivalent sentinels on nil (preserves the
	// pre-Step-14 nil-tolerance).
	Section       *usecase.SectionRegenerator
	CacheEviction *usecase.CacheEvictionUseCase
	Image         *images.Service
	// Wave 16 (June 2026): typed ports — replace the `interface{}`
	// carrier for the script-side realtime + association consumers
	// (packages removed in commit d61068b3; fields stay typed-nil).
	Realtime    usecase.RealtimeSearchService
	Association usecase.AssocSearchService
	Voiceover   *voiceover.Service
	AssetTree   *assettree.Service

	ClipSourceBuilder *usecase.ClipSourceBuilder
	MediaCurator      *scriptdto.MediaCurator
	Harvest           AutoHarvestService

	ScriptsRepo adapters.ScriptRepository
	Memory      *adapters.Service
	Jobs        jobservice.Service
	// Issue 4 (June 2026, P1): optional canonical job-type registry
	// used by EnqueueGenerationJob to source MaxRetries from
	// registry.DefaultMaxRetries(jType). Optional — nil preserves
	// the legacy hard-coded 3-retry fallback path through the
	// JobsService. Composition root will pass appjobs.Compose().
	Registry *appjobs.Registry

	// PR-FIX (June 2026): optional clip-name searcher for
	// GET /script/clips/search?q= discovery endpoint.
	// Nil → endpoint returns 503.
	ClipsSearcher ClipSearcher

	AdminToken            string
	DriveFolderClient     DriveFolderClient
	DocumentCreator       DocumentCreator
	DriveScriptsGenFolder string
	ClipServices          usecase.ClipServices // pre-built in wire_script.go

	// ── Build-time fields (mirrors the 11 C1 precedents) ───────────

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The script capability gates
	// on any script feature flag (anyScriptFeatureEnabled) —
	// the composition root wires that closure here.
	// MANDATORY — Build returns an error when nil (so this
	// package stays free of platform/config imports).
	EnabledFunc func() bool

	// ModuleOpts are variadic `api.RouteModuleOption` decorators
	// (typically `api.WithMiddleware(...)`) applied to the
	// RouteModule at Build time. OPTIONAL — nil produces a plain
	// RouteModule.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger. nil →
	// zap.NewNop() (composition-root-friendly default).
	Logger *zap.Logger
}

// ScriptDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module field
// (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods.
//
// UNIQUE TO SCRIPT: the Descriptor DOES expose the Handler
// (defensive design per the code-reviewer-minimax-m3 review on
// 2026-06-29, despite the 6 non-HTTP methods having ZERO external
// callers at HEAD). The clips C1-Step 5 precedent exposed Handler
// for the same defensive reason (1 caller at the time, but the
// field is the safe choice for future wiring — the cost is one
// extra field + the type-assertion access pattern, and it
// future-proofs the contract against the same kind of "zero
// callers today, one caller tomorrow" drift that clips already
// encountered). The 6 non-HTTP methods on the ScriptFlowHandler
// (EnableAuth/AdminToken/GetVoiceoverService/GetGroupsResolver/
// ResolveDriveFolderID/MaybeCreateGoogleDoc) are scaffolding for
// the AdminTokenProvider port + future-facing accessor methods.
// The Handler is reachable via the explicit `Handler` field for
// any future non-HTTP consumer; the Module surface remains the
// canonical HTTP path.
type ScriptDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// tryRegisterModule.
	Module api.Module

	// Handler is the raw orchestrator. Exposed defensively
	// for any future non-HTTP consumer that needs the
	// 6 non-HTTP methods (EnableAuth/AdminToken for the
	// AdminTokenProvider port; GetVoiceoverService /
	// GetGroupsResolver / ResolveDriveFolderID /
	// MaybeCreateGoogleDoc for future wiring). Matches the
	// clips C1-Step 5 precedent. Future commits may move
	// these accessors to typed ports and drop the field;
	// the current shape is the minimal-fabrication choice
	// for Step 14.
	Handler *ScriptFlowHandler
}

// Name returns the module name ("script-flow"). The pre-Step-14
// script routes were registered on a module named "script-flow"
// with prefix "/script" → final URL /api/script/*. The new Build
// contract preserves the Module name + prefix verbatim
// (zero-change-contract).
func (d *ScriptDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *ScriptDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure.
func (d *ScriptDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the ScriptFlow HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop(). ModuleOpts nil → no decorators.
//
// The returned Descriptor carries the Module (routes) + the
// Handler (defensive — for any future non-HTTP consumer that
// needs the 6 non-HTTP methods). The HTTP Handler is constructed
// here and captured by the Module's RegisterRoutes closure. The
// Handler is also exposed via the Descriptor.Handler field
// (defensive design per the code-reviewer-minimax-m3 review on
// 2026-06-29, matching the clips C1-Step 5 precedent).
//
// NewScriptFlowHandler is preserved for direct callers that bypass
// Build (e.g. the test fixtures at handler_test.go,
// handler_idempotency_test.go, handler_legacy_adapters_test.go,
// handler_legacy_int_stock_test.go).
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation.
	//
	// Engine is the canonical script-generation engine
	// (root.AI.ScriptEngine). Build is fail-closed on nil because
	// every script route (generate, regenerate, etc.) routes
	// through the engine. A nil engine would NPE at first
	// request; fail at startup instead.
	if deps.Engine == nil {
		return nil, fmt.Errorf("script.Build: Engine is required (composition root must pre-construct *usecase.Engine via BuildAIBundle; the api/ layer never builds it)")
	}
	// EnabledFunc is the canonical feature-gate closure
	// (typically `func() bool { return anyScriptFeatureEnabled(cfg) }`).
	// Build is fail-closed on nil so this package stays free
	// of platform/config imports.
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("script.Build: EnabledFunc is required (composition root must wire anyScriptFeatureEnabled closure — so this package stays free of platform/config imports)")
	}
	// Jobs + DriveFolderClient are OPTIONAL (matching the
	// handler's per-route nil-tolerance — the /jobs/:id route
	// returns 503 on nil JobsService; the /regenerate route's
	// resolveDriveFolderID would NPE on nil but the existing
	// pre-Step-14 behavior is "503-equivalent runtime error",
	// not "startup failure"). Build is lenient on these two
	// to match the handler's nil-tolerance.

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewScriptFlowHandler has
	// no fail-closed checks (preserves the pre-Step-14 behavior
	// for direct callers that bypass Build); Build's checks above
	// are the new defensive layer.
	handler := NewScriptFlowHandler(ScriptFlowDeps{
		Engine:                deps.Engine,
		Section:               deps.Section,
		CacheEviction:         deps.CacheEviction,
		Image:                 deps.Image,
		Realtime:              deps.Realtime,
		Association:           deps.Association,
		Voiceover:             deps.Voiceover,
		AssetTree:             deps.AssetTree,
		ClipSourceBuilder:     deps.ClipSourceBuilder,
		MediaCurator:          deps.MediaCurator,
		Harvest:               deps.Harvest,
		ScriptsRepo:           deps.ScriptsRepo,
		Memory:                deps.Memory,
		Jobs:                  deps.Jobs,
		Registry:              deps.Registry,
		ClipsSearcher:         deps.ClipsSearcher,
		AdminToken:            deps.AdminToken,
		DriveFolderClient:     deps.DriveFolderClient,
		DocumentCreator:       deps.DocumentCreator,
		DriveScriptsGenFolder: deps.DriveScriptsGenFolder,
		ClipServices:          deps.ClipServices,
		Log:                   log,
	})

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here, not exposed to the composition
	// root via the Module surface.
	//
	// Module name "script-flow" + prefix "/script" preserves
	// the pre-Step-14 wire shape: the 8 routes (POST /generate,
	// 4 legacy POST /generate-from-clips / /generate-with-images
	// / /generate-batch / /curate, GET /clips/search,
	// /jobs/:id, POST /:id/sections/:section_id/regenerate,
	// POST /cache/evict) mount under the /api/script/* prefix.
	// The Module name is the canonical identifier (used for
	// logging + EnabledFunc wiring).
	mod := api.NewRouteModule(
		"script-flow",
		deps.EnabledFunc,
		"/script",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &ScriptDescriptor{
		Module:  mod,
		Handler: handler,
	}, nil
}
