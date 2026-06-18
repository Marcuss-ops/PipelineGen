package assetregistry

import (
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
)

// VoiceoverRecordToClip converts a voiceover.Record to asset.MediaAsset.
func VoiceoverRecordToClip(rec *voiceovers.Record) *asset.MediaAsset {
	if rec == nil {
		return nil
	}
	name := rec.Filename
	if name == "" {
		name = rec.TextPreview
		if len(name) > 50 {
			name = name[:50]
		}
	}
	clip := &asset.MediaAsset{
		ID:          rec.ID,
		Name:        name,
		Filename:    rec.Filename,
		Source:      "voiceover",
		MediaType:   "audio",
		SearchTerms: []string{rec.TextPreview},
		CreatedAt:   rec.CreatedAt,
		UpdatedAt:   rec.UpdatedAt,
	}
	clip.SetFolderID(rec.FolderID)
	clip.SetFolderPath(rec.FolderPath)
	clip.SetDriveLink(rec.DriveLink)
	clip.SetDriveFileID(rec.DriveFileID)
	clip.SetDownloadLink(rec.DownloadLink)
	clip.SetFileHash(rec.FileHash)
	clip.SetLocalPath(rec.LocalPath)
	clip.SetMetadataJSON(rec.Metadata)
	return clip
}

// ImageAssetToClip converts an models.ImageAsset to asset.MediaAsset.
func ImageAssetToClip(assetItem *models.ImageAsset) *asset.MediaAsset {
	if assetItem == nil {
		return nil
	}
	name := assetItem.Description
	if name == "" {
		name = filepath.Base(assetItem.PathRel)
	}
	id := assetItem.SlugID
	if id == "" {
		id = assetItem.Hash
	}
	clip := &asset.MediaAsset{
		ID:          id,
		Name:        name,
		Filename:    filepath.Base(assetItem.PathRel),
		Source:      "images",
		MediaType:   "image",
		Tags:        assetItem.Tags,
		SearchTerms: []string{assetItem.Description},
		CreatedAt:   assetItem.CreatedAt,
		UpdatedAt:   assetItem.CreatedAt,
	}
	clip.SetDriveLink(assetItem.SourceURL)
	clip.SetDriveFileID(assetItem.DriveFileID)
	clip.SetFileHash(assetItem.Hash)
	clip.SetLocalPath(assetItem.PathRel)
	return clip
}

