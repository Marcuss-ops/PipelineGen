package assets

import (
	"go.uber.org/zap"

	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assettree"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/foldermemory"
	"velox/go-master/internal/service/media"
	"velox/go-master/internal/upload/drive"
)

// Handler handles unified asset operations (search, tree, folders, etc.)
type Handler struct {
	// Search & Index
	artlistSvc    *artlist.Service
	catalogRepo   *catalog.Repository
	assetIndexSvc *assetindex.Service
	
	// Media Repositories (for folder/clip resolution)
	artlistRepo   *clips.Repository
	clipsRepo     *clips.Repository
	stockRepo     *clips.Repository
	voiceoverRepo *voiceovers.Repository
	imagesRepo    *images.Repository
	
	// Services
	cleanupSvc     *drivecleanup.Service
	folderMemSvc   *foldermemory.Service
	assetTreeSvc   *assettree.Service
	driveUploader  *drive.Uploader
	mediaProcessor processor.Processor
	deletionSvc    *media.DeletionService
	
	log *zap.Logger
}

// NewHandler creates a new assets handler
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
	return &Handler{
		artlistSvc:     artlistSvc,
		catalogRepo:    catalogRepo,
		assetIndexSvc:  assetIndexSvc,
		artlistRepo:    artlistRepo,
		clipsRepo:      clipsRepo,
		stockRepo:      stockRepo,
		cleanupSvc:     cleanupSvc,
		folderMemSvc:   folderMemSvc,
		assetTreeSvc:   assetTreeSvc,
		driveUploader:  driveUploader,
		mediaProcessor: mediaProcessor,
		deletionSvc:    deletionSvc,
		log:            log,
	}
}

// SetVoiceoverRepo sets the voiceover repository.
func (h *Handler) SetVoiceoverRepo(repo *voiceovers.Repository) {
	h.voiceoverRepo = repo
}

// SetImagesRepo sets the images repository.
func (h *Handler) SetImagesRepo(repo *images.Repository) {
	h.imagesRepo = repo
}
