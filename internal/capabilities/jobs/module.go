// Package jobs — module.go: the single canonical Build entrypoint for
// the Jobs HTTP capability (the public-facing job lifecycle surface at
// /api/jobs/*).
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
// This file is part of Blocco C1-Step 13 (June 2026): every capability
// in `internal/api/**` and `internal/application/**` MUST expose a
// Build(d) signature. Direct canonical-registry Calls inside a
// capability package are forbidden (godlike/07 + the canonical
// `internal/app/capability_registry.go` hoist site landed in
// Blocco C1-Step 2). The composition root consumes this Build via
// `internal/app/registry_internal_modules.go::registerJobsRoute` +
// `internal/app/registry_public_modules.go::registerJobs` and threads
// the returned Descriptor into `tryRegisterModuleStrict(registry, ...)`
// (Module name "jobs" + prefix "/jobs" → final URL /api/jobs/*).
//
// Pattern parity with `artlist/module.go` (C1-Step 3),
// `youtube/module.go` (C1-Step 4), `clips/module.go` (C1-Step 5),
// `stock/module.go` (C1-Step 6), `voiceover/module.go` (C1-Step 7),
// `soundeffect/module.go` (C1-Step 8), `register/module.go`
// (C1-Step 9), `diagnostics/module.go` (C1-Step 10),
// `search/module.go` (C1-Step 11).
//
// UNIQUE TO JOBS (vs the assets/ tree): the jobs capability is NOT
// mounted under the `/api/media/*` aggregator (the assets umbrella).
// It lives at the top-level `/api/jobs/*` (sibling of /api/system,
// /api/images, /api/script/*). The Module prefix "/jobs" maps to
// the canonical /api/jobs/* URL set.
//
// UNIQUE TO JOBS (PR-0, June 2026): the Handler depends on TWO
// narrow ports — `jobs.Service` (the orchestrator, exposes
// Enqueue/Cancel/Retry/Get/List/ListEvents) AND
// `appjobs.JobStatsReader` (the stats reader, exposes GetStats
// only). The split is intentional (per the PR-0 commit comment):
// a future Postgres migration can wire a different stats reader
// (e.g. one that aggregates across shards) without touching the
// orchestrator's mutation surface. *appjobs.Service satisfies both
// interfaces via compile-time assertion in
// internal/capabilities/jobs/queue/stats.go — production wiring passes the
// same concrete pointer to both fields.
//
// The two deps flow through Build as flat Dependencies fields. The
// composition root owns the *appjobs.Service construction
// (via BuildJobsBundle in module_media.go); the api/ layer never
// builds it (per AGENTS.md Pattern 0).
//
// UNIQUE TO JOBS (vs clips): the Descriptor surface is the smallest
// in the tree today (tied with stock / voiceover / soundeffect /
// register / diagnostics / search) — only `Module` field, no
// `Handler` / `Service` field. The jobs HTTP capability has no
// non-HTTP consumer in the codebase that needs the raw JobsHandler
// (the `jobs.Service` is consumed via the JobsBundle which is
// the canonical facade for non-HTTP consumers like the clips
// orchestrator's EnrichUseCase). The Handler stays the internal
// worker captured by the Module closure; no caller (composition
// root, tests, internal services) reads a raw *JobsHandler from
// outside the package.
//
// NOTE: this file covers ONLY the public-facing JobsHandler. The
// internal `WorkersBrokerHandler` (handler_workers.go, mounted on
// `remoteshared.InternalPathPrefix`, NOT /api/) is a SEPARATE
// capability — it is registered via `Server.SetWorkerHandler` and
// does NOT need a Build contract in this step (the existing
// direct-construction wire shape is preserved).
package jobs

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/api"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Dependencies is the typed narrow input to Build. The JobsHandler
// depends on a jobs.Service (orchestrator) + an
// appjobs.JobStatsReader (narrow stats port) + a logger.
//
// Mandatory fields return an error when nil; optional fields fall
// through to the handler's existing nil-tolerance. Logger nil →
// zap.NewNop() (composition-root-friendly default).
type Dependencies struct {
	// Service is the canonical jobs.Service orchestrator
	// (Enqueue/Cancel/Retry/Get/List/ListEvents). In production,
	// the JobsBundle constructs the *appjobs.Service and
	// exposes it as root.Jobs.Service. MANDATORY — Build
	// returns an error when nil. The Handler stores h.service
	// and 6 of the 7 routes call service methods
	// unconditionally. A nil Service would NPE at first
	// request; fail at startup instead.
	Service jobs.Service

	// Stats is the canonical appjobs.JobStatsReader port
	// (PR-0, June 2026). In production, the
	// sfxstatsJobStatsReaderAdapter wraps *appjobs.Service's
	// GetStats helper. *appjobs.Service satisfies both
	// `jobs.Service` AND `appjobs.JobStatsReader` via
	// compile-time assertion — production wiring passes the
	// same concrete pointer to both fields. MANDATORY —
	// Build returns an error when nil. The Handler stores
	// h.stats and the /stats route calls stats.GetStats
	// unconditionally. A nil Stats would NPE at first
	// request; fail at startup instead.
	Stats appjobs.JobStatsReader

	// EnabledFunc is the closure that decides whether the
	// module's routes are mounted. The jobs capability has no
	// feature flag in production (always on) — the composition
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
	Logger  *zap.Logger
	History appjobs.HistoryReader
}

