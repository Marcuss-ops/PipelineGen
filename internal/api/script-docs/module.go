// Package scriptdocs — module.go: the canonical Build entrypoint
// for the /api/script-docs/* HTTP capability.
//
// Capability Standard module.go contract:
//
//	func Build(deps Dependencies) (api.Descriptor, error)
//
// PR-SCRIPT-DOCS-DRIFT-2026-07-08 closure: this module is the SOLE
// canonical owner of the /api/script-docs/* route surface. Composition
// root wires the route module via tryRegisterModuleStrict in
// internal/app/registry_public_modules.go::registerScriptDocs.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - the script-docs HTTP capability is owned by this module
//   - the ReAct typed port is owned by handler.go
//   - the Python ReAct agent implementation is forward-pointer
//     (CUTOVER phase) — the composition root will inject the concrete
//     adapter when the Python bridge wires up.
package scriptdocs

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. Port is the
// canonical ReActPort (Pattern 0 typed port); nil-tolerance is
// enforced at the seam (handler returns 503, not 500).
type Dependencies struct {
	// Port is the canonical ReActPort for the ReAct agent surface.
	// OPTIONAL — nil is allowed; the handler returns 503 with
	// ErrReActNotWired when nil (canonical pre-fail-closed
	// posture for optional modules). The composition root passes
	// nil today; a future CUTOVER injects a concrete adapter.
	Port ReActPort

	// EnabledFunc is the closure that decides whether the module's
	// routes are mounted. MANDATORY — Build returns an error when
	// nil. The canonical wiring is `func() bool { return cfg.Features.ScriptDocsEnabled }`.
	EnabledFunc func() bool

	// ModuleOpts are variadic api.RouteModuleOption decorators
	// (typically api.WithMiddleware(...)). OPTIONAL.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger. nil → zap.NewNop().
	Logger *zap.Logger
}

// ScriptDocsDescriptor is the concrete capability Descriptor returned
// by Build. Mirrors the AdminDescriptor / ScriptDescriptor pattern:
//   - Module is the route-only api.Module
//   - the Handler field would be the future typed-port consumer
//     surface (mirrors the pre-PR-script-deps-slim ScriptDescriptor
//     pattern); RETIRED for symmetry with the script module's
//     post-slim shape (godlike/07 minimum-blast-radius — no
//     external callers today).
type ScriptDocsDescriptor struct {
	Module api.Module
}

// Name returns the module name. Canonical: "script-docs" + prefix
// "/script-docs" → final URL /api/script-docs/* (zero-change-contract
// with the future CUTOVER Python bridge wiring).
func (d *ScriptDocsDescriptor) Name() string { return d.Module.Name() }

// Enabled forwards to the Module's closure.
func (d *ScriptDocsDescriptor) Enabled() bool { return d.Module.Enabled() }

// RegisterRoutes forwards to the Module.
func (d *ScriptDocsDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Build composes the ScriptDocs HTTP capability from the typed
// dependencies. Returns a fail-closed error when EnabledFunc is
// nil (mirrors the script.Build contract — mandatory closure so
// this package stays free of platform/config imports).
//
// Port is OPTIONAL (nil is allowed; the handler enforces the
// canonical 503 seam). ModuleOpts nil → no decorators.
// Logger nil → zap.NewNop().
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation. Only EnabledFunc is mandatory
	// (mirrors script.Build). Port is nil-tolerant by design.
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("scriptdocs.Build: EnabledFunc is required (composition root must wire cfg.Features.ScriptDocsEnabled closure)")
	}

	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewHandler is nil-port
	// tolerant; the nil-port guard inside Generate returns 503
	// (godlike/07 fail-closed at the seam).
	handler := NewHandler(deps.Port, log)

	// Construct the route Module (name "script-docs" + prefix
	// "/script-docs" → /api/script-docs/* per zero-change-contract).
	mod := api.NewRouteModule(
		"script-docs",
		deps.EnabledFunc,
		"/script-docs",
		handler,
		log,
		deps.ModuleOpts...,
	)

	return &ScriptDocsDescriptor{Module: mod}, nil
}
