package assetregistry

import (
	"path/filepath"

	"velox/go-master/internal/repository/voiceovers"
	"velox/go-master/internal/media/models"
)

// VoiceoverRecordToClip converts a voiceover.Record to models.Clip for unified handling.
// This is the canonical converter — do NOT create copies in handlers or services.
func VoiceoverRecordToClip(rec *voiceovers.Record) *models.MediaAsset {
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
	clip := &models.MediaAsset{
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
// Uses SlugID as ID (consistent with admin UI) and Hash as FileHash.
// This is the canonical converter — do NOT create copies in handlers or services.
func ImageAssetToClip(asset *models.ImageAsset) *models.MediaAsset {
	if asset == nil {
		return nil
	}
	name := asset.Description
	if name == "" {
		name = filepath.Base(asset.PathRel)
	}
	// Use SlugID as primary ID for consistency with the asset index.
	// Fall back to Hash if SlugID is empty.
	id := asset.SlugID
	if id == "" {
		id = asset.Hash
	}
	return &models.MediaAsset{
		ID:          id,
		Name:        name,
		Filename:    filepath.Base(asset.PathRel),
		DriveLink:   asset.SourceURL,
		DriveFileID: asset.DriveFileID,
		FileHash:    asset.Hash,
		LocalPath:   asset.PathRel,
		Source:      "images",
		MediaType:   "image",
		Tags:        asset.Tags,
		SearchTerms: []string{asset.Description},
		CreatedAt:   asset.CreatedAt,
		UpdatedAt:   asset.CreatedAt,
	}
}
