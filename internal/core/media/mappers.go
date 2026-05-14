package media

import (
	"velox/go-master/pkg/models"
)

// ClipToMediaAsset converts a models.MediaAsset to a core MediaAsset (legacy name).
// workspaceID and projectID must be provided as they are not part of MediaAsset.
func ClipToMediaAsset(c models.MediaAsset, workspaceID, projectID string) MediaAsset {
	asset := MediaAsset{
		ID:           c.ID,
		WorkspaceID:  workspaceID,
		ProjectID:    projectID,
		Title:        c.Name,
		Category:     c.Category,
		Tags:         c.Tags,
		ExternalURL:  c.ExternalURL,
		DurationSecs: c.Duration,
		MetadataJSON: c.MetadataJSON(),
		CreatedAt:    c.CreatedAt,
		UpdatedAt:    c.UpdatedAt,
	}

	if c.DriveLink != "" || c.DownloadLink != "" || c.LocalPath != "" || c.FileHash != "" {
		asset.PrimaryFile = &MediaFile{
			URI:          c.LocalPath,
			LocalPath:    c.LocalPath,
			DriveLink:    c.DriveLink,
			DownloadLink: c.DownloadLink,
			FileHash:     c.FileHash,
		}
	}

	return asset
}

// MediaAssetToClip converts a MediaAsset to models.MediaAsset.
func MediaAssetToClip(a MediaAsset) models.MediaAsset {
	clip := models.MediaAsset{
		ID:          a.ID,
		Name:        a.Title,
		Category:    a.Category,
		Tags:        a.Tags,
		ExternalURL: a.ExternalURL,
		Duration:    a.DurationSecs,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
	clip.SetMetadataJSON(a.MetadataJSON)

	if a.PrimaryFile != nil {
		clip.LocalPath = a.PrimaryFile.LocalPath
		clip.DriveLink = a.PrimaryFile.DriveLink
		clip.DownloadLink = a.PrimaryFile.DownloadLink
		clip.FileHash = a.PrimaryFile.FileHash
	}

	return clip
}

// ItemToMediaAsset converts an Item to a MediaAsset.
func ItemToMediaAsset(item Item, primaryFile *MediaFile, files []MediaFile) MediaAsset {
	asset := MediaAsset{
		ID:           item.ID,
		WorkspaceID:  item.WorkspaceID,
		ProjectID:    item.ProjectID,
		SourceID:     item.SourceID,
		SourceKind:   item.SourceKind,
		MediaType:    item.MediaType,
		Status:       item.Status,
		Title:        item.Title,
		Description:  item.Description,
		ExternalID:   item.ExternalID,
		ExternalURL:  item.ExternalURL,
		DurationSecs: item.DurationSecs,
		MetadataJSON: item.MetadataJSON,
		CreatedAt:    item.CreatedAt,
		UpdatedAt:    item.UpdatedAt,
	}

	if primaryFile != nil {
		asset.PrimaryFile = primaryFile
	} else if item.FileHash != "" {
		asset.PrimaryFile = &MediaFile{
			FileHash: item.FileHash,
		}
	}
	asset.Files = files

	return asset
}

// MediaAssetToItem converts a MediaAsset to an Item.
// It returns the Item and a slice of Files.
func MediaAssetToItem(a MediaAsset) (Item, []File) {
	item := Item{
		ID:           a.ID,
		WorkspaceID:  a.WorkspaceID,
		ProjectID:    a.ProjectID,
		SourceID:     a.SourceID,
		SourceKind:   a.SourceKind,
		MediaType:    a.MediaType,
		Status:       a.Status,
		Title:        a.Title,
		Description:  a.Description,
		ExternalID:   a.ExternalID,
		ExternalURL:  a.ExternalURL,
		DurationSecs: a.DurationSecs,
		MetadataJSON: a.MetadataJSON,
		CreatedAt:    a.CreatedAt,
		UpdatedAt:    a.UpdatedAt,
	}

	// Extract FileHash from PrimaryFile if available
	if a.PrimaryFile != nil {
		item.FileHash = a.PrimaryFile.FileHash
	}

	files := make([]File, 0, len(a.Files)+1)
	if a.PrimaryFile != nil {
		files = append(files, MediaFileToItemFile(*a.PrimaryFile, a.ID))
	}
	for _, f := range a.Files {
		files = append(files, MediaFileToItemFile(f, a.ID))
	}

	return item, files
}

// MediaFileToItemFile converts a MediaFile to a File (from models.go).
func MediaFileToItemFile(mf MediaFile, mediaAssetID string) File {
	return File{
		ID:            mf.ID,
		MediaItemID:   mediaAssetID,
		LocationKind:  mf.LocationKind,
		URI:           mf.URI,
		MimeType:      mf.MimeType,
		Width:         mf.Width,
		Height:        mf.Height,
		DurationSecs:  mf.DurationSecs,
		FileSizeBytes: mf.FileSizeBytes,
		FileHash:      mf.FileHash,
		Status:        mf.Status,
		CreatedAt:     mf.CreatedAt,
		UpdatedAt:     mf.UpdatedAt,
	}
}

// ItemFileToMediaFile converts a File (from models.go) to a MediaFile.
func ItemFileToMediaFile(f File) MediaFile {
	return MediaFile{
		ID:            f.ID,
		MediaAssetID:  f.MediaItemID,
		LocationKind:  f.LocationKind,
		URI:           f.URI,
		MimeType:      f.MimeType,
		Width:         f.Width,
		Height:        f.Height,
		DurationSecs:  f.DurationSecs,
		FileSizeBytes: f.FileSizeBytes,
		FileHash:      f.FileHash,
		Status:        f.Status,
		CreatedAt:     f.CreatedAt,
		UpdatedAt:     f.UpdatedAt,
	}
}
