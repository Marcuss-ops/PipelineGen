// Package assets provides the unified Assets HTTP module that aggregates
// all asset-related sub-handlers: storage, diagnostics, search, voiceover,
// soundeffect, and register. A single module registers all routes under
// the /api/media prefix.
package assets

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	api "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
)

// (June 2026) the Storage field type migrated from *storage.Handler to
// api.Descriptor (Blocco C1-Step 12), so the `internal/capabilities/assets/storage`
// import is no longer needed in this file — the type is referenced
// indirectly via the api.Descriptor interface and the canonical binding
// is constructed by the composition root via assetstorage.Build(...).
// The previous import was removed in the same commit.

// Dependencies holds the pre-built sub-handlers for the Assets module.
type Dependencies struct {
	// Blocco C1-Step 12 (June 2026; user-documented Step 11):
	// Storage is now an api.Descriptor (the canonical Build
	// contract surface) instead of a raw *storage.Handler. The
	// composition root threads the *assetstorage.StorageDescriptor
	// returned by assetstorage.Build(...) here; the descriptor's
	// RegisterRoutes(rg) forwarder delegates to the embedded
	// api.Module which captures the Handler in its closure (the
	// Module name "storage" + empty prefix "" preserve the
	// pre-Step-12 routing shape — the single admin route POST
	// /sync mounts directly on the parent /api/media group,
	// matching /api/media/sync URL verbatim). The storage
	// capability has a non-HTTP consumer at the Router level
	// (the QDRANT-001 server-to-server /internal/v1/media/sync
	// surface invoked via api.MediaInternalRouter.RegisterInternalMediaRoutes
	// — see internal/platform/httpserver/routes.go::Setup() and the canonical
	// Router.SetInternalMediaHandler binding); therefore the
	// StorageDescriptor keeps a Handler field (matches the clips
	// precedent exactly — register / soundeffect / stock / voiceover
	// drop the Handler field because they have no such consumer).
	Storage api.Descriptor

	// Blocco C1-Step 10 (June 2026): Diagnostics is now an
	// api.Descriptor (the canonical Build contract surface)
	// instead of a raw *diagnostics.Handler. The composition
	// root threads the *diagnostics.DiagnosticsDescriptor
	// returned by diagnostics.Build(...) here; the descriptor's
	// RegisterRoutes(rg) forwarder delegates to the embedded
	// api.Module which captures the Handler in its closure
	// (the Module name "diagnostics" + empty prefix "" preserve
	// the pre-Step-10 routing shape — the 3 routes mount
	// directly on the parent /api/media group, matching
	// /api/media/diagnostics + /api/media/index-health +
	// /api/media/qdrant/cleanup URLs verbatim). The diagnostics
	// capability has no non-HTTP consumer in the codebase (the
	// 3 routes are the entire public surface, reachable only
	// via HTTP), so the Descriptor surface is the smallest
	// possible — just `Module` field + forwarder methods
	// (matches the stock / voiceover / soundeffect / register
	// precedent exactly).
	Diagnostics api.Descriptor

	// Blocco C1-Step 11 (June 2026): Search is now an
	// api.Descriptor (the canonical Build contract surface)
	// instead of a raw *search.Handler. The composition root
	// threads the *search.SearchDescriptor returned by
	// search.Build(...) here; the descriptor's
	// RegisterRoutes(rg) forwarder delegates to the embedded
	// api.Module which captures the Handler in its closure
	// (the Module name "search" + empty prefix "" preserve
	// the pre-Step-11 routing shape — the single route mounts
	// directly on the parent /api/media group, matching
	// /api/media/search URL verbatim). The search capability
	// has no non-HTTP consumer in the codebase (the cross-
	// provider search surface reaches the canonical
	// search.Aggregator directly, not via the api/search
	// Handler), so the Descriptor surface is the smallest
	// possible — just `Module` field + forwarder methods
	// (matches the stock / voiceover / soundeffect / register /
	// diagnostics precedent exactly).
	Search api.Descriptor

	// Blocco C1-Step 5 (June 2026): Clips is now an api.Descriptor
	// (the canonical Build contract surface) instead of a raw
	// *clips.Handler. The composition root threads the
	// *clips.ClipsDescriptor returned by clips.Build(...) here; the
	// descriptor's RegisterRoutes(rg) forwarder delegates to the
	// embedded api.Module which captures the Handler orchestrator
	// in its closure. The descriptor is the single canonical
	// surface for the clips capability — no caller outside the
	// package reads *clips.Handler directly anymore.
	//
	// CLIPS-T05-001 audit-pin (2026-07-04, Phase 9 closure): the
	// clips module's canonical HTTP surface lives in
	// `internal/capabilities/assets/clips/` (routes `GET /:source/clips`,
	// `GET /:source/clips/:id`, `POST /:source/clips/:id/status`,
	// `POST /:source/clips/:id/verify`, `POST /:source/clips/:id/fix-hash`,
	// `DELETE /:source/clips/:id`) and is mounted on the parent
	// `/api/media` group via the `Clips api.Descriptor` wire below.
	// The full mount path is `/api/media/:source/clips/*` — there is
	// no separate `/api/clips` or `/api/media/clips` prefix; the
	// source segment is the dynamic part. godlike/06 SSOT: this
	// field is the SOLE canonical owner of the clips HTTP surface
	// wire (composition root constructs the *clips.ClipsDescriptor
	// in `internal/app/wire_assets_clips.go::buildClipsBundle` and
	// hands it here).
	Clips api.Descriptor

	// Blocco C1-Step 7 (June 2026): Voiceover is now an api.Descriptor
	// (the canonical Build contract surface) instead of a raw
	// *voiceover.Handler. The composition root threads the
	// *voiceover.VoiceoverDescriptor returned by voiceover.Build(...)
	// here; the descriptor's RegisterRoutes(rg) forwarder delegates
	// to the embedded api.Module which captures the Handler in its
	// closure (the Module prefix "/voiceover" is honored internally,
	// so the assets module no longer wraps `r.Group("/voiceover")`
	// around the descriptor's RegisterRoutes call).
	Voiceover api.Descriptor

	// Blocco C1-Step 8 (June 2026): SoundEffect is now an
	// api.Descriptor (the canonical Build contract surface)
	// instead of a raw *soundeffect.Handler. The composition
	// root threads the *soundeffect.SoundeffectDescriptor
	// returned by soundeffect.Build(...) here; the descriptor's
	// RegisterRoutes(rg) forwarder delegates to the embedded
	// api.Module which captures the Handler in its closure
	// (the Module prefix "/sound_effect" is honored internally,
	// so the assets module no longer wraps
	// `r.Group("/sound_effect")` around the descriptor's
	// RegisterRoutes call). The soundeffect capability has no
	// non-HTTP consumer in the codebase (/generate is the entire
	// public surface, reachable only via HTTP), so the
	// Descriptor surface is the smallest possible — just
	// `Module` field + forwarder methods (matches the stock
	// precedent exactly).
	SoundEffect api.Descriptor

	// Blocco C1-Step 9 (June 2026): Register is now an
	// api.Descriptor (the canonical Build contract surface)
	// instead of a raw *register.Handler. The composition
	// root threads the *register.RegisterDescriptor returned
	// by register.Build(...) here; the descriptor's
	// RegisterRoutes(rg) forwarder delegates to the embedded
	// api.Module which captures the Handler in its closure
	// (the Module name "register" + empty prefix "" preserve
	// the pre-Step-9 routing shape — the two routes mount
	// directly on the parent /api/media group, matching
	// /api/media/register-from-youtube + /api/media/register-batch
	// URLs verbatim). The register capability has no non-HTTP
	// consumer in the codebase (the YouTubeRegistrar's non-HTTP
	// surface is the sourcingEnrichmentAdapter which calls
	// clipsHandler.EnrichAndIndexClip — that consumer is
	// satisfied by clipsDesc.Handler, not by the register
	// Handler), so the Descriptor surface is the smallest
	// possible — just `Module` field + forwarder methods
	// (matches the stock / voiceover / soundeffect precedent
	// exactly).
	Register api.Descriptor
}

