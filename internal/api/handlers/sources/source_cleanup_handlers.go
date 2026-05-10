package sources

import (
	"os"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/apiutil"
)

// CleanupOrphans removes orphaned records and files.
func (h *Handler) CleanupOrphans(c *gin.Context) {
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
