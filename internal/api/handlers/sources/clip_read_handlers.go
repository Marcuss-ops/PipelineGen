package sources

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"velox/go-master/pkg/apiutil"
	"velox/go-master/pkg/models"
)

// GetClip returns a single clip.
func (h *Handler) GetClip(c *gin.Context) {
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
		apiutil.OK(c, gin.H{"ok": true, "source": source, "clip": clip})
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

	apiutil.OK(c, gin.H{
		"ok":     true,
		"source": source,
		"clip":   clip,
	})
}

// ClipStatus returns the status of a clip.
func (h *Handler) ClipStatus(c *gin.Context) {
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
		"ok":             true,
		"source":         source,
		"clip_id":        clipID,
		"exists_db":      true,
		"name":           clip.Name,
		"has_local_file": clip.LocalPath != "",
		"local_path":     clip.LocalPath,
		"has_drive_link": clip.DriveLink != "" || clip.DownloadLink != "",
		"drive_link":     clip.DriveLink,
		"download_link":  clip.DownloadLink,
		"file_hash":      clip.FileHash,
		"folder_id":      clip.FolderID,
		"folder_path":    clip.FolderPath,
		"status":         status,
	})
}

// ListClips lists all clips for a source with pagination and search.
func (h *Handler) ListClips(c *gin.Context) {
	source := c.Param("source")
	sourceLower := strings.ToLower(source)

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	q := c.Query("q")

	ctx := c.Request.Context()
	var allClips []*models.MediaAsset

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
		clips, err := repo.ListClipsPaged(ctx, source, limit, offset, q)
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
			allClips = []*models.MediaAsset{}
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
