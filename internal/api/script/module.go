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
// PR-script-deps-slim (July 2026, P1): Dependencies was a 22+2-field
// bag with 12 ignored fields + a mandatory Engine check that was
// never dereferenced (the api/ layer never reads Engine — the
// pre-Step-14 Build fail-closed was defensive-only). Slim form
// below: 3 small dep bags (Generate / Shorts / Jobs) + the
// ClipsSearcher + AdminToken + 3 build-time fields (7 total).
// ScriptDescriptor.Handler field is RETIRED (defensive
// fake-availability — the 6 non-HTTP methods have ZERO external
// callers at HEAD).
package script

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. After
// PR-script-deps-slim the bag holds 3 small dep bags (Generate /
// Shorts / Jobs) + ClipsSearcher + AdminToken + 3 build-time
// fields. Only EnabledFunc is mandatory (Build fail-closes on
// nil — the pre-Step-14 Engine check is RETIRED because the api/
// layer never dereferences Engine; godlike/07 minimum-blast-radius
// means the defensive Engine check is dead wire).
type Dependencies struct {
	// ── Slim handler bag (was 22 fields, now 5) ─────────────────────
	// Generate is the dep bag for POST /generate.
	Generate GenerateDeps
	// Shorts is the dep bag for /shorts/*.
	Shorts ShortsDeps
	// Jobs is the dep bag for /jobs/:id.
	Jobs JobsDeps

	// ClipsSearcher is the clip-name searcher for the
	// GET /script/clips/search?q= discovery endpoint. Nil →
	// endpoint returns 503.
	ClipsSearcher ClipSearcher

	// AdminToken is the auth secret consumed by EnableAuth +
	// AdminToken (AdminTokenProvider interface satisfaction).
	AdminToken string

	// ── Build-time fields (mirrors the 11 C1 precedents) ───────────

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The script capability gates
	// on any script feature flag (anyScriptFeatureEnabled) — the
	// composition root wires that closure here. MANDATORY — Build
	// returns an error when nil (so this package stays free of
	// platform/config imports).
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
// by Build. The Handler field is RETIRED in PR-script-deps-slim
// (was defensive fake-availability — the 6 non-HTTP handler
// methods had ZERO external callers at HEAD; the 4 facade
// delegator methods on ScriptFlowHandler are also RETIRED in
// lockstep). Future typed-port consumers (AdminTokenProvider etc.)
// consume Module directly.
type ScriptDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// tryRegisterModule.
	Module api.Module
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

// RegisterRoutes forwards to the Module.
func (d *ScriptDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the ScriptFlow HTTP capability from the slim
// 8-field Dependencies. Returns a fail-closed error when
// EnabledFunc is nil. The pre-Step-14 Engine mandatory check is
// RETIRED (godlike/07 minimum-blast-radius: the api/ layer never
// dereferences Engine; Build's defensive check was dead wire).
// Logger nil → zap.NewNop(). ModuleOpts nil → no decorators.
// NewScriptFlowHandler is preserved for direct callers that bypass
// Build (the test fixtures).
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation. Only EnabledFunc is mandatory
	// after PR-script-deps-slim (Engine is dropped — the api/
	// layer never reads it; the pre-Step-14 Build check was
	// defensive dead wire). The remaining fields are nil-tolerant
	// (the handler's per-route nil-guards return 503-equivalent
	// sentinels at runtime — preserves the pre-Build
	// nil-tolerance).
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
	// for direct callers that bypass Build); Build's check above
	// is the new defensive layer.
	handler := NewScriptFlowHandler(ScriptFlowDeps{
		Generate:      deps.Generate,
		Shorts:        deps.Shorts,
		Jobs:          deps.Jobs,
		ClipsSearcher: deps.ClipsSearcher,
		AdminToken:    deps.AdminToken,
		// Caps is read directly from deps.Generate.Caps inside
		// NewScriptFlowHandler (godlike/06 SSOT one canonical owner
		// per fact); it is NOT forwarded as a separate top-level
		// field on ScriptFlowDeps to avoid the drift hazard of two
		// (PR-COMMIT3: PreflightCaps removed; the per-ScriptFlowHandler
		// independence surface is empty post-removal.)
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
		Module: mod,
	}, nil
}

// Compile-time guard: appjobs.Registry is a non-nil sentinel
// that the slim ScriptFlowDeps references transitively. This is
// the canonical Pattern 0 build-failure lock per AGENTS.md.
var _ *appjobs.Registry = nil
