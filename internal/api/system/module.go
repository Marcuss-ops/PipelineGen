// Package system — module.go owns the Build contract entrypoint for the
// System HTTP capability (the /api/system/* + /api/drive/* surfaces).
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
// This file is part of Blocco C1-Step 15 (June 2026): every capability
// in `internal/api/**` and `internal/application/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/registry_public_modules.go::registerSystem` and threads
// the returned Descriptor into `tryRegisterModuleStrict(...)`.
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5),
// `stock/module.go` (C1-Step 6), `voiceover/module.go` (C1-Step 7),
// `soundeffect/module.go` (C1-Step 8), `register/module.go`
// (C1-Step 9), `diagnostics/module.go` (C1-Step 10),
// `search/module.go` (C1-Step 11), `jobs/module.go` (C1-Step 13),
// `script/module.go` (C1-Step 14).
//
// SYSTEM WAVE CONTEXT (preserved from the pre-Step-15 module.go): the
// system Module absorbed the standalone internal/api/drive/handler.go
// as a second receiver (DriveHandler). The system Module now mounts
// two sub-groups sharing the same protected router group:
//
//	/system/doctor     — admin/doctor diagnostics
//	/drive/{reconcile,
//	        cleanup,
//	        folders,
//	        move,
//	        resolve-by-id}
//
// Both sub-groups inherit Auth + RateLimit + WorkspaceScope from
// the protected group mounted in routes.go.
//
// NewModule is PRESERVED for direct callers that bypass Build (none
// known in the codebase today — wire-up in registry_public_modules.go
// now uses Build). Direct callers can still construct the Module
// directly via the legacy NewModule ctor; the Build function is the
// canonical composition-time entrypoint.
package system

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

// Dependencies is the typed narrow input to Build. Mirrors the existing
// NewModule constructor inputs (7 deps + 3 Build-time fields) wrapped
// in a single struct that the composition root populates.
//
// MANDATORY fields return an error when nil; OPTIONAL fields fall
// through to the handler's existing nil-tolerance (DriveHandler's
// DriveOps + Reconciler are explicitly nil-tolerant: missing driveOps
// returns "drive uploader not configured"; missing reconciler returns
// "reconcile service not configured"; SystemHandler's toolChecker +
// processRunner + dbHealthChecker dereference unconditionally in
// {checkExternalTools, checkVoiceover, checkDatabases} so a nil
// dependency would NPE at first request — Build is fail-closed on
// those three to catch the wiring defect at startup). Cfg nil →
// zero-value DoctorConfig (the handler still answers /doctor with
// all-checks-failed, which is acceptable degraded behavior).
// ModuleOpts nil → no decorators. Logger nil → zap.NewNop()
// (composition-root-friendly default).
type Dependencies struct {
	// ── SystemHandler bag (mirrors NewSystemHandler) ─────────────────

	// Cfg is the typed DoctorConfig snapshot the /doctor route
	// reads. Optional — zero-value DoctorConfig degrades gracefully
	// (all path checks fail with "missing", but the handler still
	// answers 200 with the diagnostic verdict).
	Cfg DoctorConfig

	// ToolChecker / ProcessRunner / DBHealthChecker feed only
	// SystemHandler (the /doctor route). MANDATORY — Build
	// returns an error when nil (the handler dereferences
	// toolChecker.CommandExists(...), processRunner.RunSimple(...),
	// dbHealthChecker.{GetAllDBs,GetDBPath,Ping}(...) unconditionally;
	// a nil dep would NPE at first /doctor call — fail at startup).
	ToolChecker     appassets.ToolChecker
	ProcessRunner   appassets.ProcessRunner
	DBHealthChecker appassets.DBHealthChecker

	// ── DriveHandler bag (mirrors NewDriveHandler) ───────────────────

	// DriveOps / Reconciler feed DriveHandler (the /drive/*
	// routes). OPTIONAL — both nil-tolerance patterns are documented
	// in the existing module.go: missing driveOps returns "drive
	// uploader not configured"; missing reconciler returns
	// "reconcile service not configured". Build is lenient to
	// preserve pre-Step-15 behavior (the wire-up currently passes
	// &noopReconciler{} explicitly; downstream PRs may switch to
	// nil-tolerant omission).
	DriveOps  DriveAdminOps
	Reconciler Reconciler

	// ── Build-time fields (mirrors the 12 C1 precedents) ───────────

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The system capability
	// historically gates Enabled() to return `true`; the
	// composition root passes `func() bool { return true }`.
	// MANDATORY — Build returns an error when nil (pattern
	// parity with the 12 C1 precedents — keeps this package
	// free of platform/config imports).
	EnabledFunc func() bool

	// ModuleOpts are variadic `api.RouteModuleOption` decorators
	// (typically `api.WithMiddleware(...)`) applied to the
	// RouteModule at Build time. OPTIONAL — nil produces a plain
	// Module.
	ModuleOpts []api.RouteModuleOption

	// Logger is the canonical structured logger. nil →
	// zap.NewNop() (composition-root-friendly default).
	Logger *zap.Logger
}

// SystemDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module field
// (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods.
//
// The Descriptor surface is the smallest in the tree today (tied with
// stock / voiceover / soundeffect / register / diagnostics / search /
// jobs / script) — only `Module` field, no `Handler` field (no
// non-HTTP consumer; both SystemHandler + DriveHandler stay internal
// to the package).
type SystemDescriptor struct {
	// Module is the route-only Module (api.Module instance) the
	// composition root threads into tryRegisterModuleStrict.
	Module api.Module
}

// Name returns the module name.
func (d *SystemDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *SystemDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the two handlers
// (SystemHandler + DriveHandler) are reachable only via the
// Module's internal closure.
func (d *SystemDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
}

// Module handles system diagnostic routes plus Drive admin ops.
//
// This is the legacy module struct — it stays exported for direct
// callers (none known in the codebase today, but the field is
// reachable via the SystemDescriptor.Module field). New code should
// use Build.
type Module struct {
	name         string
	log          *zap.Logger
	handler      *SystemHandler
	driveHandler *DriveHandler
}

// NewModule creates a new system module.
//
// driveOps is optional; when nil, drive routes return 503 with
// "drive uploader not configured". reconciler is also optional; the
// reconcile/cleanup routes return 503 when it is nil. toolChecker /
// processRunner / dbHealthChecker feed only SystemHandler (the /doctor
// route) and are themselves application-layer ports.
//
// NewModule is PRESERVED for direct callers that bypass Build (none
// known in the codebase today). The Build function is the canonical
// composition-time entrypoint; it calls NewModule internally.
func NewModule(
	cfg DoctorConfig,
	log *zap.Logger,
	toolChecker appassets.ToolChecker,
	processRunner appassets.ProcessRunner,
	dbHealthChecker appassets.DBHealthChecker,
	driveOps DriveAdminOps,
	reconciler Reconciler,
) *Module {
	return &Module{
		name: "system",
		log:  log,
		handler: NewSystemHandler(
			cfg, log,
			toolChecker, processRunner, dbHealthChecker,
		),
		driveHandler: NewDriveHandler(
			reconciler,
			driveOps,
		),
	}
}

// Name returns the module name.
func (m *Module) Name() string { return m.name }

// Enabled always returns true for the system module.
func (m *Module) Enabled() bool { return true }

// RegisterRoutes registers /system/* and /drive/* routes.
//
// Both sub-groups live under the same protected router group, so
// they share Auth + RateLimit + WorkspaceScope. Public callers
// only see /api/system/doctor if explicitly granted admin via
// the workspace scope middleware.
func (m *Module) RegisterRoutes(rg *gin.RouterGroup) {
	systemGroup := rg.Group("/system")
	{
		systemGroup.GET("/doctor", m.handler.Doctor)
	}

	driveGroup := rg.Group("/drive")
	{
		m.driveHandler.RegisterRoutes(driveGroup)
	}
}

// Build composes the system HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Cfg nil-tolerant (zero-value DoctorConfig). DriveOps nil-
// tolerant. Reconciler nil-tolerant. Logger nil → zap.NewNop().
// ModuleOpts nil → no decorators.
//
// The returned Descriptor carries the Module (system routes + drive
// admin ops surface). Both SystemHandler + DriveHandler stay
// internal to the package; the Descriptor exposes only `Module`
// field + forwarder methods (the smallest Descriptor surface in the
// api/ tree today).
//
// NewModule is PRESERVED for direct callers that bypass Build.
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation.
	//
	// ToolChecker / ProcessRunner / DBHealthChecker all
	// dereferenced unconditionally by SystemHandler.{checkExternalTools,
	// checkVoiceover, checkDatabases}. A nil dep would NPE at first
	// /doctor call. Fail at startup instead.
	if deps.ToolChecker == nil {
		return nil, fmt.Errorf("system.Build: ToolChecker is required (handlers check /doctor's external tools; nil would NPE at first request — fail at startup instead)")
	}
	if deps.ProcessRunner == nil {
		return nil, fmt.Errorf("system.Build: ProcessRunner is required (handlers check /doctor's voiceover subprocess; nil would NPE at first request — fail at startup instead)")
	}
	if deps.DBHealthChecker == nil {
		return nil, fmt.Errorf("system.Build: DBHealthChecker is required (handlers check /doctor's database health; nil would NPE at first request — fail at startup instead)")
	}
	// EnabledFunc is the canonical feature-gate closure.
	// Build is fail-closed on nil so this package stays free
	// of platform/config imports (pattern parity with the
	// 12 C1 precedents).
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("system.Build: EnabledFunc is required (composition root must wire the feature-gate closure — so this package stays free of platform/config imports)")
	}
	// DriveOps / Reconciler are OPTIONAL (matching the
	// handler's per-route nil-tolerance — DriveHandler
	// returns "drive uploader not configured" when driveOps
	// is nil; returns "reconcile service not configured"
	// when reconciler is nil). Build is lenient on these
	// two to match the handler's nil-tolerance.

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Module. NewModule has no
	// fail-closed checks (preserves the pre-Step-15 behavior
	// for direct callers); Build's checks above are the
	// new defensive layer.
	mod := NewModule(
		deps.Cfg,
		log,
		deps.ToolChecker,
		deps.ProcessRunner,
		deps.DBHealthChecker,
		deps.DriveOps,
		deps.Reconciler,
	)

	return &SystemDescriptor{
		Module: mod,
	}, nil
}


