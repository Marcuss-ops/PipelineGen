package media

import (
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/apiutil"
	driveutil "velox/go-master/pkg/drive"
	"velox/go-master/pkg/models"
)

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
