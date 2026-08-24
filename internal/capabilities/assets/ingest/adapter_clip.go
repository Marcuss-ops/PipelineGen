package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

type clipStoreAdapter struct {
	db *sql.DB
	// repo was renamed from `assets` in Wave 12 follow-up
	// Phase 2 PR-3 — the original name collided with the
	// `internal/domain/asset` package alias after the sed
	// migration (sed's `\bassets\.` pattern matched the receiver
	// field `a.ports.Upsert`, producing broken `a.asset.Upsert`
	// references).
	//
	// repo is retained for the post-dispatch SoftDelete path
	// (DeleteAssetRecord). The pre-dispatcher media_assets UPSERT
	// (where `a.repo.Upsert(...)` was previously called) now routes
	// through `dispatcher.EnqueueAndIndex` (PR 7, June 2026,
	// codex/qdrant-app-writers-fail-closed).
	//
	// Pure-narrow write — the locations + processing writes below
	// stay on their respective narrow typed ports (asset_locations +
	// asset processing) and are NOT subject to the dispatcher SSOT.
	repo       asset.Repository
	querySvc   *asset.Service
	locations  asset.LocationRepository
	processing asset.ProcessingRepository
	dispatcher mutations.AssetMutationDispatcher
}

// NewClipStoreAdapter is the canonical AssetRecordStore ctor. PR 7
// (June 2026) added a 6th positional `dispatcher` arg so the
// Upsert path enforces the canonical outbox+tx writer (QDRANT-002
// atomicity invariant). Composition-root pre-rejection lives in
// the wiring site (internal/app/module_media.go::WireMediaIngest
// + internal/app/build_bundles_domain.go::buildIngestService) which
// surfaces a configure-time error if the dispatcher is nil. Rec == nil
// returns a contract violation error at runtime (see Upsert method
// godoc for the runtime contract).
func NewClipStoreAdapter(
	db *sql.DB,
	repo asset.Repository,
	querySvc *asset.Service,
	locations asset.LocationRepository,
	processing asset.ProcessingRepository,
	dispatcher mutations.AssetMutationDispatcher,
) lifecycle.AssetRecordStore {
	return &clipStoreAdapter{
		db:         db,
		repo:       repo,
		querySvc:   querySvc,
		locations:  locations,
		processing: processing,
		dispatcher: dispatcher,
	}
}

func (a *clipStoreAdapter) Upsert(ctx context.Context, rec *artifacts.MediaRecord) error {
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
	if a.dispatcher == nil {
		return fmt.Errorf("clip store adapter dispatcher not configured (QDRANT-asset-mutation isolation required): %w", mutations.ErrDispatcherUnavailable)
	}
	if rec == nil {
		return fmt.Errorf("Upsert: MediaRecord is nil (contract violation)")
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
	// media_assets UPSERT through the canonical mutations.AssetMutationDispatcher
	// so the QDRANT-002 atomicity invariant (media_assets UPSERT + outbox_events
	// INSERT in one tx) applies uniformly to ingest-driven write paths. The
	// strict fail-closed nil dispatcher check fires at the top of this function
	// (before asset-derivation) so the dispatcher surface is reached first.
	if err := a.dispatcher.EnqueueAndIndex(ctx, m, rec.LegacyFileMD5); err != nil {
		return fmt.Errorf("dispatcher enqueue: %w", err)
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

	// Write status/processing step if present. Processing state is part of
	// the lifecycle contract: an asset write must not report success when
	// its corresponding transition was not persisted.
	if rec.Status != "" {
		step := string(asset.StageUpload)
		if rec.MediaType == "audio" {
			step = string(asset.StageDownload)
		}
		if err := persistProcessingState(ctx, a.processing, rec, step); err != nil {
			return err
		}
	}

	return nil
}

func persistProcessingState(ctx context.Context, processing asset.ProcessingRepository, rec *artifacts.MediaRecord, step string) error {
	if processing == nil {
		return fmt.Errorf("clip store adapter: processing repository not configured")
	}
	if err := processing.Start(ctx, rec.ID, step); err != nil {
		return fmt.Errorf("clip store adapter: start processing %s/%s: %w", rec.ID, step, err)
	}
	switch rec.Status {
	case "failed":
		if err := processing.Fail(ctx, rec.ID, step, rec.Error); err != nil {
			return fmt.Errorf("clip store adapter: fail processing %s/%s: %w", rec.ID, step, err)
		}
	case "ACTIVE", "completed":
		if err := processing.Complete(ctx, rec.ID, step); err != nil {
			return fmt.Errorf("clip store adapter: complete processing %s/%s: %w", rec.ID, step, err)
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

	if query.LegacyFileMD5 != "" {
		rows, err := a.db.QueryContext(ctx, `
			SELECT asset_id FROM asset_locations 
			WHERE legacy_file_md5 = ? AND location_kind = 'local'
		`, query.LegacyFileMD5)
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
