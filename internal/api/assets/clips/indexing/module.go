// Package indexing is the clips sub-descriptor that owns clip
// indexing routes: reindex (single clip) and batch reindex.
//
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): the upper
// ClipsModule narrows Dependencies. Indexing gets 5 fields: 1
// typed-narrow cluster interface + 4 standard infra. The composition
// root wires (`*clips.Handler).nonops` (parent's NonOpsHandler
// pointer) as the IndexingRoutes value — *nonops.NonOpsHandler
// satisfies the interface via Go's structural interface satisfaction
// (ReindexClip + BatchReindex methods).
package indexing

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/submodule"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Descriptor re-exports the canonical submodule.Descriptor
// concrete (godlike/06 SSOT minimum-surface UX — though Indexing is
// PRIVATE on ClipsModule, the per-package Descriptor alias remains
// available for in-package tests + re-exports).
type Descriptor = submodule.Descriptor

// IndexingRoutes is the narrow surface the indexing cluster needs
// from the nonops sub-handler. Each method corresponds to one of the
// 2 indexing routes.
//
// godlike/06 SSOT: this interface IS the canonical contract for
// "indexing needs the reindex+batch surface" — not the concrete
// *nonops.NonOpsHandler. Reuse-points (slim reindex worker, batch-
// only fixture) wrap a different impl behind this same interface.
type IndexingRoutes interface {
	ReindexClip(c *gin.Context)
	BatchReindex(c *gin.Context)
}

// Dependencies is the typed narrow input to Build. The 5 fields
// below are exactly what the 2 mounted routes consume — no more, no
// less (godlike/06 SSOT).
type Dependencies struct {
	// Indexing cluster routes (ReindexClip, BatchReindex).
	Indexing IndexingRoutes
	// Standard module infrastructure.
	EnabledFunc func() bool
	Idempotency gin.HandlerFunc
	Logger      *zap.Logger
	// ModuleOpts are optional route-module decorators.
	ModuleOpts []api.RouteModuleOption
}

// Build composes the indexing sub-descriptor from the narrow deps.
// godlike/07 NO-FAKE-AVAILABILITY: Indexing and EnabledFunc are
// REQUIRED.
func Build(deps Dependencies) (*submodule.Descriptor, error) {
	if deps.Indexing == nil {
		return nil, missingDepError("Indexing")
	}
	if deps.EnabledFunc == nil {
		return nil, missingDepError("EnabledFunc")
	}
	idem := deps.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	// The 2 indexing routes are mounted by LAMBDA inside this
	// sub-descriptor (godlike/06 SSOT one-canonical-owner:
	// indexing owns its slice-route table).
	return submodule.Build(submodule.Deps{
		Name: "clips-indexing",
		Handler: &indexingRoutesRegistrar{
			indexing: deps.Indexing,
			idem:     idem,
		},
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      deps.Logger,
		ModuleOpts:  deps.ModuleOpts,
	})
}

// indexingRoutesRegistrar is the typed-narrow RoutesRegistrar the
// indexing sub-descriptor implements.
type indexingRoutesRegistrar struct {
	indexing IndexingRoutes
	idem     gin.HandlerFunc
}

// RegisterRoutes mounts the 2 indexing routes on the supplied gin
// router group. All routes are writes (idem-protected per AGENTS.md
// Pattern 8).
//
// Route table:
//
//	POST /:source/clips/:id/reindex -> ReindexClip (write+idem)
//	POST /enrich/batch               -> BatchReindex (write+idem)
func (r *indexingRoutesRegistrar) RegisterRoutes(g *gin.RouterGroup) {
	g.POST("/:source/clips/:id/reindex", r.idem, r.indexing.ReindexClip)
	g.POST("/enrich/batch", r.idem, r.indexing.BatchReindex)
}

// missingDepError is the canonical typed-narrow error path for
// per-dep nil checks. godlike/07 NO-FAKE-AVAILABILITY.
func missingDepError(name string) error {
	return errMissingDep{"clips-indexing.Build: " + name + " is required (godlike/07 fail-closed)"}
}

// errMissingDep is the package-local typed sentinel.
type errMissingDep struct{ msg string }

func (e errMissingDep) Error() string { return e.msg }
