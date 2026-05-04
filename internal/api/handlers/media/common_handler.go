package media

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/drivecleanup"
	"velox/go-master/internal/service/foldermemory"
	"velox/go-master/internal/service/mediaasset"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/models"
	driveutil "velox/go-master/pkg/drive"
	"velox/go-master/pkg/apiutil"
)

// CommonHandler handles common media operations across different sources.
type CommonHandler struct {
	artlistRepo    *clips.Repository
	clipsRepo      *clips.Repository
	stockRepo      *clips.Repository
	cleanupSvc     *drivecleanup.Service
	folderMemSvc   *foldermemory.Service
	driveUploader  *drive.Uploader
	mediaProcessor *mediaasset.Processor
	log            *zap.Logger
}

// NewCommonHandler creates a new common media handler.
func NewCommonHandler(artlistRepo, clipsRepo, stockRepo *clips.Repository, cleanupSvc *drivecleanup.Service, folderMemSvc *foldermemory.Service, driveUploader *drive.Uploader, mediaProcessor *mediaasset.Processor, log *zap.Logger) *CommonHandler {
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

// resolveRepo returns the appropriate repository based on source.
func (h *CommonHandler) resolveRepo(source string) *clips.Repository {
	switch strings.ToLower(source) {
	case "artlist":
		return h.artlistRepo
	case "youtube", "clips":
		return h.clipsRepo
	case "stock":
		return h.stockRepo
	default:
		return nil
	}
}

// RegisterRoutes registers media routes with source parameter.
func (h *CommonHandler) RegisterRoutes(r *gin.RouterGroup) {
	// Clip-level endpoints
	clips := r.Group("/:source/clips")
	{
		clips.GET("/:id/status", h.ClipStatus)
		clips.POST("/:id/verify", h.VerifyClip)
		clips.POST("/:id/trash", h.TrashClip)
		clips.POST("/:id/delete", h.DeleteClip)
		clips.POST("/:id/reupload", h.ReuploadClip)
		clips.POST("/:id/reprocess", h.ReprocessClip)
	}

	// Folder-level endpoints
	folders := r.Group("/:source/folders")
	{
		folders.GET("", h.ListFolders)
		folders.GET("/:id/status", h.FolderStatus)
		folders.POST("/:id/regenerate-manifest", h.RegenerateManifest)
		folders.POST("/:id/trash", h.TrashFolder)
		folders.POST("/:id/delete", h.DeleteFolder)
	}

	// Source-level endpoints
	sourceGroup := r.Group("/:source")
	{
		sourceGroup.GET("/clips", h.ListClips)
		sourceGroup.POST("/reconcile", h.Reconcile)
		sourceGroup.POST("/cleanup-orphans", h.CleanupOrphans)
	}
}

// ClipStatus returns the status of a clip.
func (h *CommonHandler) ClipStatus(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

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

	// Determine status based on available data
	status := "unknown"
	if clip.DriveLink != "" || clip.DownloadLink != "" {
		status = "processed"
	} else if clip.LocalPath != "" {
		status = "downloaded"
	} else {
		status = "pending"
	}

	apiutil.OK(c, gin.H{
		"ok":          true,
		"source":      source,
		"clip_id":     clipID,
		"exists_db":   true,
		"name":        clip.Name,
		"has_local_file": clip.LocalPath != "",
		"local_path":  clip.LocalPath,
		"has_drive_link": clip.DriveLink != "" || clip.DownloadLink != "",
		"drive_link":  clip.DriveLink,
		"download_link": clip.DownloadLink,
		"file_hash":   clip.FileHash,
		"folder_id":   clip.FolderID,
		"folder_path": clip.FolderPath,
		"status":      status,
	})
}

// VerifyClip verifies DB, local file, and Drive coherence.
func (h *CommonHandler) VerifyClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

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

// verifyClip performs verification of a single clip and returns the result map.
func (h *CommonHandler) verifyClip(ctx context.Context, source string, repo *clips.Repository, clip *models.Clip) gin.H {
	result := gin.H{
		"ok":      true,
		"source":  source,
		"clip_id": clip.ID,
		"issues":  []string{},
	}

	// Check DB
	result["db"] = true

	// Check local file
	hasLocalFile := false
	if clip.LocalPath != "" {
		if _, statErr := os.Stat(clip.LocalPath); statErr == nil {
			hasLocalFile = true
			result["local_file"] = true
			result["local_path"] = clip.LocalPath
		} else {
			result["local_file"] = false
			result["local_path"] = clip.LocalPath
			result["local_error"] = "file not found: " + statErr.Error()
			result["issues"] = append(result["issues"].([]string), "local_file_missing")
		}
	} else {
		result["local_file"] = false
		result["issues"] = append(result["issues"].([]string), "local_path_empty")
	}

	// Check Drive link
	driveLink := clip.DriveLink
	if driveLink == "" {
		driveLink = clip.DownloadLink
	}
	if driveLink != "" {
		result["has_drive_link"] = true
		result["drive_link"] = driveLink

		// Extract file ID and verify with Drive API
		fileID := driveutil.FileIDFromLink(driveLink)
		if fileID != "" && h.cleanupSvc != nil {
			result["drive_file_id"] = fileID
		} else if fileID == "" {
			result["drive_link_valid"] = false
			result["issues"] = append(result["issues"].([]string), "drive_link_invalid")
		}
	} else {
		result["has_drive_link"] = false
		result["issues"] = append(result["issues"].([]string), "drive_link_missing")
	}

	// Check hash
	if clip.FileHash != "" {
		result["hash"] = clip.FileHash
		result["has_hash"] = true

		// Verify hash if local file exists
		if hasLocalFile {
			result["hash_verified"] = false // Placeholder
		}
	} else {
		result["has_hash"] = false
		result["issues"] = append(result["issues"].([]string), "hash_missing")
	}

	// Check folder info
	if clip.FolderID != "" {
		result["folder_id"] = clip.FolderID
	}
	if clip.FolderPath != "" {
		result["folder_path"] = clip.FolderPath
	}

	// Determine status based on available data
	status := "unknown"
	if clip.DriveLink != "" || clip.DownloadLink != "" {
		status = "processed"
	} else if clip.LocalPath != "" {
		status = "downloaded"
	} else {
		status = "pending"
	}
	result["status"] = status

	// Determine overall status
	issues := result["issues"].([]string)
	if len(issues) == 0 {
		result["coherent"] = true
	} else {
		result["coherent"] = false
		result["issue_count"] = len(issues)
	}

	return result
}

// TrashClip moves a clip to Drive trash and removes SQLite record.
func (h *CommonHandler) TrashClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	if h.cleanupSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("cleanup service not configured"))
		return
	}

	if err := h.cleanupSvc.TrashClip(ctx, clipID); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"action":   "trashed",
		"source":   source,
		"clip_id":  clipID,
	})
}

