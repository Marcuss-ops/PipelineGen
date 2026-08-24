// Package catalog is the clips sub-descriptor that owns read/search
// routes: listing clips, getting a clip, status queries, and duplicate
// lookup. The eight routes live across three clusters today — Search
// (ListClips/GetClip/ClipStatus) + Folder (ListFolders/FolderStatus/
// GetFolderChildren/GetTree/GetBreadcrumb) on the OpsHandler + a
// single duplicate-lookup write that the parent *Handler owns.
//
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): the upper
// ClipsModule{Catalog,Ingest,Processing,Operations} descriptor
// narrows Dependencies into per-sub-module typed structs (this file:
// 7 fields — 3 typed-narrow cluster interfaces + 4 standard infra).
// Each sub-module's Build(deps) accepts the narrow deps and wraps
// them in a canonical *submodule.Descriptor.
//
// godlike/06 SSOT (one-canonical-owner-per-fact): the interfaces
// declared in this file (SearchRoutes, FolderReadRoutes,
// FindDuplicatesHandler) are the ONLY canonical contracts the catalog
// sub-descriptor depends on. The concrete impls (*clips.SearchHandler,
// *clips.OpsHandler, parent-internal method value
// (*clips.Handler).FindDuplicates) satisfy these interfaces via
// duck-typing — the parent's Build wires the per-cluster handler
// pointer as the interface value so this package has zero imports
// from internal/api/assets/clips (parent) to avoid cyclic imports.
package assets

