// Package operator — handler.go (FACCIATA, July 2026 split by resource).
//
// Provides admin-facing read-only API endpoints for the Operator
// Console. All routes are mounted under /api/assets/operator/* (the
// canonical prefix is supplied by the registry_operator_console.go
// caller, which calls Handler.RegisterRoutes(rg) with rg = the
// /api/assets/operator group).
//
// Split rationale (resource/handler, mount under the same prefix):
//
//   - handler.go               : THIS FILE. Facciata — Handler struct
//
//   - Dependencies + NewHandler +
//     RegisterRoutes (delegates to the
//     per-resource sub-routers below).
//
//   - handler_summary.go       : Resource SUMMARY — dashboard
//     aggregation. 1 route
//     (GET /summary).
//
//   - handler_assets.go        : Resource ASSETS — list + get +
//     preview. 3 routes
//     (GET /assets, /assets/:id,
//     /assets/:id/preview).
//
//   - handler_outbox.go        : Resource OUTBOX — status + events.
//     2 routes (GET /outbox/status,
//     /outbox/events).
//
//   - handler_index.go         : Resource INDEX-HEALTH — single status
//     endpoint. 1 route (GET /index-health).
//
// All sub-routers share the parent *gin.RouterGroup passed to
// RegisterRoutes: the canonical /api/assets/operator prefix is
// preserved end-to-end without extra Group nesting — each per-
// resource registerXxxRoutes method just receives the parent rg and
// mounts its specific GET paths on it.
//
// Cross-resource helpers placement (intentional):
//   - summariesToJSON (asset-related, used by summary + assets) —
//     lives in handler_assets.go (semantic ownership: it converts
//     []*asset.Summary to JSON, so the assets file is its home).
//   - jobsToJSON (used only by summary) — lives in handler_summary.go.
//   - isAllowedPath + maskPath (used only by assets/preview) —
//     live in handler_assets.go.
//
// godlike/06 SSOT: each sub-router's URLs and Handler methods are
// bit-identical to the pre-split spec. The mount-prefix
// ("/api/assets/operator") is owned by the registry_operator_console.go
// caller; the handler is prefix-agnostic (it registers RELATIVE
// paths like "/summary" + "/assets"). This preserves the canonical
// URL surface after the split.
package operator

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Handler is the thin HTTP transport for operator console API endpoints.
// All HTTP methods hang off this struct; sub-router entry points are
// the per-resource registerXxxRoutes methods below.
type Handler struct {
	assetService  *detail.Service
	readModel     AssetInventoryReader
	indexVerifier IndexVerifier
	jobService    job.Service
	jobStats      JobStatsReader
	outboxPort    jobs.MonitorPort
	mutator       mutations.AssetMutationDispatcher
	committer     persistence.AssetCommitter
	allowedRoots  []string
	log           *zap.Logger
}

// JobStatsReader is the narrow port for job statistics.
type JobStatsReader = jobs.JobStatsReader

type OperatorOptions struct {
	AllowedRoots []string // directories allowed for file previews
}

// Dependencies holds the pre-built dependencies for the operator handler.
type Dependencies struct {
	*OperatorOptions
	AssetService  *detail.Service
	ReadModel     AssetInventoryReader
	IndexVerifier IndexVerifier
	JobService    job.Service
	JobStats      JobStatsReader
	OutboxPort    jobs.MonitorPort
	Mutator       mutations.AssetMutationDispatcher
	Committer     persistence.AssetCommitter
}

// NewHandler creates a new operator API handler.
func NewHandler(deps Dependencies, log *zap.Logger) *Handler {
	return &Handler{
		assetService:  deps.AssetService,
		readModel:     deps.ReadModel,
		indexVerifier: deps.IndexVerifier,
		jobService:    deps.JobService,
		jobStats:      deps.JobStats,
		outboxPort:    deps.OutboxPort,
		mutator:       deps.Mutator,
		committer:     deps.Committer,
		allowedRoots:  operatorAllowedRoots(deps.OperatorOptions),
		log:           log,
	}
}

func operatorAllowedRoots(options *OperatorOptions) []string {
	if options == nil {
		return nil
	}
	return options.AllowedRoots
}

// RegisterRoutes mounts the operator endpoints under the given router
// group. Internally delegates to per-resource sub-routers (each in
// its own sibling file). ALL sub-routers share the parent router
// group, so the canonical /api/assets/operator/* prefix is preserved.
// Register order matches the original spec to keep URL-grouping
// canonical: summary → assets → outbox → index-health → operations.
//
// godlike/06 SSOT: this method is the canonical entry point. Adding
// a new resource = adding a new handleXxx file with a
// registerXxxRoutes method, then wiring it here (single-edit change).
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	h.registerSummaryRoutes(rg)
	h.registerAssetsRoutes(rg)
	h.registerBulkRoutes(rg)
	h.registerOutboxRoutes(rg)
	h.registerIndexRoutes(rg)
	h.registerOperationsRoutes(rg)
}

// ── Module wrapper (consumed by sibling `module.go` for api.Descriptor) ──
//
// moduleWrapper wraps Handler to satisfy the api.Module interface that
// internal/api/registry.Module consumes. It lives in handler.go (facciata)
// next to NewHandler because module.go::Build calls NewModule(handler)
// at construction time and this is the canonical factory pair.
//
// godlike/06 SSOT: OperatorDescriptor (in module.go) is the api.Descriptor
// surface; moduleWrapper is its pre-descriptor shim with the two-state
// Enabled() capability.
type moduleWrapper struct {
	*Handler
	name    string
	enabled bool
}

// NewModule creates a module wrapper for the operator handler.
func NewModule(h *Handler) *moduleWrapper {
	return &moduleWrapper{Handler: h, name: "operator", enabled: true}
}

func (m *moduleWrapper) Name() string  { return m.name }
func (m *moduleWrapper) Enabled() bool { return m.enabled }

// Compile-time check that Handler satisfies the module interface.
var _ interface {
	Name() string
	Enabled() bool
	RegisterRoutes(rg *gin.RouterGroup)
} = (*moduleWrapper)(nil)
