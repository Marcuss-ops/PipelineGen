package artifacts

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type ClipsRegistry struct {
	db *sql.DB
	// assets is retained for the post-dispatch SoftDelete path
	// (DeleteMedia). The pre-dispatcher media_assets UPSERT
	// (where `r.assets.Upsert(...)` was previously called) now routes
	// through `dispatcher.EnqueueAndIndex` (PR 7, June 2026,
	// codex/qdrant-app-writers-fail-closed).
	//
	// Pure-narrow write — the locations + processing writes below
	// stay on their respective narrow typed ports (asset_locations +
	// asset processing) and are NOT subject to the dispatcher SSOT.
	assets asset.Repository
	// committer is the canonical persistence.AssetCommitter SSOT
	// (QDRANT-002 PR7). Required for the media_assets UPSERT path so
	// the production write emits the matching outbox_events row in
	// the same tx (v1 conflation invariant).
	committer  persistence.AssetCommitter
	querySvc   *asset.Service
	locations  asset.LocationRepository
	processing asset.ProcessingRepository
}

// NewClipsRegistry is the canonical ctor. PR 7 (June 2026) added a 6th
// positional `committer` arg so the registry's UpsertMedia path
// enforces the canonical outbox+tx writer (QDRANT-002 atomicity
// invariant).
func NewClipsRegistry(
	db *sql.DB,
	assets asset.Repository,
	querySvc *asset.Service,
	locations asset.LocationRepository,
	processing asset.ProcessingRepository,
	committer persistence.AssetCommitter,
) *ClipsRegistry {
	return &ClipsRegistry{
		db:         db,
		assets:     assets,
		querySvc:   querySvc,
		locations:  locations,
		processing: processing,
		committer:  committer,
	}
}

func (r *ClipsRegistry) UpsertMedia(ctx context.Context, rec *MediaRecord) error {
	// PR 7 followups (June 2026, codex/qdrant-app-writers-fail-closed): nil
	// contract guards applied in the order (1) dispatcher fail-closed then
	// (2) nil-rec contract violation. Dispatcher-first matches the upstream
	// BulkUploadWorker + ReprocessUseCase convention so a config-broken
	// environment surfaces the actionable ErrDispatcherUnavailable signal
	// before the per-call contract violation. Both checks fire before any
	// asset-derivation so a nil rec cannot panic on rec.ID / rec.Source /
	// rec.LegacyFileMD5. Strict fail-closed at the lifecycle adapter's error
	// propagation boundary.
	//
	// Rec == nil returns a contract violation error at runtime.
	if r.committer == nil {
		return fmt.Errorf("clips registry committer not configured (QDRANT-asset-mutation isolation required): %w", mutations.ErrDispatcherUnavailable)
	}
	if rec == nil {
		return fmt.Errorf("UpsertMedia: MediaRecord is nil (contract violation)")
	}
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

	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): route the
	// media_assets UPSERT through the canonical persistence.AssetCommitter
	// so the QDRANT-002 atomicity invariant (media_assets UPSERT + outbox_events
	// INSERT in one tx) applies uniformly to artifacts-driven write paths.
	if _, err := r.committer.CommitAndIndex(ctx, persistence.CommitRequest{
		AssetID: m.ID, Source: string(m.Source), Name: m.Name, Filename: m.Filename,
		MediaType: string(m.MediaType), ContentHash: rec.LegacyFileMD5, LifecycleState: string(m.LifecycleState),
		IndexState: m.GetMetadataString("index_state"), EmitIndexEvent: true,
	}); err != nil {
		return fmt.Errorf("committer enqueue: %w", err)
	}

	// Write locations
	if rec.LocalPath != "" {
		loc := &asset.Location{
			AssetID:       rec.ID,
			LocationKind:  asset.LocationKindLocal,
			URI:           rec.LocalPath,
			LegacyFileMD5: rec.LegacyFileMD5,
			IsPrimary:     true,
		}
		if err := r.locations.Upsert(ctx, loc); err != nil {
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
		if err := r.locations.Upsert(ctx, loc); err != nil {
			return err
		}
	}

	// Write status/processing step if present. Processing state is part of
	// the lifecycle contract and must be durable before reporting success.
	if rec.Status != "" {
		step := string(asset.StageUpload)
		if rec.MediaType == "audio" {
			step = string(asset.StageDownload)
		}
		if err := persistMediaProcessingState(ctx, r.processing, rec, step); err != nil {
			return err
		}
	}

	return nil
}

func persistMediaProcessingState(ctx context.Context, processing asset.ProcessingRepository, rec *MediaRecord, step string) error {
	if processing == nil {
		return fmt.Errorf("clips registry: processing repository not configured")
	}
	if err := processing.Start(ctx, rec.ID, step); err != nil {
		return fmt.Errorf("clips registry: start processing %s/%s: %w", rec.ID, step, err)
	}
	switch rec.Status {
	case "failed":
		if err := processing.Fail(ctx, rec.ID, step, rec.Error); err != nil {
			return fmt.Errorf("clips registry: fail processing %s/%s: %w", rec.ID, step, err)
		}
	case "ACTIVE", "completed":
		if err := processing.Complete(ctx, rec.ID, step); err != nil {
			return fmt.Errorf("clips registry: complete processing %s/%s: %w", rec.ID, step, err)
		}
	}
	return nil
}

func (r *ClipsRegistry) GetMedia(ctx context.Context, id string) (*MediaRecord, error) {
	details, err := r.querySvc.Get(ctx, id)
	if err != nil {
		if err == asset.ErrNotFound {
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

func detailsToMediaRecord(details *asset.Details) *MediaRecord {
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
		if loc.LocationKind == asset.LocationKindLocal {
			rec.LocalPath = loc.URI
			rec.LegacyFileMD5 = loc.LegacyFileMD5
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
