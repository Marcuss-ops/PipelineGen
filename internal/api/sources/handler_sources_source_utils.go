package sources

import (
	"context"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/upload/drive"

	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ValidateSource checks the source parameter against known sources and writes
// a BadRequest response if invalid. Returns true when the source is valid.
//
//	if !ValidateSource(c, source) { return }
func ValidateSource(c *gin.Context, source string) bool {
	if !artifacts.IsValidSource(source) {
		apiutil.BadRequest(c, "invalid source: "+source)
		return false
	}
	return true
}

// resolveRepo returns the appropriate repository for the given source.
// Uses centralized SourceResolver from artifacts.
func (h *Handler) resolveRepo(source string) *assets.ClipsRepository {
	resolver := artifacts.NewSourceResolver(h.artlistRepo, h.clipsRepo, h.stockRepo)
	return resolver.ResolveRepo(source)
}

// clipToAssetNode moved to internal/api/sources/clips/helpers.go in PR-A
// Phase 4 BULK. All clips/* handlers in the new subpackage are its sole
// callers; sources/ no longer needs it.

// voiceoverRecordToAssetNode converts a media.VoiceoverRecord to assets.AssetNode.
func voiceoverRecordToAssetNode(r *assets.Record) *assets.AssetNode {
	if r == nil {
		return nil
	}
	return &assets.AssetNode{
		ID:          r.ID,
		Source:      "voiceover",
		AssetID:     r.ID,
		Name:        r.Filename,
		Type:        "audio",
		ParentID:    "",
		Path:        r.Filename,
		IsFolder:    false,
		DriveFileID: r.DriveFileID,
		DriveLink:   r.DriveLink,
		Metadata:    "{}",
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// voiceoverRecordToClip delegates to the canonical converter in artifacts.
func voiceoverRecordToClip(rec *assets.Record) *asset.Asset {
	return artifacts.VoiceoverRecordToClip(rec)
}

// imageAssetToClip uses the canonical converter from artifacts.
func imageAssetToClip(a *asset.ImageAsset) *asset.Asset {
	return artifacts.ImageAssetToClip(a)
}

// verifyClip performs verification of a single clip and returns the result map.
func (h *Handler) verifyClip(ctx context.Context, source string, repo *assets.ClipsRepository, clip *asset.Asset) gin.H {
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
	if clip.LocalPath() != "" {
		if _, statErr := os.Stat(clip.LocalPath()); statErr == nil {
			hasLocalFile = true
			result["local_file"] = true
			result["local_path"] = clip.LocalPath()
		} else {
			result["local_file"] = false
			result["local_path"] = clip.LocalPath()
			result["local_error"] = "file not found: " + statErr.Error()
			result["issues"] = append(result["issues"].([]string), "local_file_missing")
		}
	} else {
		result["local_file"] = false
		result["issues"] = append(result["issues"].([]string), "local_path_empty")
	}

	// Check Drive link
	driveLink := clip.DriveLink()
	if driveLink == "" {
		driveLink = clip.DownloadLink()
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
	if clip.FileHash() != "" {
		result["hash"] = clip.FileHash()
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
				clip.SetFileHash(md5)
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
				} else if strings.ToLower(source) == "voiceover" && h.voiceoverRepo != nil {
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
	if clip.FolderID() != "" {
		result["folder_id"] = clip.FolderID()
	}
	if clip.FolderPath() != "" {
		result["folder_path"] = clip.FolderPath()
	}

	// Determine status based on available data
	status := "unknown"
	if clip.DriveLink() != "" || clip.DownloadLink() != "" {
		status = "processed"
	} else if clip.LocalPath() != "" {
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

// treeNodeToAssetNode moved to internal/api/sources/clips/helpers.go in PR-A
// Phase 4 BULK. The lone caller (clips/folder_tree.go) now sits in the
// clips subpackage alongside the helper.
