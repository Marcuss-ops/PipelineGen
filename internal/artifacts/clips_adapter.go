package artifacts

import (
	"context"
	"database/sql"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/assets"
)

type ClipsRegistry struct {
	db         *sql.DB
	assets     assets.Repository
	querySvc   *assets.Service
	locations  assets.LocationRepository
	processing assets.ProcessingRepository
}

func NewClipsRegistry(
	db *sql.DB,
	assets assets.Repository,
	querySvc *assets.Service,
	locations assets.LocationRepository,
	processing assets.ProcessingRepository,
) *ClipsRegistry {
	return &ClipsRegistry{
		db:         db,
		assets:     assets,
		querySvc:   querySvc,
		locations:  locations,
		processing: processing,
	}
}

func (r *ClipsRegistry) UpsertMedia(ctx context.Context, rec *MediaRecord) error {
	m := &assets.Asset{
		ID:             rec.ID,
		Source:         assets.Source(rec.Source),
		Name:           rec.Name,
		Filename:       rec.Filename,
		MediaType:      assets.MediaType(rec.MediaType),
		Category:       rec.Category,
		Group:          rec.Group,
		SourceURL:      rec.ExternalURL,
		Duration:       time.Duration(rec.Duration) * time.Millisecond,
		Tags:           append([]string(nil), rec.Tags...),
		LifecycleState: assets.StateReady,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	m.SetExternalURL(rec.ExternalURL)
	m.SetFolderID(rec.FolderID)
	m.SetFolderPath(rec.FolderPath)
	m.SetPHash(rec.PHash)
	m.SetVisualEmbeddingJSON(rec.VisualEmbeddingJSON)
	m.SetMetadataJSON(rec.Metadata)

	if rec.Status == "deleted" {
		m.LifecycleState = assets.StateDeleted
	}

	if err := r.assets.Upsert(ctx, m); err != nil {
		return err
	}

	// Write locations
	if rec.LocalPath != "" {
		loc := &assets.Location{
			AssetID:      rec.ID,
			LocationKind: assets.LocationKindLocal,
			URI:          rec.LocalPath,
			FileHash:     rec.FileHash,
			IsPrimary:    true,
		}
		if err := r.locations.Upsert(ctx, loc); err != nil {
			return err
		}
	}
	if rec.DriveLink != "" || rec.DriveFileID != "" {
		loc := &assets.Location{
			AssetID:      rec.ID,
			LocationKind: assets.LocationKindDrive,
			URI:          "drive://" + rec.DriveFileID,
			ExternalID:   rec.DriveFileID,
			AccessURL:    rec.DriveLink,
			DownloadURL:  rec.DownloadLink,
			IsPrimary:    rec.LocalPath == "",
		}
		if err := r.locations.Upsert(ctx, loc); err != nil {
			return err
		}
	}

	// Write status/processing step if present
	if rec.Status != "" {
		step := string(assets.StageUpload)
		if rec.MediaType == "audio" {
			step = string(assets.StageDownload)
		}
		if rec.Status == "failed" {
			_ = r.processing.Start(ctx, rec.ID, step)
			_ = r.processing.Fail(ctx, rec.ID, step, rec.Error)
		} else if rec.Status == "ready" || rec.Status == "completed" {
			_ = r.processing.Start(ctx, rec.ID, step)
			_ = r.processing.Complete(ctx, rec.ID, step)
		} else {
			_ = r.processing.Start(ctx, rec.ID, step)
		}
	}

	return nil
}

func (r *ClipsRegistry) GetMedia(ctx context.Context, id string) (*MediaRecord, error) {
	details, err := r.querySvc.Get(ctx, id)
	if err != nil {
		if err == assets.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return detailsToMediaRecord(details), nil
}

func (r *ClipsRegistry) DeleteMedia(ctx context.Context, id string) error {
	return r.assets.SoftDelete(ctx, id)
}

func (r *ClipsRegistry) GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM media_assets 
		WHERE drive_file_id IS NOT NULL AND drive_file_id != '' 
		  AND lifecycle_state != 'deleted'
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

	var records []*MediaRecord
	for _, id := range ids {
		rec, err := r.GetMedia(ctx, id)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			records = append(records, rec)
		}
	}
	return records, nil
}

func (r *ClipsRegistry) FindByPHash(ctx context.Context, phash string) (string, error) {
	if phash == "" {
		return "", nil
	}
	var id string
	err := r.db.QueryRowContext(ctx, `
		SELECT id FROM media_assets 
		WHERE phash = ? AND lifecycle_state != 'deleted' 
		LIMIT 1
	`, phash).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

func detailsToMediaRecord(details *assets.Details) *MediaRecord {
	if details == nil || details.Asset == nil {
		return nil
	}
	rec := &MediaRecord{
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
	}
	rec.Metadata = details.Asset.MetadataJSON()

	for _, loc := range details.Locations {
		if loc.LocationKind == assets.LocationKindLocal {
			rec.LocalPath = loc.URI
			rec.FileHash = loc.FileHash
		} else if loc.LocationKind == assets.LocationKindDrive {
			rec.DriveFileID = loc.ExternalID
			rec.DriveLink = loc.AccessURL
			rec.DownloadLink = loc.DownloadURL
		}
	}

	for _, proc := range details.Processing {
		if proc != nil {
			if proc.Status == assets.StatusFailed {
				rec.Status = "failed"
				rec.Error = proc.ErrorMessage
				break
			} else if proc.Status == assets.StatusRunning {
				rec.Status = "processing"
			} else if rec.Status == "" && proc.Status == assets.StatusCompleted {
				rec.Status = "ready"
			}
		}
	}
	if rec.Status == "" {
		rec.Status = "ready"
	}

	return rec
}
