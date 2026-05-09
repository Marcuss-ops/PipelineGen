package media

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/images"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/pkg/apiutil"
	driveutil "velox/go-master/pkg/drive"
	"velox/go-master/pkg/models"
)

// DeleteDriveFileRequest represents a request to delete/trash a clip by Drive file ID or link.
type DeleteDriveFileRequest struct {
	Source    string `json:"source,omitempty"`
	DriveLink string `json:"drive_link"`
	FileID    string `json:"file_id"`
	DryRun    bool   `json:"dry_run"`
	Confirm   bool   `json:"confirm"`
	Mode      string `json:"mode"` // "trash" or "delete"
}

// DeleteDriveFileResult represents the result of a drive file delete/trash operation.
type DeleteDriveFileResult struct {
	OK           bool   `json:"ok"`
	Source       string `json:"source,omitempty"`
	ClipID       string `json:"clip_id,omitempty"`
	FileID       string `json:"file_id"`
	DriveLink    string `json:"drive_link,omitempty"`
	FoundDB      bool   `json:"found_db"`
	DryRun       bool   `json:"dry_run"`
	Action       string `json:"action,omitempty"`
	DriveDeleted bool   `json:"drive_deleted,omitempty"`
	DBDeleted    bool   `json:"db_deleted,omitempty"`
	Error        string `json:"error,omitempty"`
}

// ListClips lists all clips for a source with pagination and search.
func (h *CommonHandler) ListClips(c *gin.Context) {
	source := c.Param("source")
	sourceLower := strings.ToLower(source)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	q := c.Query("q")

	ctx := c.Request.Context()
	var allClips []*models.Clip

	if sourceLower == "voiceover" {
		if h.voiceoverRepo == nil {
			apiutil.BadRequest(c, "voiceover repo not available")
			return
		}
		records, err := h.voiceoverRepo.ListAll(ctx)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		for _, rec := range records {
			allClips = append(allClips, voiceoverRecordToClip(rec))
		}
	} else if sourceLower == "images" {
		if h.imagesRepo == nil {
			apiutil.BadRequest(c, "images repo not available")
			return
		}
		assets, err := h.imagesRepo.ListAll(ctx)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		for _, asset := range assets {
			allClips = append(allClips, imageAssetToClip(asset))
		}
	} else {
		repo := h.resolveRepo(source)
		if repo == nil {
			apiutil.BadRequest(c, "invalid source: "+source)
			return
		}
		clips, err := repo.ListClipsPaged(ctx, limit, offset, q)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		allClips = clips
	}

	total := 0
	if sourceLower == "voiceover" || sourceLower == "images" {
		total = len(allClips)
		if offset >= len(allClips) {
			allClips = []*models.Clip{}
		} else {
			end := offset + limit
			if end > len(allClips) {
				end = len(allClips)
			}
			allClips = allClips[offset:end]
		}
	} else {
		repo := h.resolveRepo(source)
		if repo != nil {
			if q == "" {
				total, _ = repo.CountClips(ctx)
			} else {
				// For search, total is len of results for now (since SearchClips isn't paged yet)
				total = len(allClips)
			}
		}
	}

	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"count":  len(allClips),
		"total":  total,
		"clips":  allClips,
	})
}

