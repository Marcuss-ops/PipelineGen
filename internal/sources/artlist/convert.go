package artlist

import (
	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/media/models"
)

// toDomain converts a models.MediaAsset to the canonical asset.MediaAsset.
// PR12b: physical-location fields (LocalPath, DriveLink, DriveFileID,
// DownloadLink, FileHash) and Status / FolderID are mapped DIRECTLY into the
// canonical struct fields so that assetrepo.Upsert writes them into the
// matching legacy + canonical DB columns. The earlier approach of storing
// these in `asset.MediaAsset.Metadata` as `_local_path` etc. left the
// struct fields empty, causing legacy readers (clips.Repository) to see
// empty `local_path`/`drive_link` columns after a canonical write.
//
// Until asset_locations takes over (a later PR that retires the legacy
// columns entirely), this bridge keeps the dual-mapping contract explicit:
// struct fields for canonical round-trip, and the smaller metadata window
// for any genuinely user-supplied metadata.
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
		// PR12b: physical-location fields written directly so the legacy DB
		// columns stay populated for clips.Repository readers until the
		// asset_locations table takes over.
		LocalPath:    m.LocalPath,
		DriveLink:    m.DriveLink,
		DriveFileID:  m.DriveFileID,
		DownloadLink: m.DownloadLink,
		FileHash:     m.FileHash,
		Status:       m.Status,
		FolderID:     m.FolderID,
	}
	if m.Metadata != nil {
		a.Metadata = make(map[string]any, len(m.Metadata)+16)
		for k, v := range m.Metadata {
			a.Metadata[k] = v
		}
	}
	// PR12b transitional dual-write. clips.Repository.scan.go (the legacy
	// reader) reads DriveLink/LocalPath/DownloadLink/FileHash AND every
	// other typed field above from metadata_json.$.key via
	// GetMetadataString, not from the typed columns. Until that scanner
	// is upgraded to read typed columns (planned post-PR12c.1), we keep
	// the dual-write here in the bridge so legacy readers continue to
	// observe the canonical values. Cost: ~16 extra metadata_json keys
	// per row. Win: no behavior shift in clips.Repository callers.
	if a.Metadata == nil {
		a.Metadata = make(map[string]any, 16)
	}
	a.Metadata["filename"] = m.Filename
	a.Metadata["media_type"] = m.MediaType
	a.Metadata["category"] = m.Category
	a.Metadata["group_name"] = m.Group
	a.Metadata["folder_id"] = m.FolderID
	a.Metadata["drive_link"] = m.DriveLink
	a.Metadata["drive_file_id"] = m.DriveFileID
	a.Metadata["download_link"] = m.DownloadLink
	a.Metadata["file_hash"] = m.FileHash
	a.Metadata["local_path"] = m.LocalPath
	a.Metadata["status"] = m.Status
	a.Metadata["search_text"] = m.SearchText
	if m.ThumbURL != "" {
		a.Metadata["thumb_url"] = m.ThumbURL
	}
	if m.Error != "" {
		a.Metadata["error"] = m.Error
	}
	if m.PHash != "" {
		a.Metadata["phash"] = m.PHash
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
