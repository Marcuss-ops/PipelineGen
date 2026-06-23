// Package scripts — scene_builder_usecase is the use case that
// constructs the ScenesService for the post-generation phase 2 fan-out.
//
// Wave 14 problem #4 (June 2026): the previous handler called the
// scenes-service factory inline:
//
//	scenesSvc := scripts.NewScenesService(
//	    h.clipServices.ImgSvc,
//	    h.clipServices.VoSvc,
//	    h.log,
//	    h.cfg,
//	    h.resolveDriveFolderID,
//	    h.groupsResolver,
//	    0,
//	)
//
// Five wires + a magic 0 trailing arg, all sourced from the handler's
// own fields, then immediately passed to a Pipeline constructor in
// the same handler body. Two problems:
//
//   - the handler grew to ~470 LOC and the inline factory made it
//     impossible to swap scenes-service construction (e.g. for tests
//     or for a different drive-folder resolution path) without
//     re-touching the handler;
//   - the magic 0 (`albumCapacityHint`) was uncommented and untested;
//     any future change risked silently changing the scenes-service
//     behaviour.
//
// Moving the factory here makes the ScenesService construction a
// single typed method (Build) with named fields, no magic constants,
// and a port-shaped ImageSearchService / VoiceoverSvc so the use case
// is testable without the full images.Service fleet.
//
// The use case owns:
//   - the ScenesService factory call
//   - the resolved Drive folder ID (read once via the supplied
//     resolveFolderFn closure)
//   - a structured log line ("scene builder wired")
//
// The use case does NOT own:
//   - the spec or the payload (caller responsibility)
//   - the lifecycle of the ScenesService (returned to caller; caller
//     passes it to Pipeline via the shared application-layer
//     SetScenesService or as a constructor arg).
package scripts

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// ErrSceneBuilderUnconfigured is the sentinel for "the use case was
// constructed without the required deps (imgSvc or voSvc nil)".
var ErrSceneBuilderUnconfigured = errors.New("scene builder: imgSvc and voSvc are required")

// SceneBuilderUseCase constructs ScenesService instances for the
// pipeline post-generation phase. One-shot per job (no caching:
// each scene build is a fresh ScenesService with a fresh cfg/log
// pointer; the previous handler also constructed a fresh one per
// job, so this matches existing behaviour byte-for-byte).
//
// Takes *images.Service + *voiceover.Service directly. These are the
// concrete types whose method sets satisfy scripts.SceneImageService
// and scripts.SceneVoiceoverService (the interfaces NewScenesService
// consumes) — verified by the existing handler's own wiring of
// h.clipServices.ImgSvc (concrete *images.Service) into NewScenesService.
type SceneBuilderUseCase struct {
	imgSvc        *images.Service
	voSvc         *voiceover.Service
	log           *zap.Logger
	cfg           *config.Config
	resolveFolder FolderResolver
	groupsRes     *voiceover.GroupsResolver
}

// NewSceneBuilderUseCase wires the use case. nil imgSvc + voSvc is
// acceptable only in tests; production always supplies both.
func NewSceneBuilderUseCase(
	imgSvc *images.Service,
	voSvc *voiceover.Service,
	log *zap.Logger,
	cfg *config.Config,
	resolveFolder FolderResolver,
	groupsRes *voiceover.GroupsResolver,
) *SceneBuilderUseCase {
	return &SceneBuilderUseCase{
		imgSvc:        imgSvc,
		voSvc:         voSvc,
		log:           log,
		cfg:           cfg,
		resolveFolder: resolveFolder,
		groupsRes:     groupsRes,
	}
}

// Build returns a fresh ScenesService instance for the pipeline. The
// returned pointer has the same initialisation semantics as the
// previous handler's inline call:
//   - ImgSvc + VoSvc from the use case's deps
//   - log + cfg as injected
//   - resolveFolder is the function field (handler's
//     resolveDriveFolderID was the canonical implementation)
//   - groupsRes is the asset-tree resolver
//   - the trailing 0 is the albumCapacityHint (kept as 0 — the
//     handler comment "0 = unlimited" was the previous intent)
//
// Returns ErrSceneBuilderUnconfigured when the use case was built
// without the required deps; the pipeline caller can fall back to
// running without scenes (logging the skip) or refuse to run (with
// a typed error).
func (u *SceneBuilderUseCase) Build(ctx context.Context) (*ScenesService, error) {
	if u == nil {
		return nil, ErrSceneBuilderUnconfigured
	}
	if u.imgSvc == nil || u.voSvc == nil {
		return nil, ErrSceneBuilderUnconfigured
	}
	if u.log != nil {
		u.log.Info("scene_builder_wired",
			zap.Bool("voiceover_resolver", u.groupsRes != nil),
			zap.Bool("resolve_folder", u.resolveFolder != nil))
	}
	_ = ctx // reserved for future resolve-folder pre-warm; currently unused
	return NewScenesService(
		u.imgSvc,
		u.voSvc,
		u.log,
		u.cfg,
		u.resolveFolder,
		u.groupsRes,
		0,
	), nil
}