// Reconcile checks for mismatches between SQLite and Google Drive.
func (h *CommonHandler) Reconcile(c *gin.Context) {
	source := c.Param("source")
	sourceLower := strings.ToLower(source)

	var req struct {
		RootFolderID string `json:"root_folder_id"`
		DryRun       bool   `json:"dry_run"`
		CheckDrive   bool   `json:"check_drive"`
	}
	_ = c.ShouldBindJSON(&req)

	ctx := c.Request.Context()
	var allClips []*models.Clip
	var deletedCount int

	if sourceLower == "voiceover" {
		if h.voiceoverRepo == nil {
			apiutil.BadRequest(c, "voiceover repo not available")
			return
		}
		records, err := h.voiceoverRepo.ListAll(ctx)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		for _, rec := range records {
			allClips = append(allClips, voiceoverRecordToClip(rec))
		}
	} else if sourceLower == "images" {
		if h.imagesRepo == nil {
			apiutil.BadRequest(c, "images repo not available")
			return
		}
		assets, err := h.imagesRepo.ListAll(ctx)
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		for _, asset := range assets {
			allClips = append(allClips, imageAssetToClip(asset))
		}
	} else {
		repo := h.resolveRepo(source)
		if repo == nil {
			apiutil.BadRequest(c, "invalid source: "+source)
			return
		}
		clips, err := repo.ListClips(ctx, "")
		if err != nil {
			apiutil.InternalError(c, err)
			return
		}
		allClips = clips
	}

	var results []gin.H
	issueCount := make(map[string]int)

	for _, clip := range allClips {
		var result gin.H
		repo := h.resolveRepo(source)
		if repo != nil {
			result = h.verifyClip(ctx, source, repo, clip)
		} else {
			result = gin.H{
				"ok":      true,
				"source":  source,
				"clip_id": clip.ID,
				"issues":  []string{},
			}
			if clip.DriveLink != "" {
				result["has_drive_link"] = true
			} else {
				result["has_drive_link"] = false
				result["issues"] = append(result["issues"].([]string), "drive_link_missing")
			}
		}

		if req.CheckDrive && clip.DriveLink != "" {
			fileID := driveutil.FileIDFromLink(clip.DriveLink)
			if fileID != "" && h.driveUploader != nil && h.driveUploader.Service != nil {
				_, err := h.driveUploader.Service.Files.Get(fileID).Fields("id").Do()
				if err != nil {
					result["drive_file_missing"] = true
					result["issues"] = append(result["issues"].([]string), "drive_file_missing")

					if !req.DryRun {
						var delErr error
						if sourceLower == "voiceover" {
							delErr = h.voiceoverRepo.Delete(ctx, clip.ID)
						} else {
							repo := h.resolveRepo(source)
							if repo != nil {
								delErr = repo.DeleteClip(ctx, clip.ID)
							}
						}
						if delErr == nil {
							result["db_deleted"] = true
							deletedCount++
						}
					}
				}
			}
		}

		results = append(results, result)
		for _, issue := range result["issues"].([]string) {
			issueCount[issue]++
		}
	}

	summary := gin.H{
		"total_clips":  len(allClips),
		"issue_counts": issueCount,
		"deleted":      deletedCount,
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

// CleanupOrphans removes orphaned records and files.
func (h *CommonHandler) CleanupOrphans(c *gin.Context) {
	source := c.Param("source")

	repo := h.resolveRepo(source)
	if repo == nil {
		apiutil.BadRequest(c, "invalid source: "+source)
		return
	}

	var req struct {
		Target string `json:"target"`
		Where  string `json:"where"`
		DryRun bool   `json:"dry_run"`
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
				"clip_id":   clip.ID,
				"name":      clip.Name,
				"reasons":   reasons,
				"folder_id": clip.FolderID,
			})
		}
	}

	deleted := 0
	if !req.DryRun {
		for _, orphan := range orphans {
			clipID := orphan["clip_id"].(string)
			if req.Target == "db" || req.Target == "both" {
				if err := repo.DeleteClip(ctx, clipID); err == nil {
					deleted++
				}
			}
		}
	}

	apiutil.OK(c, gin.H{
		"ok":            true,
		"source":        source,
		"dry_run":       req.DryRun,
		"target":        req.Target,
		"where":         req.Where,
		"total_checked": len(clips),
		"orphans_found": len(orphans),
		"deleted":       deleted,
		"orphans":       orphans,
	})
}

// TrashByDriveFile trashes a clip by Drive file ID or link.
func (h *CommonHandler) TrashByDriveFile(c *gin.Context) {
	source := c.Param("source")

	var req DeleteDriveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request body")
		return
	}
	req.Source = source
	req.Mode = "trash"

	result, err := h.processDriveFileDelete(c.Request.Context(), &req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, result)
}

// DeleteByDriveFile permanently deletes a clip by Drive file ID or link.
func (h *CommonHandler) DeleteByDriveFile(c *gin.Context) {
	source := c.Param("source")

	var req DeleteDriveFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request body")
		return
	}
	req.Source = source
	req.Mode = "delete"

	result, err := h.processDriveFileDelete(c.Request.Context(), &req)
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	apiutil.OK(c, result)
}

