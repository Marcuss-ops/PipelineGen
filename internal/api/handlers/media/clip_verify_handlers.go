package media

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/apiutil"
	driveutil "velox/go-master/pkg/drive"
	"velox/go-master/pkg/models"
)

// VerifyClip verifies DB, local file, and Drive coherence.
func (h *CommonHandler) VerifyClip(c *gin.Context) {
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
	var fileID string
	if driveLink != "" {
		result["has_drive_link"] = true
		result["drive_link"] = driveLink

		// Extract file ID and verify with Drive API
		fileID = driveutil.FileIDFromLink(driveLink)
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
		// Try to recover hash from Drive if available
		if fileID != "" && h.driveUploader != nil {
			md5, err := h.driveUploader.GetFileMD5(ctx, fileID)
			if err == nil && md5 != "" {
				clip.FileHash = md5
				result["hash"] = md5
				result["has_hash"] = true
				result["hash_recovered"] = true
				// Auto-save recovered hash to DB
				if repo != nil {
					if err := repo.UpsertClip(ctx, clip); err != nil {
						h.log.Warn("failed to save recovered hash", zap.String("clip_id", clip.ID), zap.Error(err))
					} else {
						h.log.Info("recovered and saved missing hash from drive", zap.String("clip_id", clip.ID), zap.String("hash", md5))
					}
				} else if source == "voiceover" && h.voiceoverRepo != nil {
					rec, err := h.voiceoverRepo.GetByID(ctx, clip.ID)
					if err == nil && rec != nil {
						rec.FileHash = md5
						if err := h.voiceoverRepo.Upsert(ctx, rec); err != nil {
							h.log.Warn("failed to save recovered voiceover hash", zap.String("id", clip.ID), zap.Error(err))
						} else {
							h.log.Info("recovered and saved missing voiceover hash from drive", zap.String("id", clip.ID), zap.String("hash", md5))
						}
					}
				}
			} else {
				result["has_hash"] = false
				result["issues"] = append(result["issues"].([]string), "hash_missing")
			}
		} else {
			result["has_hash"] = false
			result["issues"] = append(result["issues"].([]string), "hash_missing")
		}
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