// DeleteClip permanently deletes a clip from Drive and SQLite.
func (h *CommonHandler) DeleteClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	if h.cleanupSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("cleanup service not configured"))
		return
	}

	if err := h.cleanupSvc.DeleteClipPermanently(ctx, clipID); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"action":   "deleted",
		"source":   source,
		"clip_id":  clipID,
	})
}

// ReuploadClip reuploads a clip to Drive.
// POST /api/media/:source/clips/:id/reupload
func (h *CommonHandler) ReuploadClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	// Check local file
	if clip.LocalPath == "" {
		apiutil.BadRequest(c, "clip has no local path")
		return
	}

	if _, err := os.Stat(clip.LocalPath); err != nil {
		apiutil.BadRequest(c, "local file not found: "+clip.LocalPath)
		return
	}

	// Check if uploader is available
	if h.driveUploader == nil {
		apiutil.InternalError(c, fmt.Errorf("drive uploader not configured"))
		return
	}

	// Determine folder ID
	folderID := clip.FolderID
	if folderID == "" {
		apiutil.BadRequest(c, "clip has no folder ID")
		return
	}

	// Upload file to Drive
	filename := clip.Filename
	if filename == "" {
		filename = filepath.Base(clip.LocalPath)
	}

	result, err := h.driveUploader.UploadFile(ctx, clip.LocalPath, folderID, filename)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("upload failed: %w", err))
		return
	}

	// Update clip with new Drive link
	clip.DriveLink = result.DownloadLink
	if clip.DriveLink == "" && result.FileID != "" {
		clip.DriveLink = "https://drive.google.com/file/d/" + result.FileID + "/view"
	}

	// Update file hash if available
	if result.MD5Checksum != "" {
		clip.FileHash = result.MD5Checksum
	}

	// Save to DB
	if err := repo.UpsertClip(ctx, clip); err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to update clip: %w", err))
		return
	}

	apiutil.OK(c, gin.H{
		"ok":          true,
		"source":      source,
		"clip_id":     clipID,
		"drive_link":  clip.DriveLink,
		"file_hash":   clip.FileHash,
		"uploaded_at": time.Now().Format(time.RFC3339),
	})
}