// processDriveFileDelete handles the common logic for trash/delete by drive file.
func (h *CommonHandler) processDriveFileDelete(ctx context.Context, req *DeleteDriveFileRequest) (*DeleteDriveFileResult, error) {
	fileID := strings.TrimSpace(req.FileID)
	if fileID == "" && req.DriveLink != "" {
		fileID = driveutil.FileIDFromLink(req.DriveLink)
	}
	if fileID == "" {
		return nil, fmt.Errorf("drive file id or drive_link is required")
	}

	repos := map[string]interface{}{
		"artlist":   h.artlistRepo,
		"clips":     h.clipsRepo,
		"stock":     h.stockRepo,
		"voiceover": h.voiceoverRepo,
		"images":    h.imagesRepo,
	}

	if req.Source != "" && req.Source != "all" {
		repos = map[string]interface{}{req.Source: repos[req.Source]}
	}

	var foundSource string
	var foundClip *models.Clip

	for source, repo := range repos {
		if repo == nil {
			continue
		}

		switch source {
		case "artlist", "clips", "stock":
			clipRepo, ok := repo.(*clips.Repository)
			if !ok {
				continue
			}
			clip, err := clipRepo.GetClipByDriveFileID(ctx, fileID)
			if err != nil {
				return nil, fmt.Errorf("failed searching %s repo: %w", source, err)
			}
			if clip != nil {
				foundSource = source
				foundClip = clip
				goto Found
			}
		case "voiceover":
			voRepo, ok := repo.(*voiceovers.Repository)
			if !ok {
				continue
			}
			rec, err := voRepo.GetByDriveFileID(ctx, fileID)
			if err != nil {
				return nil, fmt.Errorf("failed searching %s repo: %w", source, err)
			}
			if rec != nil {
				foundSource = source
				foundClip = voiceoverRecordToClip(rec)
				goto Found
			}
		case "images":
			imgRepo, ok := repo.(*images.Repository)
			if !ok {
				continue
			}
			img, err := imgRepo.GetByDriveFileID(ctx, fileID)
			if err != nil {
				return nil, fmt.Errorf("failed searching %s repo: %w", source, err)
			}
			if img != nil {
				foundSource = source
				foundClip = imageAssetToClip(img)
				goto Found
			}
		}
	}
Found:

	if foundClip == nil {
		result := &DeleteDriveFileResult{
			OK:      false,
			FileID:  fileID,
			FoundDB: false,
			Error:   "clip not found in database",
		}
		return result, nil
	}

	if req.DryRun {
		result := &DeleteDriveFileResult{
			OK:        true,
			FileID:    fileID,
			FoundDB:   true,
			Source:    foundSource,
			ClipID:    foundClip.ID,
			DriveLink: foundClip.DriveLink,
			DryRun:    true,
			Action:    "would_" + req.Mode,
		}
		return result, nil
	}

	if !req.Confirm {
		return nil, fmt.Errorf("confirm=true is required for real deletion")
	}

	if h.driveUploader != nil {
		var driveErr error
		if req.Mode == "delete" {
			driveErr = h.driveUploader.DeleteFile(ctx, fileID)
		} else {
			driveErr = h.driveUploader.TrashFile(ctx, fileID)
		}
		if driveErr != nil {
			return nil, fmt.Errorf("failed to %s drive file: %w", req.Mode, driveErr)
		}
	}

	switch foundSource {
	case "artlist", "clips", "stock":
		clipRepo, ok := repos[foundSource].(*clips.Repository)
		if ok {
			if err := clipRepo.DeleteClip(ctx, foundClip.ID); err != nil {
				return nil, fmt.Errorf("drive file %sd but failed to delete db record: %w", req.Mode, err)
			}
		}
	case "voiceover":
		voRepo, ok := repos[foundSource].(*voiceovers.Repository)
		if ok {
			if err := voRepo.Delete(ctx, foundClip.ID); err != nil {
				return nil, fmt.Errorf("drive file %sd but failed to delete db record: %w", req.Mode, err)
			}
		}
	case "images":
		imgRepo, ok := repos[foundSource].(*images.Repository)
		if ok {
			id, _ := strconv.ParseInt(foundClip.ID, 10, 64)
			if err := imgRepo.Delete(ctx, id); err != nil {
				return nil, fmt.Errorf("drive file %sd but failed to delete db record: %w", req.Mode, err)
			}
		}
	}

	result := &DeleteDriveFileResult{
		OK:           true,
		FileID:       fileID,
		FoundDB:      true,
		Source:       foundSource,
		ClipID:       foundClip.ID,
		DriveLink:    foundClip.DriveLink,
		DriveDeleted: true,
		DBDeleted:    true,
		Action:       req.Mode + "d",
		DryRun:       req.DryRun,
	}
	return result, nil
}
