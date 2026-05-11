package sources

import (
	"context"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"velox/go-master/internal/repository/clips"
	assettreerepo "velox/go-master/internal/repository/assettree"
	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/service/mediaregistry"
	"velox/go-master/pkg/models"
	driveutil "velox/go-master/pkg/drive"
)

// resolveRepo returns the appropriate repository for the given source.
// Uses centralized SourceResolver from mediaregistry.
func (h *Handler) resolveRepo(source string) *clips.Repository {
	resolver := mediaregistry.NewSourceResolver(h.artlistRepo, h.clipsRepo, h.stockRepo)
	return resolver.ResolveRepo(source)
}

// clipToAssetNode converts a models.Clip to assettree.AssetNode for unified tree handling.
func clipToAssetNode(clip *models.Clip) *assettreerepo.AssetNode {
	if clip == nil {
		return nil
	}
	nodeType := "file"
	if clip.IsFolder {
		nodeType = "folder"
	} else if clip.MediaType != "" {
		nodeType = clip.MediaType
	}

	return &assettreerepo.AssetNode{
		ID:          clip.ID,
		Source:      clip.Source,
		AssetID:     clip.ID,
		Name:        clip.Name,
		Type:        nodeType,
		ParentID:    clip.ParentFolderID,
		Path:        clip.FolderPath,
		Depth:       clip.Depth,
		IsFolder:    clip.IsFolder,
		DriveFileID: clip.DriveFileID,
		DriveLink:   clip.DriveLink,
		Metadata:    clip.Metadata,
		CreatedAt:   clip.CreatedAt,
		UpdatedAt:   clip.UpdatedAt,
		ChildCount:  clip.ChildCount,
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

// voiceoverRecordToClip delegates to the canonical converter in mediaregistry.
func voiceoverRecordToClip(rec *voiceovers.Record) *models.Clip {
	return mediaregistry.VoiceoverRecordToClip(rec)
}

// imageAssetToClip uses the canonical converter from mediaregistry.
func imageAssetToClip(asset *models.ImageAsset) *models.Clip {
	return mediaregistry.ImageAssetToClip(asset)
}

// verifyClip performs verification of a single clip and returns the result map.
func (h *Handler) verifyClip(ctx context.Context, source string, repo *clips.Repository, clip *models.Clip) gin.H {
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