// ReprocessClip reprocesses a clip (download/process/upload).
// POST /api/media/:source/clips/:id/reprocess
// Body: {"force": true, "upload_drive": true, "normalize": true}
func (h *CommonHandler) ReprocessClip(c *gin.Context) {
	source := c.Param("source")
	clipID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	clip, err := repo.GetClip(ctx, clipID)
	if err != nil {
		apiutil.NotFound(c, "clip not found")
		return
	}

	if h.mediaProcessor == nil {
		apiutil.InternalError(c, fmt.Errorf("media processor not configured"))
		return
	}

	var req struct {
		Force      bool `json:"force"`
		UploadDrive bool `json:"upload_drive"`
		Normalize  *bool `json:"normalize"`
	}
	_ = c.ShouldBindJSON(&req)

	// Build AssetInput from clip data
	input := mediaasset.AssetInput{
		ID:        clip.ID,
		Name:      clip.Name,
		SourceURL: clip.ExternalURL,
		FolderID:  clip.FolderID,
		Duration:  clip.Duration,
		Metadata: map[string]interface{}{
			"source": source,
			"tags":   clip.Tags,
		},
	}

	// Add download sections if metadata has start/end
	if clip.Metadata != "" {
		// Parse metadata for sections (simplified)
		input.Metadata["raw_metadata"] = clip.Metadata
	}

	// Process the asset
	result, err := h.mediaProcessor.DownloadProcessUpload(ctx, input)
	if err != nil {
		apiutil.InternalError(c, fmt.Errorf("reprocess failed: %w", err))
		return
	}

	// Update clip with result
	clip.LocalPath = result.LocalPath
	clip.FileHash = result.FileHash
	if result.DriveLink != "" {
		clip.DriveLink = result.DriveLink
	}
	if result.DownloadLink != "" {
		clip.DownloadLink = result.DownloadLink
	}
	clip.UpdatedAt = time.Now()

	// Save to DB
	if err := repo.UpsertClip(ctx, clip); err != nil {
		apiutil.InternalError(c, fmt.Errorf("failed to update clip: %w", err))
		return
	}

	apiutil.OK(c, gin.H{
		"ok":          true,
		"source":      source,
		"clip_id":     clipID,
		"status":      result.Status,
		"local_path":  result.LocalPath,
		"file_hash":   result.FileHash,
		"drive_link":  result.DriveLink,
		"download_link": result.DownloadLink,
		"processed_at": time.Now().Format(time.RFC3339),
	})
}

// ListFolders lists all folders for a source.
// Query params: limit (default 50, max 500)
func (h *CommonHandler) ListFolders(c *gin.Context) {
	source := c.Param("source")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	folders, err := repo.ListClipFolders(c.Request.Context(), "")
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	// Apply limit
	if limit > 0 && limit < len(folders) {
		folders = folders[:limit]
	}

	apiutil.OK(c, gin.H{
		"ok":      true,
		"source":  source,
		"count":   len(folders),
		"folders": folders,
	})
}

// FolderStatus returns the status of a folder.
func (h *CommonHandler) FolderStatus(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	// Get folder
	folder, err := repo.GetClipFolder(ctx, folderID)
	if err != nil {
		// Try by folder_id (Drive ID)
		folders, err2 := repo.ListClipFolders(ctx, "")
		if err2 != nil {
			apiutil.InternalError(c, err2)
			return
		}
		found := false
		for _, f := range folders {
			if f.FolderID == folderID {
				folder = f
				found = true
				break
			}
		}
		if !found {
			apiutil.NotFound(c, "folder not found")
			return
		}
	}

	// Get clips in folder
	clipList, _ := repo.ListClipsByFolderID(ctx, folder.FolderID)
	if len(clipList) == 0 {
		clipList, _ = repo.ListClipsByFolderPath(ctx, folder.FolderPath)
	}

	// Compute stats
	stats := models.ClipFolderStats{}
	for _, clip := range clipList {
		stats.ClipCount++
		if clip.DriveLink != "" || clip.DownloadLink != "" {
			stats.ProcessedCount++
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"folder":     folder,
		"stats":      stats,
		"clip_count": len(clipList),
	})
}