import (
	"github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips/submodule"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Descriptor re-exports the canonical submodule.Descriptor concrete
// so the upper ClipsModule.Catalog field can be the per-sub-module
// typed pointer (godlike/06 SSOT minimum-surface UX — no
// premature interface widening, callers pin to the typed
// `*catalog.Descriptor`).
type Descriptor = submodule.Descriptor

// SearchRoutes is the narrow surface the catalog cluster needs from the
// search handler. Each method corresponds to one of the catalog
// read routes. godlike/06 SSOT: this interface IS the canonical
// contract for "catalog needs the read/search surface" — not the
// concrete *clips.SearchHandler. Reuse-points (future routed
// record-summary, batch histogram) wrap a different impl behind
// this same interface.
type SearchRoutes interface {
	RegisterRoutes(r *gin.RouterGroup, idem gin.HandlerFunc)
}

// FolderReadRoutes is the narrow surface the catalog cluster needs
// from the OpsHandler for folder read-only queries. Each method
// corresponds to one of the catalog folder routes (ListFolders /
// FolderStatus / GetFolderChildren / GetTree / GetBreadcrumb).
type FolderReadRoutes interface {
	ListFolders(c *gin.Context)
	FolderStatus(c *gin.Context)
	GetFolderChildren(c *gin.Context)
	GetTree(c *gin.Context)
	GetBreadcrumb(c *gin.Context)
}

// FindDuplicatesHandler is the single typed-narrow surface for the
// catalog write route that finds duplicates via the canonical
// duplicates.Finder capability. Typed as `gin.HandlerFunc` directly
// so the gin.POST variadic accepts the parent's method value
// (`*clips.Handler).FindDuplicates` is assignable to gin.HandlerFunc
// by Go's method-value capture rules).
type FindDuplicatesHandler gin.HandlerFunc

// Dependencies is the typed narrow input to Build. The 7 fields
// below are exactly what the 8 mounted cluster routes consume — no
// more, no less (godlike/06 SSOT: zero fat-orchestrator reach-
// through). Each field is a typed-port interface or a primitive
// (idiomatic Go), and the package compiles against the interface
// surface alone, never the concrete *clips.* parent handlers.
type Dependencies struct {
	// Search cluster routes (ListClips, GetClip, ClipStatus).
	Search SearchRoutes
	// Folder cluster routes (ListFolders, etc.).
	Folders FolderReadRoutes
	// Single write route for duplicate lookup (gin.HandlerFunc-typed).
	FindDuplicates FindDuplicatesHandler
	// Standard module infrastructure.
	EnabledFunc func() bool
	Idempotency gin.HandlerFunc
	Logger      *zap.Logger
	// ModuleOpts are optional route-module decorators.
	ModuleOpts []api.RouteModuleOption
}

// Build composes the catalog sub-descriptor from the narrow deps.
// godlike/07 NO-FAKE-AVAILABILITY: Search, Folders, FindDuplicates,
// and EnabledFunc are REQUIRED (returns error when nil so the
// composition root fails closed at boot, not silently succeeding
// at first request).
func Build(deps Dependencies) (*submodule.Descriptor, error) {
	if deps.Search == nil {
		return nil, missingDepError("Search")
	}
	if deps.Folders == nil {
		return nil, missingDepError("Folders")
	}
	if deps.FindDuplicates == nil {
		return nil, missingDepError("FindDuplicates")
	}
	if deps.EnabledFunc == nil {
		return nil, missingDepError("EnabledFunc")
	}
	// Idempotency nil-tolerated (test fixtures / dry-run CLI).
	idem := deps.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	// The routes are defined LOCALLY in this sub-descriptor
	// (catalog owns its route table). godlike/06 SSOT one-
	// canonical-owner-per-fact: catalog has its own catalogRoutes
	// closure — no parent reach-through.
	return submodule.Build(submodule.Deps{
		Name:        "clips-catalog",
		Handler:     &catalogRoutes{deps: deps, idem: idem},
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      deps.Logger,
		ModuleOpts:  deps.ModuleOpts,
	})
}

// catalogRoutes is the typed-narrow RoutesRegistrar the catalog
// sub-descriptor implements. It composes the cluster read surface
// (Search + Folders) + the single write-route (FindDuplicates) into
// a single gin.RouterGroup call. The idem middleware is captured at
// construction time so this matches api.Module.RegisterRoutes.
type catalogRoutes struct {
	deps Dependencies
	idem gin.HandlerFunc
}

// RegisterRoutes mounts the 8 catalog routes on the supplied gin
// router group. Read routes install no idem; write routes install
// idem before the handler per AGENTS.md Pattern 8.
func (r *catalogRoutes) RegisterRoutes(g *gin.RouterGroup) {
	// Read-only routes (no idempotency middleware). The Search
	// handler's RegisterRoutes installs its own routing and idem
	// for the ClipStatus POST under it.
	r.deps.Search.RegisterRoutes(g, r.idem)

	// Folder read routes (no idempotency).
	g.GET("/:source/folders", r.deps.Folders.ListFolders)
	g.GET("/:source/folders/:id", r.deps.Folders.FolderStatus)
	g.GET("/:source/folders/:id/children", r.deps.Folders.GetFolderChildren)
	g.GET("/:source/tree", r.deps.Folders.GetTree)
	g.GET("/:source/breadcrumb", r.deps.Folders.GetBreadcrumb)

	// Single write route (idempotency-protected).
	//
	// godlike/07 NO-FAKE-AVAILABILITY: the gin.HandlerFunc is a
	// method-value captured from the parent *Handler so the
	// concrete FindDuplicates impl (which fans out to the
	// duplicates.Finder capability) is the SINGLE canonical write
	// path. Method value satisfies gin.HandlerFunc by Go's
	// method-value conversion rules, so no manual gin.HandlerFunc
	// wrapper is needed at the call site.
	g.POST("/:source/clips/:id/duplicates", r.idem, gin.HandlerFunc(r.deps.FindDuplicates))
}

// missingDepError is the canonical typed-narrow error path for
// per-dep nil checks. godlike/07 NO-FAKE-AVAILABILITY: every missing
// required dep surfaces as a distinct typed sentinel so the
// composition root can branch via errors.Is / strings.Contains.
func missingDepError(name string) error {
	return errMissingDep{"clips-catalog.Build: " + name + " is required (godlike/07 fail-closed)"}
}

// errMissingDep is the package-local typed sentinel the parent +
// future tests can match against. Internal to this package; no
// godlike/06 SSOT cross-package exposure needed today.
type errMissingDep struct{ msg string }

func (e errMissingDep) Error() string { return e.msg }