// JobsDescriptor is the concrete capability Descriptor returned
// by Build. It satisfies api.Descriptor via the explicit Module
// field (named, not embedded — no method-promotion surprises from
// api.Module) and forwarder methods.
//
// UNIQUE TO JOBS: the Descriptor does NOT expose the Handler
// (matches the stock / voiceover / soundeffect / register /
// diagnostics / search precedent of dropping the explicit Handler
// field) NOR the Service (the Service is a composition-root
// artifact — moving it into the api/ layer would require moving
// the JobsBundle + composition root chain too, violating
// AGENTS.md Pattern 0). There is no non-HTTP consumer of the
// JobsHandler in the codebase — the 7 routes (POST/GET "",
// GET /stats, GET /:id, GET /:id/full, POST /:id/cancel, POST
// /:id/retry, GET /:id/events) are the entire public surface,
// reachable only via HTTP. The Handler stays the internal worker
// captured by the Module closure; no caller reads a raw *JobsHandler
// from outside the package.
type JobsDescriptor struct {
	// Module is the route-only Module (api.NewRouteModule
	// instance) the composition root threads into
	// tryRegisterModuleStrict.
	Module        api.Module
	HistoryModule api.Module
}

// Name returns the module name ("jobs"). The pre-Step-13 jobs
// routes were registered on a module named "jobs" with prefix
// "/jobs" → final URL /api/jobs/*. The new Build contract
// preserves the Module name + prefix verbatim (zero-change-contract).
func (d *JobsDescriptor) Name() string {
	return d.Module.Name()
}

// Enabled forwards to the Module's closure.
func (d *JobsDescriptor) Enabled() bool {
	return d.Module.Enabled()
}

// RegisterRoutes forwards to the Module — the Handler is reachable
// only via the Module's internal closure.
func (d *JobsDescriptor) RegisterRoutes(rg *gin.RouterGroup) {
	d.Module.RegisterRoutes(rg)
	d.HistoryModule.RegisterRoutes(rg)
}

type historyRoute struct{ handler *JobsHandler }

func (h historyRoute) RegisterRoutes(rg *gin.RouterGroup) { rg.GET("", h.handler.History) }

// Build composes the Jobs HTTP capability from the typed narrow
// dependencies. Returns a fail-closed error when any mandatory dep
// is nil. Logger nil → zap.NewNop(). ModuleOpts nil → no decorators.
//
// The returned Descriptor carries the Module (routes). The HTTP
// Handler is constructed here and captured by the Module's
// RegisterRoutes closure — no caller (composition root, tests,
// internal services) reads the raw Handler anywhere outside this
// function. NewJobsHandler is preserved for direct callers that
// bypass Build (e.g. the test fixture at
// handler_legacy_int_stock_test.go::line 190).
func Build(deps Dependencies) (api.Descriptor, error) {
	// Mandatory-shape validation.
	if deps.Service == nil {
		return nil, fmt.Errorf("jobs.Build: Service is required (composition root must pre-construct the *appjobs.Service via BuildJobsBundle; the api/ layer never builds it)")
	}
	if deps.Stats == nil {
		return nil, fmt.Errorf("jobs.Build: Stats is required (PR-0 split — the /stats route calls stats.GetStats unconditionally; a nil port would NPE at first request)")
	}
	if deps.EnabledFunc == nil {
		return nil, fmt.Errorf("jobs.Build: EnabledFunc is required (composition root must wire a closure — typically func() bool { return true } — so this package stays free of platform/config imports)")
	}

	// Logger: nil → zap.NewNop() (composition-root-friendly default).
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}

	// Construct the canonical Handler. NewJobsHandler has no
	// fail-closed checks (preserves the pre-Step-13 behavior for
	// direct callers that bypass Build); Build's checks above
	// are the new defensive layer.
	handler := NewJobsHandler(
		deps.Service,
		deps.Stats,
		log,
	)
	handler.SetHistoryReader(deps.History)

	// Construct the route Module. The closure inside
	// api.NewRouteModule calls handler.RegisterRoutes(r) — the
	// Handler is captured here, not exposed to the composition
	// root via the Module surface.
	//
	// Module name "jobs" + prefix "/jobs" preserves the
	// pre-Step-13 wire shape: the 7 routes (POST/GET "",
	// GET /stats, GET /:id, GET /:id/full, POST /:id/cancel,
	// POST /:id/retry, GET /:id/events) mount under the
	// /api/jobs/* prefix. The Module name is the canonical
	// identifier (used for logging + EnabledFunc wiring).
	mod := api.NewRouteModule(
		"jobs",
		deps.EnabledFunc,
		"/jobs",
		handler,
		log,
		deps.ModuleOpts...,
	)
	historyMod := api.NewRouteModule("history", deps.EnabledFunc, "/history", historyRoute{handler: handler}, log)

	return &JobsDescriptor{
		Module:        mod,
		HistoryModule: historyMod,
	}, nil
}
