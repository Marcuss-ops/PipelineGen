package artlist

import (
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// toDomain converts a models.MediaAsset to the canonical asset.MediaAsset.
// Physical-location fields (LocalPath, DriveLink, etc.) are mapped from the
// legacy struct for backward compatibility. Once asset_locations is the
// source of truth, callers should use the locations repository instead.
func toDomain(m *models.MediaAsset) *asset.MediaAsset {
	if m == nil {
		return nil
	}
	a := &asset.MediaAsset{
		ID:             m.ID,
		Source:         m.Source,
		Name:           m.Name,
		Filename:       m.Filename,
		MediaType:      m.MediaType,
		Category:       m.Category,
		Group:          m.Group,
		SourceURL:      m.ExternalURL,
		ExternalURL:    m.ExternalURL,
		ClipPageURL:    m.ClipPageURL,
		ThumbnailURL:   m.ThumbURL,
		DurationMs:     int64(m.Duration),
		Tags:           m.Tags,
		SearchTerms:    m.SearchTerms,
		SearchText:     m.SearchText,
		LifecycleState: asset.LifecycleState(m.Status),
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
		DeletedAt:      m.DeletedAt,
	}
	if m.Metadata != nil {
		a.Metadata = make(map[string]any, len(m.Metadata))
		for k, v := range m.Metadata {
			a.Metadata[k] = v
		}
	}
	// Store location fields in metadata for backward compat during migration.
	// These will move to asset_locations once the migration is complete.
	if m.LocalPath != "" {
		a.SetMetadataString("_local_path", m.LocalPath)
	}
	if m.DriveLink != "" {
		a.SetMetadataString("_drive_link", m.DriveLink)
	}
	if m.DriveFileID != "" {
		a.SetMetadataString("_drive_file_id", m.DriveFileID)
	}
	if m.DownloadLink != "" {
		a.SetMetadataString("_download_link", m.DownloadLink)
	}
	if m.FileHash != "" {
		a.SetMetadataString("_file_hash", m.FileHash)
	}
	return a
}

// toDomainSlice converts a slice of models.MediaAsset to asset.MediaAsset.
func toDomainSlice(items []models.MediaAsset) []asset.MediaAsset {
	out := make([]asset.MediaAsset, 0, len(items))
	for i := range items {
		if a := toDomain(&items[i]); a != nil {
			out = append(out, *a)
		}
	}
	return out
}

// toDomainPtrSlice converts a slice of *models.MediaAsset to *asset.MediaAsset.
func toDomainPtrSlice(items []*models.MediaAsset) []*asset.MediaAsset {
	out := make([]*asset.MediaAsset, 0, len(items))
	for _, m := range items {
		if a := toDomain(m); a != nil {
			out = append(out, a)
		}
	}
	return out
}
