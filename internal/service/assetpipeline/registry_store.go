package assetpipeline

import (
	"context"
	"time"

	"velox/go-master/internal/service/mediaregistry"
)

// RegistryStoreAdapter adapts a mediaregistry.Registry to the AssetRecordStore interface.
type RegistryStoreAdapter struct {
	registry mediaregistry.Registry
}

// NewRegistryStoreAdapter creates a new RegistryStoreAdapter.
func NewRegistryStoreAdapter(registry mediaregistry.Registry) AssetRecordStore {
	return &RegistryStoreAdapter{registry: registry}
}

// FindExisting finds an existing asset record by query.
// Implements a simple search based on the query fields.
func (a *RegistryStoreAdapter) FindExisting(ctx context.Context, query ExistingAssetQuery) (*AssetRecord, error) {
	if query.ID != "" {
		rec, err := a.registry.GetMedia(ctx, query.ID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			return mediaRecordToAssetRecord(rec), nil
		}
	}

	if query.DriveFileID != "" {
		records, err := a.registry.GetAllWithDriveFileID(ctx)
		if err != nil {
			return nil, err
		}
		for _, rec := range records {
			if rec.DriveFileID == query.DriveFileID {
				return mediaRecordToAssetRecord(rec), nil
			}
		}
	}

	return nil, nil
}

// ListWithDriveFileID lists all records that have a non-empty Drive file ID.
// If source is non-empty, filters by source.
func (a *RegistryStoreAdapter) ListWithDriveFileID(ctx context.Context, source string) ([]*AssetRecord, error) {
	records, err := a.registry.GetAllWithDriveFileID(ctx)
	if err != nil {
		return nil, err
	}

	var result []*AssetRecord
	for _, rec := range records {
		if source == "" || rec.Source == source {
			result = append(result, mediaRecordToAssetRecord(rec))
		}
	}
	return result, nil
}

// MarkDriveMissing marks a record as having a missing Drive file.
// Updates the Status field to "drive_missing".
func (a *RegistryStoreAdapter) MarkDriveMissing(ctx context.Context, id string) error {
	rec, err := a.registry.GetMedia(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}

	rec.Status = "drive_missing"
	return a.registry.UpsertMedia(ctx, rec)
}

// DeleteAssetRecord deletes an asset record by ID.
func (a *RegistryStoreAdapter) DeleteAssetRecord(ctx context.Context, id string) error {
	return a.registry.DeleteMedia(ctx, id)
}

// mediaRecordToAssetRecord converts a mediaregistry.MediaRecord to an AssetRecord.
func mediaRecordToAssetRecord(rec *mediaregistry.MediaRecord) *AssetRecord {
	if rec == nil {
		return nil
	}
	return &AssetRecord{
		ID:           rec.ID,
		Name:         rec.Name,
		Filename:     rec.Filename,
		Source:       rec.Source,
		MediaType:    rec.MediaType,
		DriveFileID:  rec.DriveFileID,
		DriveLink:    rec.DriveLink,
		DownloadLink: rec.DownloadLink,
		FileHash:     rec.FileHash,
		LocalPath:    rec.LocalPath,
		Status:       rec.Status,
		Error:        rec.Error,
		Metadata:     rec.Metadata,
		CreatedAt:    time.Now(), // MediaRecord doesn't have CreatedAt
		UpdatedAt:    time.Now(), // MediaRecord doesn't have UpdatedAt
	}
}
