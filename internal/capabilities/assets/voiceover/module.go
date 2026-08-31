// Package voiceover — module.go: the single canonical Build entrypoint
// for the Voiceover HTTP capability.
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
// This file is part of Blocco C1-Step 7 (June 2026): every capability
// in `internal/capabilities/**` and `internal/capabilities/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/module_media.go::WireAssets` and threads the returned
// Descriptor into `assetsapi.Dependencies.Voiceover` (route module
// that mounts /media/voiceover).
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5),
// `stock/module.go` (C1-Step 6).
//
// UNIQUE TO VOICEOVER (post Blocco 4 EXPAND slim, June 2026):
//   - the package owns the SMALLEST handler in the assets tree today
//     (1 route: POST /generate, no sub-handlers, no mirror fields).
//   - the canonical surface is async-only — the /generate handler
//     enqueues a voiceover.generate job and returns 202 Accepted.
//     The legacy /generate-with-group /batch /promo /sync /groups
//     routes were retired PR-VOICEOVER-RECOVERY (V1..V7, Wave 21).
//   - the handler is THIN transport — wire-shape / payload translation
//     is delegated to types.go::GenerateVoiceoversRequest (Pattern 6
//     of AGENTS.md).
//   - NewHandler PANICS on nil jobsSvc (composition-root misconfig
//     surfaces at startup). Build is fail-closed on nil Jobs (returns
//     an error from Build) so the panic-in-NewHandler path becomes
//     unreachable from Build's caller. The panic is preserved for
//     any direct caller of NewHandler that bypasses Build (defensive
//     belt-and-suspenders).
//
// The Build contract therefore mirrors the stock shape exactly (no
// `Handler` / `Service` field on the Descriptor; only `Module` +
// forwarder methods). There is no non-HTTP consumer of the voiceover
// handler in the codebase (the only route is /generate, only
// reachable via HTTP).
package voiceover

import (
	"fmt"

	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Voiceover's
// handler is the smallest in the assets tree (1 jobs service + 1
// logger), so the Dependency surface is correspondingly narrow.
//
// Mandatory fields return an error when nil; optional fields fall
// through to the handler's existing nil-tolerance (Handler.NewHandler
// already defaults a nil log to zap.NewNop()).
//
// Logger nil → zap.NewNop() (composition-root-friendly default).
type Dependencies struct {
	// Jobs is the canonical jobs.Service (facade) used by the
	// /generate enqueue path. MANDATORY — Build returns an error
	// when nil. Note: NewHandler PANICS on nil jobsSvc (a
	// composition-root misconfig surfaces at startup); the panic
	// is unreachable from Build's caller because Build is
	// fail-closed on nil Jobs BEFORE calling NewHandler.
	Jobs jobs.Service

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The voiceover capability has
	// no feature flag in production (always on) — the composition
	// root wires `func() bool { return true }` (or any
	// availability-check closure the platform team prefers).
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

// VoiceoverDescriptor is the concrete capability Descriptor
// returned by Build. It satisfies api.Descriptor via the explicit
// Module field (named, not embedded — no method-promotion surprises
// from api.Module) and forwarder methods.
//
// UNIQUE TO VOICEOVER: the Descriptor does NOT expose the handler
// (matches the artlist / stock precedent of dropping the explicit
// Handler field). There is no non-HTTP consumer of the voiceover
// handler in the codebase — /generate is the entire public surface,
// reachable only via HTTP. The handler stays the internal worker
// captured by the Module closure; no caller reads a raw *Handler
// from outside the package.
type VoiceoverDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// assetsapi.Dependencies.Voiceover.
	Module api.Module
}

// ── Module satisfaction (api.Descriptor) ────────────────────────────
// Descriptor does NOT embed Module. The explicit field form does not
// promote Name / Enabled / RegisterRoutes via embedding, so we
// forward them by hand. (Matches the Artlist / YouTube / Clips /
// Stock precedent.)

// Name returns the module name ("voiceover"). Preserved verbatim
// from the pre-Step-7 wiring so the public route prefix
// `/api/media/voiceover/generate` stays unchanged (zero-change-
// contract).
func (d *VoiceoverDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *VoiceoverDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure.
func (d *VoiceoverDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the Voiceover HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop(). ModuleOpts nil → no decorators.
//
// The returned Descriptor carries the Module (routes). The HTTP
// Handler is constructed here and captured by the Module's
// RegisterRoutes closure — no caller (composition root, tests,
// internal services) reads the raw Handler anywhere outside this
// function.
func Build(deps Dependencies) (api.Descriptor, error) {
	// ── Mandatory-shape validation ────────────────────────────────
	if deps.Jobs == nil {
		return nil, fmt.Errorf("voiceover.Build: Jobs is required (the /generate enqueue path is unreachable without the canonical jobs.Service)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("voiceover.Build: EnabledFunc is required (composition root must wire a closure — typically func() bool { return true } — so this package stays free of platform/config imports)")
	}

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewHandler has its own
	// panic-on-nil-jobsSvc defensive check (preserved for direct
	// callers that bypass Build); Build's fail-closed check above
	// makes the panic unreachable from Build's caller.
	handler := NewHandler(deps.Jobs, log)

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here, not exposed to the composition
	// root via the Module surface.
	mod := api.NewRouteModule(
		"voiceover",
		deps.EnabledFunc,
		"/voiceover",
		handler,
		log,
		deps.ModuleOpts..., // typically []ModuleOption{api.WithMiddleware(...)}
	)

	return &VoiceoverDescriptor{
		Module: mod,
	}, nil
}
