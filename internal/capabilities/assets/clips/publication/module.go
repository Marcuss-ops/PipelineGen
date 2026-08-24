// Package publication is the clips sub-descriptor that owns media
// publication/retrieval routes: download (local file or Drive proxy)
// and reupload (re-upload to Drive after a folder resolution).
//
// PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE (July 2026): the upper
// ClipsModule narrows Dependencies. Publication gets 5 fields: 1
// typed-narrow cluster interface + 4 standard infra. The composition
// root wires (`*clips.Handler).DownloadClip + .ReuploadClip` as the
// PublicationRoutes method-value pair (parent instance satisfies
// the interface via Go's structural interface satisfaction).
package publication

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/submodule"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Descriptor re-exports the canonical submodule.Descriptor
// concrete (godlike/06 SSOT minimum-surface UX — though Publication
// is PRIVATE on ClipsModule, the per-package Descriptor alias
// remains available for in-package tests + re-exports).
type Descriptor = submodule.Descriptor

// PublicationRoutes is the narrow surface the publication cluster
// needs from the action methods on the parent *clips.Handler. Each
// method corresponds to one of the 2 publication routes.
//
// godlike/06 SSOT: this interface IS the canonical contract for
// "publication needs the download+reupload surface" — not the
// concrete *clips.Handler. Test fixtures wrap a slim stub behind
// this same interface.
type PublicationRoutes interface {
	DownloadClip(c *gin.Context)
	ReuploadClip(c *gin.Context)
}

// Dependencies is the typed narrow input to Build. The 5 fields
// below are exactly what the 2 mounted routes consume — no more, no
// less (godlike/06 SSOT).
type Dependencies struct {
	// Publication cluster routes (DownloadClip, ReuploadClip).
	Publication PublicationRoutes
	// Standard module infrastructure.
	EnabledFunc func() bool
	Idempotency gin.HandlerFunc
	Logger      *zap.Logger
	// ModuleOpts are optional route-module decorators.
	ModuleOpts []api.RouteModuleOption
}

// Build composes the publication sub-descriptor from the narrow
// deps. godlike/07 NO-FAKE-AVAILABILITY: Publication and EnabledFunc
// are REQUIRED.
func Build(deps Dependencies) (*submodule.Descriptor, error) {
	if deps.Publication == nil {
		return nil, missingDepError("Publication")
	}
	if deps.EnabledFunc == nil {
		return nil, missingDepError("EnabledFunc")
	}
	idem := deps.Idempotency
	if idem == nil {
		idem = func(c *gin.Context) { c.Next() }
	}

	// The 2 publication routes are mounted by LAMBDA inside this
	// sub-descriptor (godlike/06 SSOT one-canonical-owner:
	// publication owns its slice-route table).
	return submodule.Build(submodule.Deps{
		Name: "clips-publication",
		Handler: &publicationRoutesRegistrar{
			publication: deps.Publication,
			idem:        idem,
		},
		EnabledFunc: deps.EnabledFunc,
		Idempotency: idem,
		Logger:      deps.Logger,
		ModuleOpts:  deps.ModuleOpts,
	})
}

// publicationRoutesRegistrar is the typed-narrow RoutesRegistrar the
// publication sub-descriptor implements.
type publicationRoutesRegistrar struct {
	publication PublicationRoutes
	idem        gin.HandlerFunc
}

// RegisterRoutes mounts the 2 publication routes on the supplied
// gin router group. All routes are writes (idem-protected per
// AGENTS.md Pattern 8).
//
// Route table:
//
//	POST /:source/clips/:id/download -> DownloadClip (write+idem)
//	POST /:source/clips/:id/reupload -> ReuploadClip (write+idem)
func (r *publicationRoutesRegistrar) RegisterRoutes(g *gin.RouterGroup) {
	g.POST("/:source/clips/:id/download", r.idem, r.publication.DownloadClip)
	g.POST("/:source/clips/:id/reupload", r.idem, r.publication.ReuploadClip)
}

// missingDepError is the canonical typed-narrow error path for
// per-dep nil checks. godlike/07 NO-FAKE-AVAILABILITY.
func missingDepError(name string) error {
	return errMissingDep{"clips-publication.Build: " + name + " is required (godlike/07 fail-closed)"}
}

// errMissingDep is the package-local typed sentinel.
type errMissingDep struct{ msg string }

func (e errMissingDep) Error() string { return e.msg }
