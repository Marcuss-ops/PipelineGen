package artifacts

import (
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
)

// VoiceoverRecordToClip converts a voiceover.Record to assets.Asset.
func VoiceoverRecordToClip(rec *voiceovers.Record) *assets.Asset {
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
	clip := &assets.Asset{
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

// ImageAssetToClip converts an models.ImageAsset to assets.Asset.
func ImageAssetToClip(assetItem *models.ImageAsset) *assets.Asset {
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
	clip := &assets.Asset{
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

