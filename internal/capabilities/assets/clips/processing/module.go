// Package processing is the clips sub-descriptor that owns media
// processing routes: reprocess and enrich. The parent's
// *nonops.NonOpsHandler satisfies the typed-narrow ProcessingRoutes
// interface implemented below via its ReprocessClip + EnrichMedia
// methods (Go's structural interface satisfaction).
//
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): the upper
// ClipsModule narrows Dependencies. Processing gets 5 fields: 1
// typed-narrow cluster interface + 4 standard infra. The composition
// root wires (`*clips.Handler).nonops` (parent's NonOpsHandler
// pointer already built in NewHandler) as the ProcessingRoutes value.
package processing

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/submodule"
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Descriptor re-exports the canonical submodule.Descriptor
// concrete so the upper ClipsModule.Processing field can be the
// per-sub-module typed pointer (godlike/06 SSOT minimum-surface UX).
type Descriptor = submodule.Descriptor

// ProcessingRoutes is the narrow surface the processing cluster
// needs from the nonops sub-handler. Each method corresponds to
// one of the 2 processing routes. godlike/06 SSOT: this interface
// IS the canonical contract for "processing needs the
// reprocess+enrich surface" — not the concrete *nonops.NonOpsHandler.
// Reuse-points (slim reprocess worker, batch-only enrichment
// fixture) wrap a different impl behind this same interface.
type ProcessingRoutes interface {
	ReprocessClip(c *gin.Context)
	EnrichMedia(c *gin.Context)
}

// Dependencies is the typed narrow input to Build. The 5 fields
// below are exactly what the 2 mounted routes consume — no more, no
// less (godlike/06 SSOT). The Processing field is a typed-port
// interface (not the concrete *nonops.NonOpsHandler).
type Dependencies struct {
	// Processing cluster routes (ReprocessClip, EnrichMedia).
	Processing ProcessingRoutes
	// Standard module infrastructure.
	EnabledFunc func() bool
	Idempotency gin.HandlerFunc
	Logger      *zap.Logger
	// ModuleOpts are optional route-module decorators.
	ModuleOpts []api.RouteModuleOption
}

// Build composes the processing sub-descriptor from the narrow deps.
// godlike/07 NO-FAKE-AVAILABILITY: Processing and EnabledFunc are
// REQUIRED (returns error when nil so the composition root fails
// closed at boot, not silently succeeding at first request).
func Build(deps Dependencies) (*submodule.Descriptor, error) {
	if deps.Processing == nil {
		return nil, missingDepError("Processing")
	}
	if deps.EnabledFunc == nil {
		return nil, missingDepError("EnabledFunc")
	}
	idem := deps.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	// The 2 processing routes are mounted by LAMBDA inside this
	// sub-descriptor (catalog-style local mounting, godlike/06
	// SSOT one-canonical-owner: processing owns its slice-route
	// table). The parent's *nonops.NonOpsHandler is the typed
	// narrow dep supplying the 2 method values.
	return submodule.Build(submodule.Deps{
		Name: "clips-processing",
		Handler: &processingRoutesRegistrar{
			processing: deps.Processing,
			idem:       idem,
		},
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      deps.Logger,
		ModuleOpts:  deps.ModuleOpts,
	})
}

// processingRoutesRegistrar is the typed-narrow RoutesRegistrar the
// processing sub-descriptor implements. It mounts the 2 processing
// routes on the supplied gin router group.
type processingRoutesRegistrar struct {
	processing ProcessingRoutes
	idem       gin.HandlerFunc
}

// RegisterRoutes mounts the 2 processing routes on the supplied
// gin router group. All routes are writes (idem-protected per
// AGENTS.md Pattern 8).
//
// Route table:
//
//	POST /:source/clips/:id/reprocess -> ReprocessClip (write+idem)
//	POST /enrich                        -> EnrichMedia   (write+idem)
func (r *processingRoutesRegistrar) RegisterRoutes(g *gin.RouterGroup) {
	g.POST("/:source/clips/:id/reprocess", r.idem, r.processing.ReprocessClip)
	g.POST("/enrich", r.idem, r.processing.EnrichMedia)
}

// missingDepError is the canonical typed-narrow error path for
// per-dep nil checks. godlike/07 NO-FAKE-AVAILABILITY: every missing
// required dep surfaces as a distinct typed sentinel.
func missingDepError(name string) error {
	return errMissingDep{"clips-processing.Build: " + name + " is required (godlike/07 fail-closed)"}
}

// errMissingDep is the package-local typed sentinel.
type errMissingDep struct{ msg string }

func (e errMissingDep) Error() string { return e.msg }
