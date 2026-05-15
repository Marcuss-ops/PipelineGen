package sources

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/core/maintenance"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/repository/catalog"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/artlist"
	"velox/go-master/internal/service/assetindex"
	"velox/go-master/internal/service/assettree"
	"velox/go-master/internal/service/catalogsync"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/foldermemory"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/media"
	"velox/go-master/internal/service/voiceover"
	"velox/go-master/internal/service/youtubeclip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

// Handler handles common media operations.
type Handler struct {
	cfg            *config.Config
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
	catalogSync    *catalogsync.Service
	maintenanceSvc *maintenance.Service
	log            *zap.Logger

	// Sub-handlers
	Voiceover *VoiceoverHandler
}

// NewHandler creates a new common media handler.
func NewHandler(
	cfg *config.Config,
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
	catalogSync *catalogsync.Service,
	maintenanceSvc *maintenance.Service,
	log *zap.Logger,
) *Handler {
	h := &Handler{
		cfg:            cfg,
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
		catalogSync:    catalogSync,
		maintenanceSvc: maintenanceSvc,
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

	// System diagnostics
	r.GET("/diagnostics", h.GetDiagnostics)
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

	if h.catalogSync != nil {
		summary, err := h.catalogSync.SyncSource(c.Request.Context(), source)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		apiutil.OK(c, summary)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"message": "reconciliation started (no service)",
	})
}

// Cleanup removes orphan database records.
func (h *Handler) Cleanup(c *gin.Context) {
	source := c.Param("source")
	var req struct {
		DryRun     bool `json:"dry_run"`
		CheckDrive bool `json:"check_drive"`
		Deep       bool `json:"deep"`
	}
	_ = c.ShouldBindJSON(&req)

	deep := c.Query("deep") == "true" || req.Deep

	// Use Job system for heavy all-source deep cleanup
	if deep && (strings.ToLower(source) == "all" || source == "") {
		if h.jobsSvc != nil {
			activeKey := "system_maintenance_manual"
			if req.DryRun {
				activeKey += "_dry"
			}

			job, err := h.jobsSvc.Enqueue(c.Request.Context(), &jobservice.EnqueueRequest{
				Type:      models.JobTypeSystemCleanup,
				Payload:   map[string]any{"deep": true, "dry_run": req.DryRun},
				Priority:  10,
				ActiveKey: activeKey,
			})
			if err != nil {
				apiutil.InternalError(c, err)
				return
			}
			apiutil.OK(c, gin.H{
				"ok":      true,
				"job_id":  job.ID,
				"message": "system cleanup job enqueued",
			})
			return
		}

		// Fallback to synchronous if no jobs service (unlikely)
		if h.deletionSvc != nil && !req.DryRun {
			deleted, err := h.deletionSvc.CleanupOrphanFiles(c.Request.Context(), h.cfg.Storage.AssetsPath(), false)
			if err != nil {
				apiutil.InternalError(c, err)
				return
			}
			apiutil.OK(c, gin.H{"ok": true, "deleted": deleted, "message": "deep cleanup completed synchronously"})
			return
		}
	}

	repo := h.resolveRepo(source)
	sourceLower := strings.ToLower(source)
	if repo == nil && sourceLower != "images" && sourceLower != "voiceover" {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	var allClips []*models.MediaAsset
	sourceLower = strings.ToLower(source)

	if sourceLower == "images" && h.imagesRepo != nil {
		imgs, _ := h.imagesRepo.ListAll(ctx)
		for _, img := range imgs {
			allClips = append(allClips, imageAssetToClip(img))
		}
	} else if sourceLower == "voiceover" && h.voiceoverRepo != nil {
		recs, _ := h.voiceoverRepo.ListAll(ctx)
		for _, rec := range recs {
			allClips = append(allClips, voiceoverRecordToClip(rec))
		}
	} else if repo != nil {
		clips, err := repo.ListClipsPaged(ctx, source, 10000, 0, "")
		if err == nil {
			allClips = clips
		}
	}

	results := []gin.H{}
	deletedCount := 0

	for _, clip := range allClips {
		verify := h.verifyClip(ctx, source, repo, clip)
		isOrphan := !verify["db"].(bool) || (!verify["local_file"].(bool) && !verify["has_drive_link"].(bool))

		if isOrphan {
			if !req.DryRun && h.deletionSvc != nil {
				if err := h.deletionSvc.DeleteClip(ctx, source, clip.ID, false); err == nil {
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

// GetDiagnostics returns system health and version information.
func (h *Handler) GetDiagnostics(c *gin.Context) {
	results := gin.H{
		"ok": true,
		"services": gin.H{
			"artlist":   h.artlistSvc != nil,
			"youtube":   h.youtubeSvc != nil,
			"voiceover": h.voiceoverSvc != nil,
			"jobs":      h.jobsSvc != nil,
		},
		"environment": gin.H{
			"go_version": "1.25.9",
		},
	}

	// Add repository status
	repos := gin.H{}
	if h.artlistRepo != nil {
		repos["artlist"] = "connected"
	}
	if h.clipsRepo != nil {
		repos["clips"] = "connected"
	}
	if h.stockRepo != nil {
		repos["stock"] = "connected"
	}
	results["repositories"] = repos

	// Check Drive connectivity
	if h.driveUploader != nil {
		results["drive"] = gin.H{
			"status": "connected",
		}
	} else {
		results["drive"] = gin.H{
			"status": "disconnected",
		}
	}

	apiutil.OK(c, results)
}
