package api

import (
	"context"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	assettreerepo "github.com/Marcuss-ops/PipelineGen/internal/repository/assettree"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/database/drive"
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
func (h *Handler) resolveRepo(source string) *clips.Repository {
	resolver := artifacts.NewSourceResolver(h.artlistRepo, h.clipsRepo, h.stockRepo)
	return resolver.ResolveRepo(source)
}

// clipToAssetNode converts a canonical assets.Asset to assettree.AssetNode
// for unified tree handling.
func clipToAssetNode(clip *assets.Asset) *assettreerepo.AssetNode {
	if clip == nil {
		return nil
	}
	nodeType := "file"
	if clip.IsFolder() {
		nodeType = "folder"
	} else if clip.MediaType != "" {
		nodeType = string(clip.MediaType)
	}

	return &assettreerepo.AssetNode{
		ID:          clip.ID,
		Source:      string(clip.Source),
		AssetID:     clip.ID,
		Name:        clip.Name,
		Type:        nodeType,
		ParentID:    clip.ParentFolderID(),
		Path:        clip.FolderPath(),
		Depth:       clip.Depth(),
		IsFolder:    clip.IsFolder(),
		DriveFileID: clip.DriveFileID(),
		DriveLink:   clip.DriveLink(),
		Metadata:    clip.MetadataJSON(),
		CreatedAt:   clip.CreatedAt,
		UpdatedAt:   clip.UpdatedAt,
		ChildCount:  clip.ChildCount(),
	}
}

// voiceoverRecordToAssetNode converts a models.VoiceoverRecord to assettree.AssetNode.
func voiceoverRecordToAssetNode(r *voiceovers.Record) *assettreerepo.AssetNode {
	if r == nil {
		return nil
	}
	return &assettreerepo.AssetNode{
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
func voiceoverRecordToClip(rec *voiceovers.Record) *assets.Asset {
	return artifacts.VoiceoverRecordToClip(rec)
}

// imageAssetToClip uses the canonical converter from artifacts.
func imageAssetToClip(a *models.ImageAsset) *assets.Asset {
	return artifacts.ImageAssetToClip(a)
}

// verifyClip performs verification of a single clip and returns the result map.
func (h *Handler) verifyClip(ctx context.Context, source string, repo *clips.Repository, clip *assets.Asset) gin.H {
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

func treeNodeToAssetNode(tn *assettreerepo.AssetNode) *models.AssetNode {
	if tn == nil {
		return nil
	}
	return &models.AssetNode{
		ID:          tn.ID,
		Source:      tn.Source,
		AssetID:     tn.AssetID,
		Name:        tn.Name,
		Type:        tn.Type,
		ParentID:    tn.ParentID,
		RootID:      tn.RootID,
		Path:        tn.Path,
		Depth:       tn.Depth,
		IsFolder:    tn.IsFolder,
		DriveFileID: tn.DriveFileID,
		DriveLink:   tn.DriveLink,
		Metadata:    tn.Metadata,
		ChildCount:  tn.ChildCount,
	}
}
