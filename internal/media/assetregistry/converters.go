package assetregistry

import (
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/repository/voiceovers"
)

// VoiceoverRecordToClip converts a voiceover.Record to models.Clip for unified handling.
// This is the canonical converter — do NOT create copies in handlers or services.
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

// ImageAssetToClip converts an models.ImageAsset to models.Clip for unified handling.
// Uses SlugID as ID (consistent with asset index) and Hash as FileHash.
// This is the canonical converter — do NOT create copies in handlers or services.
func ImageAssetToClip(assetItem *models.ImageAsset) *asset.MediaAsset {
	if assetItem == nil {
		return nil
	}
	name := assetItem.Description
	if name == "" {
		name = filepath.Base(assetItem.PathRel)
	}
	// Use SlugID as primary ID for consistency with the asset index.
	// Fall back to Hash if SlugID is empty.
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