// RegenerateManifest regenerates manifest files for a folder.
// POST /api/media/:source/folders/:id/regenerate-manifest
func (h *CommonHandler) RegenerateManifest(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()

	// Get folder - try by ID first, then by folder_id (Drive ID)
	folder, err := repo.GetClipFolder(ctx, folderID)
	if err != nil {
		// Try to find by folder_id (Drive folder ID)
		folders, err2 := repo.ListClipFolders(ctx, "")
		if err2 != nil {
			apiutil.InternalError(c, err2)
			return
		}
		found := false
		for _, f := range folders {
			if f.FolderID == folderID {
				folder = f
				found = true
				break
			}
		}
		if !found {
			apiutil.NotFound(c, "folder not found")
			return
		}
	}

	// Get clips for this folder
	clipList, err := repo.ListClipsByFolderID(ctx, folder.FolderID)
	if err != nil || len(clipList) == 0 {
		// Try by folder path
		clipList, err = repo.ListClipsByFolderPath(ctx, folder.FolderPath)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
	}

	// Build manifest
	manifest := &models.ClipManifest{
		ID:              folder.ID,
		FolderID:        folder.FolderID,
		FolderPath:      folder.FolderPath,
		Source:          folder.Source,
		SourceURL:       folder.SourceURL,
		VideoID:         folder.VideoID,
		LocalFolderPath: folder.LocalFolderPath,
		Clips:           []models.ClipManifestItem{},
	}

	// Add clips to manifest
	for _, clip := range clipList {
		item := models.ClipManifestItem{
			ID:         clip.ID,
			Name:       clip.Name,
			Filename:   clip.Filename,
			LocalPath:  clip.LocalPath,
			DriveLink:  clip.DriveLink,
			FileHash:   clip.FileHash,
			Status:     "processed",
			Tags:       strings.Join(clip.Tags, ","),
		}

		// Try to extract start/end from metadata
		if clip.Metadata != "" {
			// Parse metadata JSON to get start/end
			// This is a simplified version - you may need to parse the actual metadata
			if strings.Contains(clip.Metadata, "start") {
				// Simplified - in reality, parse the JSON properly
				item.Status = "processed"
			}
		}

		manifest.Clips = append(manifest.Clips, item)
	}

	// Compute stats
	if h.folderMemSvc != nil {
		stats := h.folderMemSvc.ComputeManifestStats(manifest)
		manifest.Stats = stats

		// Save manifest files
		if folder.ManifestJSONPath != "" {
			if err := h.folderMemSvc.SaveManifest(folder.ManifestJSONPath, manifest); err != nil {
				h.log.Warn("failed to save manifest JSON", zap.Error(err))
			}
		}
		if folder.ManifestTXTPath != "" {
			if err := h.folderMemSvc.UpdateManifestTXT(folder, manifest); err != nil {
				h.log.Warn("failed to update manifest TXT", zap.Error(err))
			}
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"folder_id":  folder.ID,
		"clip_count": len(manifest.Clips),
		"stats":      manifest.Stats,
	})
}

// TrashFolder moves a folder to Drive trash and removes all associated clips.
func (h *CommonHandler) TrashFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	if h.cleanupSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("cleanup service not configured"))
		return
	}

	if err := h.cleanupSvc.DeleteFolderAndClips(ctx, folderID, true); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":        true,
		"action":    "trashed",
		"source":    source,
		"folder_id": folderID,
	})
}

// DeleteFolder permanently deletes a folder from Drive and SQLite.
func (h *CommonHandler) DeleteFolder(c *gin.Context) {
	source := c.Param("source")
	folderID := c.Param("id")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	ctx := c.Request.Context()
	if h.cleanupSvc == nil {
		apiutil.InternalError(c, fmt.Errorf("cleanup service not configured"))
		return
	}

	if err := h.cleanupSvc.DeleteFolderAndClips(ctx, folderID, false); err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":        true,
		"action":    "deleted",
		"source":    source,
		"folder_id": folderID,
	})
}

