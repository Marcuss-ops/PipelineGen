// Package operations is the clips sub-descriptor that owns
// operational routes: integrity (verify/fix-hash) and maintenance
// (trash/delete/reconcile/cleanup/regenerate-manifest).
//
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): the upper
// ClipsModule narrows Dependencies. Operations gets 5 fields: 1
// typed-narrow cluster interface (OpsRoutes) + 4 standard infra.
// The former bulk-tag routes (add/remove) were migrated to the
// canonical /api/assets/operator/bulk surface (add_tags/remove_tags)
// and the legacy source-specific transports were removed.
//
// godlike/06 SSOT (one-canonical-owner-per-fact): the OpsRoutes
// interface is the canonical contract for the 7 ops-only routes —
// not the concrete *clips.OpsHandler. The composition root wires
// it via the parent's per-cluster handler pointer:
// opts.Ops = (*clips.Handler).ops.
package assets

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/submodule"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Descriptor re-exports the canonical submodule.Descriptor
// concrete so the upper ClipsModule.Operations field can be the
// per-sub-module typed pointer (godlike/06 SSOT minimum-surface UX).
type Descriptor = submodule.Descriptor

// OpsRoutes is the narrow surface the operations cluster needs
// from the per-cluster *clips.OpsHandler. The 7 methods correspond
// to the 7 ops-only routes (verify/fix-hash/trash/reconcile/cleanup/
// regenerate-manifest/trash-folder).
//
// godlike/06 SSOT: this interface IS the canonical contract —
// not the concrete *clips.OpsHandler. Reuse-points (slim-ops
// worker, audit-only fixture) wrap a different impl behind this
// same interface.
type OpsRoutes interface {
	VerifyClip(c *gin.Context)
	HandleFixHash(c *gin.Context)
	TrashClip(c *gin.Context)
	Reconcile(c *gin.Context)
	Cleanup(c *gin.Context)
	RegenerateManifest(c *gin.Context)
	TrashFolder(c *gin.Context)
}

// Dependencies is the typed narrow input to Build. The 5 fields
// below are exactly what the 7 mounted routes consume — no more, no
// less (godlike/06 SSOT).
type Dependencies struct {
	// Ops cluster routes (VerifyClip, HandleFixHash, TrashClip,
	// Reconcile, Cleanup, RegenerateManifest, TrashFolder).
	Ops OpsRoutes
	// Standard module infrastructure.
	EnabledFunc func() bool
	Idempotency gin.HandlerFunc
	Logger      *zap.Logger
	// ModuleOpts are optional route-module decorators.
	ModuleOpts []api.RouteModuleOption
}

// Build composes the operations sub-descriptor from the narrow
// deps. godlike/07 NO-FAKE-AVAILABILITY: Ops and EnabledFunc are
// REQUIRED.
func Build(deps Dependencies) (*submodule.Descriptor, error) {
	if deps.Ops == nil {
		return nil, missingDepError("Ops")
	}
	if deps.EnabledFunc == nil {
		return nil, missingDepError("EnabledFunc")
	}
	idem := deps.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	// All 7 operations routes are mounted by LAMBDA inside this
	// sub-descriptor (godlike/06 SSOT one-canonical-owner:
	// operations owns its slice-route table). The Ops field
	// supplies 7 method values for the ops-only routes.
	return submodule.Build(submodule.Deps{
		Name: "clips-operations",
		Handler: &operationsRoutesRegistrar{
			ops:  deps.Ops,
			idem: idem,
		},
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      deps.Logger,
		ModuleOpts:  deps.ModuleOpts,
	})
}

// operationsRoutesRegistrar is the typed-narrow RoutesRegistrar the
// operations sub-descriptor implements.
type operationsRoutesRegistrar struct {
	ops  OpsRoutes
	idem gin.HandlerFunc
}

// RegisterRoutes mounts the 7 operations routes on the supplied
// gin router group. All routes are writes (idem-protected per
// AGENTS.md Pattern 8).
//
// Route table:
//
//	POST   /:source/clips/:id/verify              -> VerifyClip
//	POST   /:source/clips/:id/fix-hash            -> HandleFixHash
//	DELETE /:source/clips/:id                     -> TrashClip
//	POST   /:source/reconcile                     -> Reconcile
//	POST   /:source/cleanup                       -> Cleanup
//	POST   /:source/folders/:id/manifest          -> RegenerateManifest
//	DELETE /:source/folders/:id                   -> TrashFolder
func (r *operationsRoutesRegistrar) RegisterRoutes(g *gin.RouterGroup) {
	// Integrity routes (ops cluster).
	g.POST("/:source/clips/:id/verify", r.idem, r.ops.VerifyClip)
	g.POST("/:source/clips/:id/fix-hash", r.idem, r.ops.HandleFixHash)

	// Maintenance routes (ops cluster).
	g.DELETE("/:source/clips/:id", r.idem, r.ops.TrashClip)
	g.POST("/:source/reconcile", r.idem, r.ops.Reconcile)
	g.POST("/:source/cleanup", r.idem, r.ops.Cleanup)
	g.POST("/:source/folders/:id/manifest", r.idem, r.ops.RegenerateManifest)
	g.DELETE("/:source/folders/:id", r.idem, r.ops.TrashFolder)
}

// missingDepError is the canonical typed-narrow error path for
// per-dep nil checks. godlike/07 NO-FAKE-AVAILABILITY.
func missingDepError(name string) error {
	return errMissingDep{"clips-operations.Build: " + name + " is required (godlike/07 fail-closed)"}
}

// errMissingDep is the package-local typed sentinel.
type errMissingDep struct{ msg string }

func (e errMissingDep) Error() string { return e.msg }
