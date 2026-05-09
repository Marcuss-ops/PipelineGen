package media

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/foldermemory"
	"velox/go-master/internal/upload/drive"
)

// CommonHandler handles common media operations across different sources.
type CommonHandler struct {
	artlistRepo    *clips.Repository
	clipsRepo      *clips.Repository
	stockRepo      *clips.Repository
	voiceoverRepo  *voiceovers.Repository
	imagesRepo     *images.Repository
	cleanupSvc     *drivecleanup.Service
	folderMemSvc   *foldermemory.Service
	driveUploader  *drive.Uploader
	mediaProcessor processor.Processor
	log            *zap.Logger
}

// NewCommonHandler creates a new common media handler.
func NewCommonHandler(artlistRepo, clipsRepo, stockRepo *clips.Repository, cleanupSvc *drivecleanup.Service, folderMemSvc *foldermemory.Service, driveUploader *drive.Uploader, mediaProcessor processor.Processor, log *zap.Logger) *CommonHandler {
	return &CommonHandler{
		artlistRepo:    artlistRepo,
		clipsRepo:      clipsRepo,
		stockRepo:      stockRepo,
		cleanupSvc:     cleanupSvc,
		folderMemSvc:   folderMemSvc,
		driveUploader:  driveUploader,
		mediaProcessor: mediaProcessor,
		log:            log,
	}
}

// SetVoiceoverRepo sets the voiceover repository.
func (h *CommonHandler) SetVoiceoverRepo(repo *voiceovers.Repository) {
	h.voiceoverRepo = repo
}

// SetImagesRepo sets the images repository.
func (h *CommonHandler) SetImagesRepo(repo *images.Repository) {
	h.imagesRepo = repo
}

// RegisterRoutes registers media routes with source parameter.
func (h *CommonHandler) RegisterRoutes(r *gin.RouterGroup) {
	h.log.Info("Registering common media routes")
	
	// Clip-level endpoints
	r.POST("/:source/clips", h.CreateClip)
	r.GET("/:source/clips/:id", h.GetClip)
	r.PATCH("/:source/clips/:id", h.UpdateClip)
	r.POST("/:source/clips/:id/status", h.ClipStatus)
	r.POST("/:source/clips/:id/verify", h.VerifyClip)
	r.POST("/:source/clips/:id/trash", h.TrashClip)
	r.POST("/:source/clips/:id/delete", h.DeleteClip)
	r.POST("/:source/clips/:id/reupload", h.ReuploadClip)
	r.POST("/:source/clips/:id/reprocess", h.ReprocessClip)
	r.GET("/:source/clips/:id/duplicates", h.FindDuplicates)
	r.GET("/:source/clips/:id/download", h.DownloadClip)

	// Folder-level endpoints
	r.GET("/:source/folders", h.ListFolders)
	r.GET("/:source/folders/:id/children", h.GetFolderChildren)
	r.GET("/:source/folders/:id/status", h.FolderStatus)
	r.POST("/:source/folders/:id/regenerate-manifest", h.RegenerateManifest)
	r.POST("/:source/folders/:id/trash", h.TrashFolder)
	r.POST("/:source/folders/:id/delete", h.DeleteFolder)

	// Source-level endpoints
	r.GET("/:source/clips", h.ListClips)
	r.POST("/:source/reconcile", h.Reconcile)
	r.POST("/:source/cleanup-orphans", h.CleanupOrphans)

	// Drive file endpoints (operate by Drive file ID or link)
	r.POST("/:source/drive-file/trash", h.TrashByDriveFile)
	r.POST("/:source/drive-file/delete", h.DeleteByDriveFile)
}
