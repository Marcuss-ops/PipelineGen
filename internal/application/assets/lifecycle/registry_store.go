package lifecycle

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assetop"
)

// RegistryStoreAdapter adapts a artifacts.Registry to the AssetRecordStore
// interface. Method signatures use assetop.X directly since W16-PR4 removed
// the re-export type aliases from this package (matches the pattern at
// internal/application/assets/ingest/{adapter_voiceover,adapter_clip,
// adapter_image}.go which were already on assetop.X before the PR).
type RegistryStoreAdapter struct {
	registry artifacts.Registry
}

// NewRegistryStoreAdapter creates a new RegistryStoreAdapter.
func NewRegistryStoreAdapter(registry artifacts.Registry) AssetRecordStore {
	return &RegistryStoreAdapter{registry: registry}
}

// FindExisting finds an existing asset record by query.
func (a *RegistryStoreAdapter) FindExisting(ctx context.Context, query assetop.ExistingAssetQuery) (*assetop.AssetRecord, error) {
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
func (a *RegistryStoreAdapter) ListWithDriveFileID(ctx context.Context, source string) ([]*assetop.AssetRecord, error) {
	records, err := a.registry.GetAllWithDriveFileID(ctx)
	if err != nil {
		return nil, err
	}

	var result []*assetop.AssetRecord
	for _, rec := range records {
		if source == "" || rec.Source == source {
			result = append(result, mediaRecordToAssetRecord(rec))
		}
	}
	return result, nil
}

// MarkDriveMissing marks a record as having a missing Drive file.
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

// Upsert implements AssetRecordStore.
func (a *RegistryStoreAdapter) Upsert(ctx context.Context, rec *artifacts.MediaRecord) error {
	return a.registry.UpsertMedia(ctx, rec)
}

// Get implements AssetRecordStore.
func (a *RegistryStoreAdapter) Get(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
	return a.registry.GetMedia(ctx, id)
}

// mediaRecordToAssetRecord converts a artifacts.MediaRecord to an AssetRecord.
func mediaRecordToAssetRecord(rec *artifacts.MediaRecord) *assetop.AssetRecord {
	if rec == nil {
		return nil
	}
	return &assetop.AssetRecord{
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
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}
