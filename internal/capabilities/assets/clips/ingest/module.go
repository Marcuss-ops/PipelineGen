// Package ingest is the clips sub-descriptor that owns clip
// ingestion routes: create, update, and upload. The parent's
// *clips.IngestHandler satisfies the typed-narrow IngestRoutes
// interface implemented below via duck-typing (Go's structural
// interface satisfaction).
//
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): the upper
// ClipsModule{Catalog,Ingest,Processing,Operations} descriptor
// narrows Dependencies. Ingest gets 5 fields: 1 typed-narrow
// cluster interface + 4 standard infra. The composition root wires
// `(*clips.Handler).ingest` (parent's per-cluster handler pointer
// already built in NewHandler) as the IngestRoutes value.
package ingest

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/submodule"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Descriptor re-exports the canonical submodule.Descriptor concrete
// so the upper ClipsModule.Catalog/.Ingest/.Processing/.Operations
// fields can be the per-sub-module typed pointer
// (godlike/06 SSOT minimum-surface UX — no premature interface
// widening, callers pin to the typed `*catalog.Descriptor` etc.).
type Descriptor = submodule.Descriptor

// IngestRoutes is the narrow surface the ingest cluster needs from
// the per-cluster *clips.IngestHandler. godlike/06 SSOT: this
// interface IS the canonical contract for "ingest needs the
// sub-handler's route-installer" — not the concrete
// *clips.IngestHandler. Reuse-points (test fixtures, slim intake,
// specialised creation worker) wrap a different impl behind this
// same interface.
type IngestRoutes interface {
	// RegisterRoutes installs the 3 ingest routes on the supplied
	// gin router group: POST /:source/clips (CreateClip),
	// PATCH /:source/clips/:id (UpdateClip),
	// POST /upload-video (UploadVideoClip). The idem middleware
	// is captured when the parent constructed the IngestHandler
	// so idem installation is the surface's responsibility.
	RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc)
}

// Dependencies is the typed narrow input to Build. The 5 fields
// below are exactly what the 3 ingest routes consume — no more, no
// less (godlike/06 SSOT). The Ingest field is a typed-port interface
// (not the concrete *clips.IngestHandler) so this package compiles
// against the interface surface alone.
type Dependencies struct {
	// Ingest cluster routes (CreateClip, UpdateClip, UploadVideoClip).
	Ingest IngestRoutes
	// Standard module infrastructure.
	EnabledFunc func() bool
	Idempotency gin.HandlerFunc
	Logger      *zap.Logger
	// ModuleOpts are optional route-module decorators.
	ModuleOpts []api.RouteModuleOption
}

// Build composes the ingest sub-descriptor from the narrow deps.
// godlike/07 NO-FAKE-AVAILABILITY: Ingest and EnabledFunc are
// REQUIRED (returns error when nil so the composition root fails
// closed at boot, not silently succeeding at first request).
func Build(deps Dependencies) (*submodule.Descriptor, error) {
	if deps.Ingest == nil {
		return nil, missingDepError("Ingest")
	}
	if deps.EnabledFunc == nil {
		return nil, missingDepError("EnabledFunc")
	}
	idem := deps.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	// Ingest routes are owned by the parent's *clips.IngestHandler
	// — this sub-module's registrar delegates RegisterRoutes to
	// it. godlike/06 SSOT: the per-cluster route-installer is the
	// canonical owner of the cluster's gin routing — the
	// sub-module is a thin aggregator.
	return submodule.Build(submodule.Deps{
		Name:        "clips-ingest",
		Handler:     &ingestRoutesRegistrar{ingest: deps.Ingest, idem: idem},
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      deps.Logger,
		ModuleOpts:  deps.ModuleOpts,
	})
}

// ingestRoutesRegistrar is the typed-narrow RoutesRegistrar the
// ingest sub-descriptor implements. It captures the parent's
// IngestRoutes + idem at construction time and delegates
// RegisterRoutes to the underlying cluster handler so the slice
// route table stays owned by the per-cluster handler.
type ingestRoutesRegistrar struct {
	ingest IngestRoutes
	idem   gin.HandlerFunc
}

// RegisterRoutes delegates to the inner IngestRoutes. The idem is
// forwarded because the cluster's RegisterRoutes is the canonical
// installation site for it.
func (r *ingestRoutesRegistrar) RegisterRoutes(g *gin.RouterGroup) {
	r.ingest.RegisterRoutes(g, r.idem)
}

// missingDepError is the canonical typed-narrow error path for
// per-dep nil checks. godlike/07 NO-FAKE-AVAILABILITY: every missing
// required dep surfaces as a distinct typed sentinel so the
// composition root can branch via errors.Is / strings.Contains.
func missingDepError(name string) error {
	return errMissingDep{"clips-ingest.Build: " + name + " is required (godlike/07 fail-closed)"}
}

// errMissingDep is the package-local typed sentinel the parent +
// future tests can match against. Internal to this package.
type errMissingDep struct{ msg string }

func (e errMissingDep) Error() string { return e.msg }
