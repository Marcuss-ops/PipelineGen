package assets

import (
	"go.uber.org/zap"

	sources "velox/go-master/internal/api/handlers/sources"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assettree"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/foldermemory"
	"velox/go-master/internal/service/media"
	"velox/go-master/internal/upload/drive"
)

type Handler = sources.Handler

func NewHandler(
	artlistSvc *artlist.Service,
	catalogRepo *catalog.Repository,
	assetIndexSvc *assetindex.Service,
	artlistRepo, clipsRepo, stockRepo *clips.Repository,
	cleanupSvc *drivecleanup.Service,
	folderMemSvc *foldermemory.Service,
	assetTreeSvc *assettree.Service,
	driveUploader *drive.Uploader,
	mediaProcessor processor.Processor,
	deletionSvc *media.DeletionService,
	log *zap.Logger,
) *Handler {
	return sources.NewHandler(
		artlistSvc,
		catalogRepo,
		assetIndexSvc,
		artlistRepo,
		clipsRepo,
		stockRepo,
		cleanupSvc,
		folderMemSvc,
		assetTreeSvc,
		driveUploader,
		mediaProcessor,
		deletionSvc,
		log,
	)
}
