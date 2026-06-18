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
		ID:           rec.ID,
		Name:         name,
		Filename:     rec.Filename,
		FolderID:     rec.FolderID,
		FolderPath:   rec.FolderPath,
		DriveLink:    rec.DriveLink,
		DriveFileID:  rec.DriveFileID,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		LocalPath:    rec.LocalPath,
		Source:       "voiceover",
		MediaType:    "audio",
		SearchTerms:  []string{rec.TextPreview},
		CreatedAt:    rec.CreatedAt,
		UpdatedAt:    rec.UpdatedAt,
	}
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
	return &asset.MediaAsset{
		ID:          id,
		Name:        name,
		Filename:    filepath.Base(assetItem.PathRel),
		DriveLink:   assetItem.SourceURL,
		DriveFileID: assetItem.DriveFileID,
		FileHash:    assetItem.Hash,
		LocalPath:   assetItem.PathRel,
		Source:      "images",
		MediaType:   "image",
		Tags:        assetItem.Tags,
		SearchTerms: []string{assetItem.Description},
		CreatedAt:   assetItem.CreatedAt,
		UpdatedAt:   assetItem.CreatedAt,
	}
}


// ToCanonical is an identity function during the transition.
// Since the canonical type (*asset.MediaAsset) is already the single source of truth,
// this simply returns its input unchanged.
//
// Deprecated: This function is a no-op. Remove all call sites, then delete this function.
// After all callers are migrated, this file should be removed entirely.
func ToCanonical(a *asset.MediaAsset) *asset.MediaAsset {
	return a
}

// ToLegacy is an identity function during the transition.
// Since models.MediaAsset has been deleted and all code now uses *asset.MediaAsset,
// this simply returns its input unchanged.
//
// Deprecated: This function is a no-op. Remove all call sites, then delete this function.
// After all callers are migrated, this file should be removed entirely.
func ToLegacy(a *asset.MediaAsset) *asset.MediaAsset {
	return a
}
