package sources

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/apiutil"
)

// FindDuplicates finds clips with the same file_hash across different sources.
func (h *Handler) FindDuplicates(c *gin.Context) {
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

	if clip.FileHash == "" {
		apiutil.OK(c, gin.H{
			"ok":         true,
			"source":      source,
			"clip_id":    clipID,
			"file_hash":   "",
			"duplicates": []gin.H{},
		})
		return
	}

	duplicates := []gin.H{}
	repos := map[string]*clips.Repository{
		"artlist": h.artlistRepo,
		"youtube": h.clipsRepo,
		"stock":   h.stockRepo,
	}

	for repoSource, srcRepo := range repos {
		if srcRepo == nil {
			continue
		}

		found, err := srcRepo.FindClipsByHash(c.Request.Context(), clip.FileHash)
		if err != nil {
			h.log.Warn("Failed to search duplicates in "+repoSource, zap.Error(err))
			continue
		}

		for _, dup := range found {
			if repoSource == source && dup.ID == clipID {
				continue
			}

			duplicates = append(duplicates, gin.H{
				"source":     repoSource,
				"id":         dup.ID,
				"name":       dup.Name,
				"drive_link": dup.DriveLink,
				"local_path": dup.LocalPath,
				"thumb_url":  dup.ThumbURL,
			})
		}
	}

	apiutil.OK(c, gin.H{
		"ok":         true,
		"source":      source,
		"clip_id":     clipID,
		"file_hash":   clip.FileHash,
		"duplicates":  duplicates,
	})
}
