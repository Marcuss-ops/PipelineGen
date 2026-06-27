// Package scripts — scene_builder_usecase is the use case that
// constructs the ScenesService for the post-generation phase 2 fan-out.
package usecase

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── ScenesService stub (PR G — placeholder; canonical impl deleted) ─────
// Scripts.NewScenesService was deleted from origin/main and will be
// re-introduced when the scene pipeline is re-constituted. Until
// then this stub returns an empty struct so the use case compiles
// without producing fake availability for the actual scene pipeline.

type ScenesService struct{}

func NewScenesService(
	imgSvc interface{},
	voSvc interface{},
	log interface{},
	cfg interface{},
	resolveFolder adapters.FolderResolver,
	groupsRes interface{},
	capacityHint int,
) *ScenesService {
	_ = imgSvc
	_ = voSvc
	_ = log
	_ = cfg
	_ = resolveFolder
	_ = groupsRes
	_ = capacityHint
	return &ScenesService{}
}

// ErrSceneBuilderUnconfigured is the sentinel for "the use case was
// constructed without the required deps (imgSvc or voSvc nil)".
var ErrSceneBuilderUnconfigured = errors.New("scene builder: imgSvc and voSvc are required")

// SceneBuilderUseCase constructs ScenesService instances for the pipeline.
type SceneBuilderUseCase struct {
	imgSvc        *images.Service
	voSvc         *voiceover.Service
	log           *zap.Logger
	cfg           *config.Config
	resolveFolder adapters.FolderResolver
	groupsRes     *voiceover.GroupsResolver
}

// NewSceneBuilderUseCase wires the use case.
func NewSceneBuilderUseCase(
	imgSvc *images.Service,
	voSvc *voiceover.Service,
	log *zap.Logger,
	cfg *config.Config,
	resolveFolder adapters.FolderResolver,
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

// Build returns a fresh ScenesService instance.
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
	_ = ctx
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