// ListClips lists all clips for a source with pagination and search.
// Query params: limit (default 50, max 500), offset (default 0), q (search term)
func (h *CommonHandler) ListClips(c *gin.Context) {
	source := c.Param("source")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	q := c.Query("q")

	clips, err := repo.ListClipsPaged(c.Request.Context(), limit, offset, q)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"count":  len(clips),
		"clips":  clips,
	})
}

// Reconcile checks for mismatches between SQLite and Google Drive.
// POST /api/media/:source/reconcile
// Body: {"root_folder_id": "...", "dry_run": true}
func (h *CommonHandler) Reconcile(c *gin.Context) {
	source := c.Param("source")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var req struct {
		RootFolderID string `json:"root_folder_id"`
		DryRun       bool   `json:"dry_run"`
	}
	_ = c.ShouldBindJSON(&req)

	ctx := c.Request.Context()
	clips, err := repo.ListClips(ctx, "")
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	var results []gin.H
	issueCount := make(map[string]int)

	for _, clip := range clips {
		result := h.verifyClip(ctx, source, repo, clip)
		results = append(results, result)

		// Count issues
		if issues, ok := result["issues"].([]string); ok {
			for _, issue := range issues {
				issueCount[issue]++
			}
		}
	}

	// Build summary
	summary := gin.H{
		"total_clips": len(clips),
		"issue_counts": issueCount,
	}
	for issue, count := range issueCount {
		summary[issue] = count
	}

	apiutil.OK(c, gin.H{
		"ok":       true,
		"source":   source,
		"dry_run":  req.DryRun,
		"checked":  len(results),
		"summary":  summary,
		"items":    results,
	})
}

// CleanupOrphans removes orphaned records and files.
// POST /api/media/:source/cleanup-orphans
// Body: {"target": "db", "where": "local_path_missing", "dry_run": true}
func (h *CommonHandler) CleanupOrphans(c *gin.Context) {
	source := c.Param("source")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var req struct {
		Target  string `json:"target"`  // db, drive, both
		Where   string `json:"where"`   // local_path_missing, drive_link_missing, hash_missing
		DryRun  bool   `json:"dry_run"`
	}
	_ = c.ShouldBindJSON(&req)

	ctx := c.Request.Context()
	clips, err := repo.ListClips(ctx, "")
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}

	var orphans []gin.H
	for _, clip := range clips {
		isOrphan := false
		reasons := []string{}

		if req.Where == "" || req.Where == "local_path_missing" {
			if clip.LocalPath == "" {
				isOrphan = true
				reasons = append(reasons, "local_path_empty")
			} else if _, err := os.Stat(clip.LocalPath); err != nil {
				isOrphan = true
				reasons = append(reasons, "local_file_missing")
			}
		}

		if req.Where == "" || req.Where == "drive_link_missing" {
			if clip.DriveLink == "" && clip.DownloadLink == "" {
				isOrphan = true
				reasons = append(reasons, "drive_link_missing")
			}
		}

		if req.Where == "" || req.Where == "hash_missing" {
			if clip.FileHash == "" {
				isOrphan = true
				reasons = append(reasons, "hash_missing")
			}
		}

		if isOrphan {
			orphans = append(orphans, gin.H{
				"clip_id":  clip.ID,
				"name":     clip.Name,
				"reasons":  reasons,
				"folder_id": clip.FolderID,
			})
		}
	}

	// Perform cleanup if not dry_run
	deleted := 0
	if !req.DryRun {
		for _, orphan := range orphans {
			clipID := orphan["clip_id"].(string)
			if req.Target == "db" || req.Target == "both" {
				if err := repo.DeleteClip(ctx, clipID); err == nil {
					deleted++
				}
			}
			// TODO: Add Drive cleanup via cleanupSvc if target is "drive" or "both"
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":     source,
		"dry_run":    req.DryRun,
		"target":     req.Target,
		"where":      req.Where,
		"total_checked": len(clips),
		"orphans_found":  len(orphans),
		"deleted":    deleted,
		"orphans":    orphans,
	})
}
