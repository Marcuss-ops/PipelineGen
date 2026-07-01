// Package script — module.go: the canonical Build entrypoint for the
// ScriptFlow HTTP capability (/api/script/*).
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// The returned Descriptor is complete: missing mandatory dependencies
// return an error during composition; Logger nil → zap.NewNop().
//
// Composition site: `internal/app/wire_script.go::wireScriptFlow`
// calls `script.Build(...)` and threads the returned Descriptor into
// `tryRegisterModule` with Module name "script-flow" + prefix
// "/script" → /api/script/* (zero-change-contract).
//
// Pattern parity with the 11 C1 sibling conversions (artlist /
// youtube / clips / stock / voiceover / soundeffect / register /
// diagnostics / search / jobs / storage). The script capability is
// the most complex handler in the api/ tree today (8 routes; ~22-field
// Dependencies bag); only Engine + EnabledFunc are mandatory at Build
// time — the remaining 18 fields are nil-tolerant (the handler's
// per-route nil-guards return 503-equivalent sentinels).
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

// Dependencies is the typed narrow input to Build. Engine + EnabledFunc
// are mandatory (Build fail-closes on nil — the handler dereferences
// Engine unconditionally and the package stays free of
// platform/config imports). The remaining fields are nil-tolerant
// at request time (per-route nil-guards return 503-equivalent
// sentinels). Logger nil → zap.NewNop().
type Dependencies struct {
	// ── Handler bag (mirrors ScriptFlowDeps) ─────────────────────────
	// Engine is the canonical script-generation engine
	// (root.AI.ScriptEngine). MANDATORY.
	Engine *usecase.Engine

	// Remaining fields are nil-tolerant at request time —
	// per-route nil-guards in the handler return 503-equivalent
	// sentinels matching the pre-Build nil-tolerance.
	Section           *usecase.SectionRegenerator
	CacheEviction     *usecase.CacheEvictionUseCase
	Image             *images.Service
	Realtime          usecase.RealtimeSearchService
	Association       usecase.AssocSearchService
	Voiceover         *voiceover.Service
	AssetTree         *assettree.Service
	ClipSourceBuilder *usecase.ClipSourceBuilder
	MediaCurator      *scriptdto.MediaCurator
	Harvest           AutoHarvestService
	ScriptsRepo       adapters.ScriptRepository
	// Commit H Phase 2 (June 2026): Memory field dropped.
	Jobs              jobservice.Service
	// Registry is the canonical job-type registry (appjobs.Compose())
	// used by EnqueueGenerationJob to source MaxRetries via
	// registry.DefaultMaxRetries(jType). Nil falls back to the
	// legacy hard-coded 3-retry path.
	Registry *appjobs.Registry
	// ClipsSearcher is the clip-name searcher for the
	// GET /script/clips/search?q= discovery endpoint. Nil →
	// endpoint returns 503.
	ClipsSearcher ClipSearcher

	AdminToken            string
	DriveFolderClient     DriveFolderClient
	DocumentCreator       DocumentCreator
	DriveScriptsGenFolder string
	ClipServices          usecase.ClipServices

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
// by Build. It exposes Module (canonical HTTP path) AND Handler
// (defensive — matches the clips C1-Step 5 precedent,
// future-proofing against the AdminTokenProvider port + 4 future
// accessors). The 6 non-HTTP handler methods (EnableAuth /
// AdminToken / GetVoiceoverService / GetGroupsResolver /
// ResolveDriveFolderID / MaybeCreateGoogleDoc) have ZERO external
// callers at HEAD; the Handler field is the minimal-fabrication
// choice (future commits may move these accessors to typed ports
// and drop the field).
type ScriptDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// tryRegisterModule.
	Module api.Module

	// Handler is the raw orchestrator (defensive exposure for
	// future non-HTTP consumers; see ScriptDescriptor godoc).
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
// dependencies. Returns a fail-closed error when Engine or
// EnabledFunc is nil. Logger nil → zap.NewNop(). ModuleOpts nil →
// no decorators. NewScriptFlowHandler is preserved for direct
// callers that bypass Build (the test fixtures).
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation. Only Engine + EnabledFunc
	// are mandatory (Build fail-closes on nil); the remaining
	// fields are nil-tolerant (the handler's per-route
	// nil-guards return 503-equivalent sentinels at runtime —
	// preserves the pre-Build nil-tolerance).
	if deps.Engine == nil {
		return nil, fmt.Errorf("script.Build: Engine is required (composition root must pre-construct *usecase.Engine via BuildAIBundle; the api/ layer never builds it)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("script.Build: EnabledFunc is required (composition root must wire anyScriptFeatureEnabled closure — so this package stays free of platform/config imports)")
	}

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
		// Commit H Phase 2 (June 2026): Memory: deps.Memory dropped.
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

	// Construct the route Module (name "script-flow" + prefix
	// "/script" → /api/script/* per zero-change-contract; the
	// closure inside api.NewRouteModule calls handler.RegisterRoutes(r),
	// capturing the Handler here).
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
