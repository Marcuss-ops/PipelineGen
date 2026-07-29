package artifacts

import (
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
)

// VoiceoverRecordToClip converts a voiceover.Record to asset.Asset.
func VoiceoverRecordToClip(rec *assets.Record) *asset.Asset {
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
	clip := &asset.Asset{
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

// ImageAssetToClip converts an asset.ImageAsset to asset.Asset.
func ImageAssetToClip(assetItem *asset.ImageAsset) *asset.Asset {
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
	clip := &asset.Asset{
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
