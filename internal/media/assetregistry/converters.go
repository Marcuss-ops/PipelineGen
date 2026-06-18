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

// cloneMetadata returns a shallow copy of src, or an empty map if src is nil.
func cloneMetadata(src map[string]any) map[string]any {
	if src == nil {
		return make(map[string]any)
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// safeStringSlice returns s if non-nil, or an empty slice.
func safeStringSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