// Module is the unified Assets HTTP module.
type Module struct {
	deps Dependencies
	log  *zap.Logger
}

// NewModule creates an AssetsModule from pre-built dependencies.
func NewModule(deps Dependencies, log *zap.Logger) *Module {
	return &Module{deps: deps, log: log}
}

// RegisterRoutes registers all asset routes under the given parent group.
func (m *Module) RegisterRoutes(r *gin.RouterGroup) {
	m.log.Info("Registering unified Assets module routes")

	// Storage operations (drive/move-files, create-folders, etc.)
	if m.deps.Storage != nil {
		m.deps.Storage.RegisterRoutes(r)
	}

	// Diagnostics operations (index-health, qdrant health).
	// Blocco C1-Step 10 (June 2026): the diagnostics Descriptor
	// owns its own Module; the assets module passes the parent
	// /api/media group straight through (no more inline
	// r.Group("") wrap — the descriptor's DiagnosticsDescriptor
	// forwarder delegates to the embedded api.Module which
	// mounts the 3 routes directly on the parent group,
	// preserving the public URLs /api/media/diagnostics +
	// /api/media/index-health + /api/media/qdrant/cleanup
	// verbatim).
	if m.deps.Diagnostics != nil {
		m.deps.Diagnostics.RegisterRoutes(r)
	}

	// Search operations (cross-provider keyword search).
	// Blocco C1-Step 11 (June 2026): the search Descriptor
	// owns its own Module; the assets module passes the parent
	// /api/media group straight through (no more inline
	// r.Group("") wrap — the descriptor's SearchDescriptor
	// forwarder delegates to the embedded api.Module which
	// mounts the single route directly on the parent group,
	// preserving the public URL /api/media/search verbatim).
	if m.deps.Search != nil {
		m.deps.Search.RegisterRoutes(r)
	}

	// Clip operations (/clips/*)
	if m.deps.Clips != nil {
		m.deps.Clips.RegisterRoutes(r)
	}

	// Voiceover operations (/voiceover/*). Blocco C1-Step 7 (June 2026):
	// the voiceover Module owns its own /voiceover prefix; the assets
	// module passes the parent /api/media group straight through.
	if m.deps.Voiceover != nil {
		m.deps.Voiceover.RegisterRoutes(r)
	}

	// SoundEffect operations (/sound_effect/*). Blocco C1-Step 8
	// (June 2026): the soundeffect Module owns its own
	// /sound_effect prefix; the assets module passes the parent
	// /api/media group straight through (no more
	// `r.Group("/sound_effect")` wrap — the descriptor's
	// SoundeffectDescriptor.RegisterRoutes(rg) forwarder delegates
	// to the embedded api.Module which routes to handler.Generate).
	if m.deps.SoundEffect != nil {
		m.deps.SoundEffect.RegisterRoutes(r)
	}

	// YouTube registration (register-from-youtube, register-batch).
	// Blocco C1-Step 9 (June 2026): the register Descriptor owns
	// its own Module; the assets module passes the parent
	// /api/media group straight through (no more inline
	// r.Group("") wrap — the descriptor's RegisterDescriptor
	// forwarder delegates to the embedded api.Module which
	// mounts the two routes directly on the parent group,
	// preserving the public URLs /api/media/register-from-youtube
	// + /api/media/register-batch verbatim).
	if m.deps.Register != nil {
		m.deps.Register.RegisterRoutes(r)
	}
}
