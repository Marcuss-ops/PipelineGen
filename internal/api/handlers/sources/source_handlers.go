package sources

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
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
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/media"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/apiutil"
)

// Handler handles common media operations.
type Handler struct {
	artlistSvc     *artlist.Service
	youtubeSvc     *youtubeclip.Service
	voiceoverSvc   *voiceover.Service
	jobsSvc        *jobservice.Service
	catalogRepo    *catalog.Repository
	assetIndexSvc  *assetindex.Service
	artlistRepo    *clips.Repository
	clipsRepo      *clips.Repository
	stockRepo      *clips.Repository
	voiceoverRepo  *voiceovers.Repository
	imagesRepo     *images.Repository
	cleanupSvc     *drivecleanup.Service
	folderMemSvc   *foldermemory.Service
	assetTreeSvc   *assettree.Service
	driveUploader  *drive.Uploader
	mediaProcessor processor.Processor
	deletionSvc    *media.DeletionService
	log            *zap.Logger

	// Sub-handlers
	Voiceover *VoiceoverHandler
}

// NewHandler creates a new common media handler.
func NewHandler(
	artlistSvc *artlist.Service,
	youtubeSvc *youtubeclip.Service,
	voiceoverSvc *voiceover.Service,
	jobsSvc *jobservice.Service,
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
	h := &Handler{
		artlistSvc:     artlistSvc,
		youtubeSvc:     youtubeSvc,
		voiceoverSvc:   voiceoverSvc,
		jobsSvc:        jobsSvc,
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

	h.Voiceover = NewVoiceoverHandler(voiceoverSvc, jobsSvc)

	return h
}

// SetVoiceoverRepo sets the voiceover repository.
func (h *Handler) SetVoiceoverRepo(repo *voiceovers.Repository) {
	h.voiceoverRepo = repo
}

// SetImagesRepo sets the images repository.
func (h *Handler) SetImagesRepo(repo *images.Repository) {
	h.imagesRepo = repo
}

// RegisterRoutes registers media routes with source parameter.
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
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
	r.POST("/:source/bulk/tags/add", h.BulkAddTags)
	r.POST("/:source/bulk/tags/remove", h.BulkRemoveTags)

	// Source-level endpoints
	r.GET("/search", h.Search)
	r.GET("/:source/clips", h.ListClips)
	r.POST("/:source/reconcile", h.Reconcile)
	r.POST("/:source/cleanup", h.Cleanup)
	r.GET("/:source/folders", h.ListFolders)
	r.GET("/:source/folders/:id", h.FolderStatus)
	r.POST("/:source/folders/:id/manifest", h.RegenerateManifest)
	r.POST("/:source/folders/:id/trash", h.TrashFolder)
	r.POST("/:source/folders/:id/delete", h.DeleteFolder)
	r.GET("/:source/folders/:id/children", h.GetFolderChildren)
	r.GET("/:source/tree", h.GetTree)
	r.GET("/:source/breadcrumb", h.GetBreadcrumb)

	// Voiceover specific routes
	voiceover := r.Group("/voiceover")
	{
		h.Voiceover.RegisterRoutes(voiceover)
	}
}

// Reconcile reconciles database with Drive files.
func (h *Handler) Reconcile(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		FolderID string `json:"folder_id"`
		Fix      bool   `json:"fix"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, err.Error())
		return
	}

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	h.log.Info("Starting reconciliation", zap.String("source", source), zap.String("folder", req.FolderID))
	
	// Implementation placeholder for reconciliation logic
	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"message": "reconciliation started",
	})
}

// Cleanup removes orphan database records.
func (h *Handler) Cleanup(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		DryRun     bool `json:"dry_run"`
		CheckDrive bool `json:"check_drive"`
	}
	_ = c.ShouldBindJSON(&req)

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	clips, err := repo.ListClipsPaged(ctx, 10000, 0, "")
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	results := []gin.H{}
	deletedCount := 0

	for _, clip := range clips {
		verify := h.verifyClip(ctx, source, repo, clip)
		isOrphan := !verify["db"].(bool) || (!verify["local_file"].(bool) && !verify["has_drive_link"].(bool))
		
		if isOrphan {
			if !req.DryRun {
				if err := repo.DeleteClip(ctx, clip.ID); err == nil {
					deletedCount++
				}
			}
			results = append(results, gin.H{
				"id":     clip.ID,
				"name":   clip.Name,
				"reason": "orphan",
			})
		}
	}

	summary := fmt.Sprintf("Found %d orphans", len(results))
	if !req.DryRun {
		summary += fmt.Sprintf(", deleted %d", deletedCount)
	}

	apiutil.OK(c, gin.H{
		"ok":          true,
		"source":      source,
		"dry_run":     req.DryRun,
		"check_drive": req.CheckDrive,
		"checked":     len(results),
		"deleted":     deletedCount,
		"summary":     summary,
		"items":       results,
	})
}

// VerifyClip verifies DB, local file, and Drive coherence.
func (h *Handler) VerifyClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	// Handle Voiceover source
	if strings.ToLower(source) == "voiceover" && h.voiceoverRepo != nil {
		rec, err := h.voiceoverRepo.GetByID(c.Request.Context(), clipID)
		if err != nil {
			apiutil.NotFound(c, "voiceover not found")
			return
		}
		clip := voiceoverRecordToClip(rec)
		result := h.verifyClip(c.Request.Context(), source, nil, clip)
		c.JSON(http.StatusOK, result)
		return
	}

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	clip, err := repo.GetClip(c.Request.Context(), clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	result := h.verifyClip(c.Request.Context(), source, repo, clip)
	c.JSON(http.StatusOK, result)
}
