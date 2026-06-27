package ingest

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

type clipStoreAdapter struct {
	db *sql.DB
	// repo was renamed from `assets` in Wave 12 follow-up
	// Phase 2 PR-3 — the original name collided with the
	// `internal/domain/asset` package alias after the sed
	// migration (sed's `\bassets\.` pattern matched the receiver
	// field `a.assets.Upsert`, producing broken `a.asset.Upsert`
	// references).
	repo       asset.Repository
	querySvc   *asset.Service
	locations  asset.LocationRepository
	processing asset.ProcessingRepository
}

func NewClipStoreAdapter(
	db *sql.DB,
	repo asset.Repository,
	querySvc *asset.Service,
	locations asset.LocationRepository,
	processing asset.ProcessingRepository,
) lifecycle.AssetRecordStore {
	return &clipStoreAdapter{
		db:         db,
		repo:       repo,
		querySvc:   querySvc,
		locations:  locations,
		processing: processing,
	}
}

func (a *clipStoreAdapter) Upsert(ctx context.Context, rec *artifacts.MediaRecord) error {
	m := &asset.Asset{
		ID:             rec.ID,
		Source:         asset.Source(rec.Source),
		Name:           rec.Name,
		Filename:       rec.Filename,
		MediaType:      asset.MediaType(rec.MediaType),
		Category:       rec.Category,
		Group:          rec.Group,
		SourceURL:      rec.ExternalURL,
		Duration:       time.Duration(rec.Duration) * time.Millisecond,
		Tags:           append([]string(nil), rec.Tags...),
		LifecycleState: asset.StateActive,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	m.SetExternalURL(rec.ExternalURL)
	m.SetFolderID(rec.FolderID)
	m.SetFolderPath(rec.FolderPath)
	m.SetPHash(rec.PHash)
	m.SetVisualEmbeddingJSON(rec.VisualEmbeddingJSON)
	m.SetMetadataJSON(rec.Metadata)

	if rec.Status == "DELETED" {
		m.LifecycleState = asset.StateDeleted
	}

	if err := a.repo.Upsert(ctx, m); err != nil {
		return err
	}

	// Write locations
	if rec.LocalPath != "" {
		loc := &asset.Location{
			AssetID:      rec.ID,
			LocationKind: asset.LocationKindLocal,
			URI:          rec.LocalPath,
			FileHash:     rec.FileHash,
			IsPrimary:    true,
		}
		if err := a.locations.Upsert(ctx, loc); err != nil {
			return err
		}
	}
	if rec.DriveLink != "" || rec.DriveFileID != "" {
		loc := &asset.Location{
			AssetID:      rec.ID,
			LocationKind: asset.LocationKindDrive,
			URI:          "drive://" + rec.DriveFileID,
			ExternalID:   rec.DriveFileID,
			AccessURL:    rec.DriveLink,
			DownloadURL:  rec.DownloadLink,
			IsPrimary:    rec.LocalPath == "",
		}
		if err := a.locations.Upsert(ctx, loc); err != nil {
			return err
		}
	}

	// Write status/processing step if present
	if rec.Status != "" {
		step := string(asset.StageUpload)
		if rec.MediaType == "audio" {
			step = string(asset.StageDownload)
		}
		if rec.Status == "failed" {
			_ = a.processing.Start(ctx, rec.ID, step)
			_ = a.processing.Fail(ctx, rec.ID, step, rec.Error)
	} else if rec.Status == "ACTIVE" || rec.Status == "completed" {
		_ = a.processing.Start(ctx, rec.ID, step)
		_ = a.processing.Complete(ctx, rec.ID, step)
	} else {
			_ = a.processing.Start(ctx, rec.ID, step)
		}
	}

	return nil
}

func (a *clipStoreAdapter) Get(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
	details, err := a.querySvc.Get(ctx, id)
	if err != nil {
		if err == asset.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return detailsToMediaRecord(details), nil
}

func (a *clipStoreAdapter) FindExisting(ctx context.Context, query assetop.ExistingAssetQuery) (*assetop.AssetRecord, error) {
	if query.ID != "" {
		rec, err := a.Get(ctx, query.ID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			return mediaRecordToAssetRecord(rec), nil
		}
	}

	if query.DriveFileID != "" {
		var assetID string
		err := a.db.QueryRowContext(ctx, `
			SELECT asset_id FROM asset_locations 
			WHERE external_id = ? AND location_kind = 'drive' 
			LIMIT 1
		`, query.DriveFileID).Scan(&assetID)
		if err == nil && assetID != "" {
			rec, err := a.Get(ctx, assetID)
			if err == nil && rec != nil {
				return mediaRecordToAssetRecord(rec), nil
			}
		}
	}

	if query.FileHash != "" {
		rows, err := a.db.QueryContext(ctx, `
			SELECT asset_id FROM asset_locations 
			WHERE file_hash = ? AND location_kind = 'local'
		`, query.FileHash)
		if err == nil {
			defer rows.Close()
			var assetID string
			if rows.Next() {
				if err := rows.Scan(&assetID); err == nil && assetID != "" {
					rec, err := a.Get(ctx, assetID)
					if err == nil && rec != nil {
						return mediaRecordToAssetRecord(rec), nil
					}
				}
			}
		}
	}

	return nil, nil
}

func (a *clipStoreAdapter) ListWithDriveFileID(ctx context.Context, source string) ([]*assetop.AssetRecord, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT id FROM media_assets 
		WHERE drive_file_id IS NOT NULL AND drive_file_id != '' 
		  AND lifecycle_state != 'DELETED'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	var out []*assetop.AssetRecord
	for _, id := range ids {
		rec, err := a.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			if source != "" && !strings.EqualFold(strings.TrimSpace(rec.Source), strings.TrimSpace(source)) {
				continue
			}
			out = append(out, mediaRecordToAssetRecord(rec))
		}
	}
	return out, nil
}

func (a *clipStoreAdapter) MarkDriveMissing(ctx context.Context, id string) error {
	rec, err := a.Get(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	rec.Status = "drive_missing"
	return a.Upsert(ctx, rec)
}

func (a *clipStoreAdapter) DeleteAssetRecord(ctx context.Context, id string) error {
	return a.repo.SoftDelete(ctx, id)
}

func detailsToMediaRecord(details *asset.Details) *artifacts.MediaRecord {
	if details == nil || details.Asset == nil {
		return nil
	}
	rec := &artifacts.MediaRecord{
		ID:                  details.Asset.ID,
		Name:                details.Asset.Name,
		Filename:            details.Asset.Filename,
		Source:              string(details.Asset.Source),
		Category:            details.Asset.Category,
		MediaType:           string(details.Asset.MediaType),
		ExternalURL:         details.Asset.ExternalURL(),
		FolderID:            details.Asset.FolderID(),
		FolderPath:          details.Asset.FolderPath(),
		Group:               details.Asset.Group,
		Tags:                append([]string(nil), details.Asset.Tags...),
		Duration:            int(details.Asset.Duration.Milliseconds()),
		VisualEmbeddingJSON: details.Asset.VisualEmbeddingJSON(),
		SourceID:            textutil.FirstNonEmpty(details.Asset.ExternalURL(), details.Asset.Filename, details.Asset.ID),
	}
	rec.Metadata = details.Asset.MetadataJSON()

	for _, loc := range details.Locations {
		if loc.LocationKind == asset.LocationKindLocal {
			rec.LocalPath = loc.URI
			rec.FileHash = loc.FileHash
		} else if loc.LocationKind == asset.LocationKindDrive {
			rec.DriveFileID = loc.ExternalID
			rec.DriveLink = loc.AccessURL
			rec.DownloadLink = loc.DownloadURL
		}
	}

	for _, proc := range details.Processing {
		if proc != nil {
			if proc.Status == asset.StatusFailed {
				rec.Status = "failed"
				rec.Error = proc.ErrorMessage
				break
			} else if proc.Status == asset.StatusRunning {
				rec.Status = "processing"
		} else if rec.Status == "" && proc.Status == asset.StatusCompleted {
			rec.Status = "ACTIVE"
		}
		}
	}
	if rec.Status == "" {
		rec.Status = "ACTIVE"
	}

	return rec
}
