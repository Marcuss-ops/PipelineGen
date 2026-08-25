// Package bulk is the clips sub-descriptor that owns the bulk
// upload route: a single POST that enqueues a bulk upload of
// YouTube clips.
//
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): the upper
// ClipsModule narrows Dependencies. Bulk gets 5 fields: 1 typed-
// narrow cluster interface + 4 standard infra. The composition
// root wires (`*clips.Handler).bulkTransport` (parent's
// *BulkUploadTransport pointer already built in NewHandler) as the
// BulkTransportRoutes value.
//
// Bulk is not exposed on the upper ClipsModule struct (godlike/06
// SSOT: minimal-needed cross-package surface). The bulk sub-
// descriptor is routing-only and has no composition-root consumers
// other than its own route mounting.
package bulk

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/submodule"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Descriptor re-exports the canonical submodule.Descriptor
// concrete (godlike/06 SSOT minimum-surface UX — though Bulk is
// PRIVATE on ClipsModule, the per-package Descriptor alias remains
// available for in-package tests + re-exports).
type Descriptor = submodule.Descriptor

// BulkTransportRoutes is the narrow surface the bulk sub-descriptor
// needs from the parent's *clips.BulkUploadTransport. The single
// method corresponds to the single bulk upload route -- the bulk
// transport internally mounts its route via RegisterRoutes.
//
// godlike/06 SSOT: this interface IS the canonical contract for
// "bulk needs the transport surface" -- not the concrete
// *clips.BulkUploadTransport. Test fixtures wrap a slim stub
// behind this same interface.
type BulkTransportRoutes interface {
	// RegisterRoutes installs the single bulk upload route on
	// the supplied gin router group. The idem middleware is
	// captured at construction time by the parent *BulkUploadTransport.
	RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc)
}

// Dependencies is the typed narrow input to Build. The 5 fields
// below are exactly what the 1 mounted route consumes -- no more,
// no less (godlike/06 SSOT).
type Dependencies struct {
	// Bulk transport routes (EnqueueBulkUpload).
	Transport BulkTransportRoutes
	// Standard module infrastructure.
	EnabledFunc func() bool
	Idempotency gin.HandlerFunc
	Logger      *zap.Logger
	// ModuleOpts are optional route-module decorators.
	ModuleOpts []api.RouteModuleOption
}

// Build composes the bulk sub-descriptor from the narrow deps.
// godlike/07 NO-FAKE-AVAILABILITY: Transport and EnabledFunc are
// REQUIRED.
func Build(deps Dependencies) (*submodule.Descriptor, error) {
	if deps.Transport == nil {
		return nil, missingDepError("Transport")
	}
	if deps.EnabledFunc == nil {
		return nil, missingDepError("EnabledFunc")
	}
	idem := deps.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	// The single bulk route is owned by the parent's
	// *clips.BulkUploadTransport -- this sub-module's registrar
	// delegates RegisterRoutes to it. godlike/06 SSOT: the
	// per-cluster route-installer is the canonical owner of the
	// cluster's gin routing -- the sub-module is a thin
	// aggregator.
	return submodule.Build(submodule.Deps{
		Name:        "clips-bulk",
		Handler:     &bulkRoutesRegistrar{transport: deps.Transport, idem: idem},
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      deps.Logger,
		ModuleOpts:  deps.ModuleOpts,
	})
}

// bulkRoutesRegistrar is the typed-narrow RoutesRegistrar the bulk
// sub-descriptor implements.
type bulkRoutesRegistrar struct {
	transport BulkTransportRoutes
	idem      gin.HandlerFunc
}

// RegisterRoutes delegates to the inner BulkTransportRoutes. The
// idem is forwarded because the cluster's RegisterRoutes is the
// canonical installation site for it.
func (r *bulkRoutesRegistrar) RegisterRoutes(g *gin.RouterGroup) {
	r.transport.RegisterRoutes(g, r.idem)
}

// missingDepError is the canonical typed-narrow error path for
// per-dep nil checks. godlike/07 NO-FAKE-AVAILABILITY.
func missingDepError(name string) error {
	return errMissingDep{"clips-bulk.Build: " + name + " is required (godlike/07 fail-closed)"}
}

// errMissingDep is the package-local typed sentinel.
type errMissingDep struct{ msg string }

func (e errMissingDep) Error() string { return e.msg }
