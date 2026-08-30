package jobs

import (
	"github.com/gin-gonic/gin"

	mw "github.com/Marcuss-ops/PipelineGen/internal/capabilities/middleware"
	apimw "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver/middleware"
)

// M2MJobsModule is the M2M (machine-to-machine) job surface — a tiny
// route module that exposes ONLY the two routes a remote submitter
// (PipelineGen / Agent / second PC) needs to submit a job and poll its
// status, each gated by the per-route scope check:
//
//	POST /api/v1/jobs      → Enqueue     (scope: jobs.submit)
//	GET  /api/v1/jobs/:id  → Get         (scope: jobs.read)
//
// It is deliberately distinct from the full JobsHandler (module.go),
// which registers ALL 9 job routes (List, Stats, GetFull, Cancel, Retry,
// Events, Replay, History) under the admin-gated /api/jobs group.
// Those administrative / dashboard / summary routes stay admin-only;
// the M2M submitter must NOT reach them (the remote computer should
// not enumerate every job on the Master, cancel arbitrary jobs, or
// read full timeline events of jobs it did not submit).
//
// The module is constructed from the same *JobsHandler the admin
// module uses (so Enqueue/Get stay single-implementation) but it is
// registered on a SEPARATE RouterGroup that carries the
// JobClientAuthMiddleware + the per-route RequireScope. The
// composition root wires the group at Setup time; this module never
// imports gin's engine directly.
//
// PG-M2M (Aug 2026): the scope constants live here (not in a shared
// constants package) so the M2M module is the single SSOT for the
// scope strings the M2M surface grants. The admin key-creation
// endpoint (POST /api/v1/admin/m2m/keys) accepts arbitrary scope
// strings; the strings MUST match these consts for the grant to be
// useful on this surface.
type M2MJobsModule struct {
	handler *JobsHandler
	enabled func() bool
}

// Scope constants for the M2M job surface. The admin key-creation
// endpoint grants these as strings in the scopes array; the
// requireScope gate checks them verbatim. Renaming a const here
// silently revokes every previously-issued key that used the old
// string — do not rename without an operator-visible migration.
const (
	ScopeJobsSubmit = "jobs.submit"
	ScopeJobsRead   = "jobs.read"
)

// NewM2MJobsModule constructs the M2M job surface from the canonical
// JobsHandler. enabled is the closure that decides whether the M2M
// surface is mounted (typically func() bool { return true } once the
// M2M store is wired; a feature flag can gate it in dev).
func NewM2MJobsModule(handler *JobsHandler, enabled func() bool) *M2MJobsModule {
	return &M2MJobsModule{handler: handler, enabled: enabled}
}

// Name returns the module name. Distinct from "jobs" (the admin module)
// so the WireRegistry / route-inventory surface reports the M2M
// surface as a separate capability mount point.
func (m *M2MJobsModule) Name() string { return "jobs-m2m" }

// Enabled forwards to the construction-time closure.
func (m *M2MJobsModule) Enabled() bool {
	if m == nil || m.enabled == nil {
		return false
	}
	return m.enabled()
}

// RegisterRoutes mounts the two M2M routes on the supplied group. The
// group is expected to already carry JobClientAuthMiddleware (the
// composition root mounts it on the group before calling this). The
// per-route RequireScope is applied HERE (not on the group) so the
// scope gate sits immediately before the handler, matching the
// proposed route table:
//
//	jobsAPI.POST("",      apimw.RequireScope(ScopeJobsSubmit), m.handler.Enqueue)
//	jobsAPI.GET("/:id",   apimw.RequireScope(ScopeJobsRead),   m.handler.Get)
//
// The prefix is intentionally empty ("") — the composition root mounts
// this module on a group already rooted at /api/v1/jobs, so the
// module's routes resolve to POST /api/v1/jobs and GET /api/v1/jobs/:id.
// This mirrors how the admin JobsHandler.RegisterRoutes uses ""
// relative to its /api/jobs group.
func (m *M2MJobsModule) RegisterRoutes(rg *gin.RouterGroup) {
	if m == nil || m.handler == nil {
		return
	}
	rg.POST("", apimw.RequireScope(ScopeJobsSubmit), m.handler.Enqueue)
	rg.GET("/:id", apimw.RequireScope(ScopeJobsRead), m.handler.Get)
}

// Compile-time assertion: M2MJobsModule satisfies the minimal
// route-module interface the composition root expects (the same
// interface admin/artlist/youtube modules satisfy via api.Module).
// Kept local so the m2m package stays free of the api package import.
var _ interface {
	Name() string
	Enabled() bool
	RegisterRoutes(*gin.RouterGroup)
} = (*M2MJobsModule)(nil)

// _ scopes: silence unused import warning when the module is compiled
// in isolation (the mw import is the M2MClient port surface the
// requireScope helper reads — referenced transitively via apimw).
var _ = mw.M2MClient{}
