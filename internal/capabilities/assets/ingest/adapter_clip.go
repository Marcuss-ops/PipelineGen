package ingest

import (
	"context"
	"database/sql"
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

type clipStoreAdapter struct {
	db *sql.DB
	// retirer is the canonical media-retirement surface for
	// DeleteAssetRecord. It replaced the generic `repo detail.Repository`
	// field (Wave 12 follow-up Phase 2 PR-3 had renamed it from `assets`):
	// that seam carried no information about which database owned the write,
	// so in PostgreSQL mode the composition root could satisfy it with the
	// operational SQLite facade and silently retire the row on the wrong
	// engine. persistence.AssetSoftDeleter is the single owner of that port
	// (satisfied by PostgresMediaCommitter.SoftDeleteAsset — the documented
	// mirror of the SQLite ClipsRepository.SoftDelete) and
	// persistence.CanonicalAssetSoftDeleter is the single resolution rule, so
	// this capability names an engine for neither read nor write. A nil
	// implementation fails closed rather than writing an unnamed database.
	//
	// MEDIA-SSOT write-bridge (September 2026): the locations + processing
	// writes below no longer use the generic detail.LocationRepository /
	// detail.ProcessingRepository seam. That seam named no engine, so the
	// composition root satisfied it with the operational SQLite store while
	// PostgreSQL held media_assets: a media row committed to the SSOT then had
	// its location and its pipeline-step progress written to a second
	// database. Both surfaces are now media-authoritative and reached through
	// the narrow engine-named ports resolved from the canonical committer
	// (persistence.CanonicalAssetLocationWriter /
	// CanonicalAssetProcessingWriter), so this capability cannot name a
	// database of its own. A nil port is a media-plane-closed signal and the
	// write fails closed rather than landing on an unnamed engine.
	retirer    persistence.AssetSoftDeleter
	querySvc   AssetDetailsReader
	locations  persistence.AssetLocationWriter
	processing persistence.AssetProcessingWriter
	dispatcher mutations.AssetMutationDispatcher
	// driveFileIDs is the narrow media-SSOT read that ListWithDriveFileID
	// needs: the ids of assets still carrying a Drive file id. Before
	// MEDIA-SSOT P2-9 Phase 2 this listing ran the raw
	// `SELECT id FROM media_assets WHERE drive_file_id ...` statement against
	// the operational SQLite handle above, so an asset committed by the
	// canonical PostgreSQL committer was invisible to it and the Drive sweep
	// silently saw an empty catalog. The engine-named port replaces that
	// unnamed read; nil means the media plane is closed and the listing
	// fails closed instead of degrading onto a second engine.
	driveFileIDs MediaDriveFileIDLister
}

// MediaDriveFileIDLister is the narrow media-SSOT read that answers "which
// assets still have a Drive file id". Declaring it here — rather than reaching
// for a *sql.DB — is what keeps this capability from naming an engine: the
// composition root resolves it from the canonical media committer
// (mediaDriveFileListerFromCommitter) and PostgreSQL is the only
// implementation. A nil implementation is a media-plane-closed signal and
// ListWithDriveFileID fails closed.
type MediaDriveFileIDLister interface {
	ListAssetIDsWithDriveFileID(ctx context.Context) ([]string, error)
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
// AssetDetailsReader is the narrow media-details read the clip store hydrates a
// MediaRecord from (Get). Declaring it here — instead of requiring the concrete
// *detail.Service — is what lets the PostgreSQL media SSOT serve media
// hydration (MEDIA-SSOT P2-9 step 2).
//
// This adapter previously had NO PostgreSQL hydration path at all: it read the
// operational SQLite mirror unconditionally, so an asset committed by the
// canonical PostgreSQL committer looked absent to the ingest lifecycle and a
// stale pre-cutover row could be staged instead. *detail.Service (legacy
// SQLite) and *pgmedia.AssetDetailsReader both satisfy the interface, so the
// composition root picks the engine (derived from the canonical committer) and
// this capability stops naming one. A nil implementation is a
// media-plane-closed signal and Get fails closed.
type AssetDetailsReader interface {
	Get(ctx context.Context, id string) (*asset.Details, error)
}

func NewClipStoreAdapter(
	db *sql.DB,
	retirer persistence.AssetSoftDeleter,
	querySvc AssetDetailsReader,
	locations persistence.AssetLocationWriter,
	processing persistence.AssetProcessingWriter,
	dispatcher mutations.AssetMutationDispatcher,
	driveFileIDs MediaDriveFileIDLister,
) lifecycle.AssetRecordStore {
	return &clipStoreAdapter{
		db:           db,
		retirer:      retirer,
		querySvc:     querySvc,
		locations:    locations,
		processing:   processing,
		dispatcher:   dispatcher,
		driveFileIDs: driveFileIDs,
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

	// Write locations onto the media SSOT. The port is the narrow
	// engine-named asset_locations writer, so it cannot be satisfied by a
	// store that does not own the media database.
	if rec.LocalPath != "" {
		if err := a.upsertLocation(ctx, &asset.Location{
			AssetID:       rec.ID,
			LocationKind:  asset.LocationKindLocal,
			URI:           rec.LocalPath,
			LegacyFileMD5: rec.LegacyFileMD5,
			IsPrimary:     true,
		}); err != nil {
			return err
		}
	}
	if rec.DriveLink != "" || rec.DriveFileID != "" {
		if err := a.upsertLocation(ctx, &asset.Location{
			AssetID:      rec.ID,
			LocationKind: asset.LocationKindDrive,
			URI:          "drive://" + rec.DriveFileID,
			ExternalID:   rec.DriveFileID,
			AccessURL:    rec.DriveLink,
			DownloadURL:  rec.DownloadLink,
			IsPrimary:    rec.LocalPath == "",
		}); err != nil {
			return err
		}
	}

	// Write status/processing step if present. Processing state is part of
	// the lifecycle contract: an asset write must not report success when
	// its corresponding transition was not persisted on the media SSOT.
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

// upsertLocation attaches one storage location through the narrow
// engine-named asset_locations port. Nil means the media plane is closed, and
// a location write is not recoverable by picking a second database.
func (a *clipStoreAdapter) upsertLocation(ctx context.Context, loc *asset.Location) error {
	if a.locations == nil {
		return fmt.Errorf("clip store adapter: no asset_locations writer wired (media SSOT closed): %w", mutations.ErrDispatcherUnavailable)
	}
	if err := a.locations.UpsertAssetLocation(ctx, loc); err != nil {
		return fmt.Errorf("clip store adapter: upsert %s location %s: %w", loc.LocationKind, loc.AssetID, err)
	}
	return nil
}

func persistProcessingState(ctx context.Context, processing persistence.AssetProcessingWriter, rec *artifacts.MediaRecord, step string) error {
	if processing == nil {
		return fmt.Errorf("clip store adapter: no asset_processing writer wired (media SSOT closed): %w", mutations.ErrDispatcherUnavailable)
	}
	if err := processing.StartAssetProcessing(ctx, rec.ID, step); err != nil {
		return fmt.Errorf("clip store adapter: start processing %s/%s: %w", rec.ID, step, err)
	}
	switch rec.Status {
	case "failed":
		if err := processing.FailAssetProcessing(ctx, rec.ID, step, rec.Error); err != nil {
			return fmt.Errorf("clip store adapter: fail processing %s/%s: %w", rec.ID, step, err)
		}
	case "ACTIVE", "completed":
		if err := processing.CompleteAssetProcessing(ctx, rec.ID, step); err != nil {
			return fmt.Errorf("clip store adapter: complete processing %s/%s: %w", rec.ID, step, err)
		}
	}
	return nil
}

func (a *clipStoreAdapter) Get(ctx context.Context, id string) (*artifacts.MediaRecord, error) {
	if a.querySvc == nil {
		// Fail closed: reporting "not found" for an unreadable catalog would be
		// a successful no-op, and the caller would then stage/publish a clip it
		// never verified.
		return nil, fmt.Errorf("clip store adapter: no media details reader wired (media SSOT closed)")
	}
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

// ListWithDriveFileID lists the media assets that have a Drive file id, hydrating
// each one through Get (so the read model stays single-sourced) and optionally
// filtering by source.
//
// MEDIA-SSOT P2-9 Phase 2: the id listing comes from the media-SSOT port. It no
// longer issues raw SQL against the operational SQLite handle, because that
// handle and the PostgreSQL media SSOT are different databases — the SQLite
// mirror is empty in production while PostgreSQL holds the rows, so the legacy
// statement could only ever return nothing. A nil port is a media-plane-closed
// signal and this method fails closed rather than reading a second engine.
func (a *clipStoreAdapter) ListWithDriveFileID(ctx context.Context, source string) ([]*assetop.AssetRecord, error) {
	if a == nil || a.driveFileIDs == nil {
		return nil, fmt.Errorf("clip store adapter: no media drive-file-id lister wired (media SSOT closed)")
	}
	ids, err := a.driveFileIDs.ListAssetIDsWithDriveFileID(ctx)
	if err != nil {
		return nil, fmt.Errorf("clip store adapter: list drive file ids: %w", err)
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

// DeleteAssetRecord retires the media asset through the canonical retirement
// port. See persistence.AssetSoftDeleter for why the generic detail.Repository
// seam was removed here.
func (a *clipStoreAdapter) DeleteAssetRecord(ctx context.Context, id string) error {
	if a.retirer == nil {
		// Fail closed: an unwired retirement port must not be reported as a
		// successful delete, and must never fall back to a database nobody
		// named.
		return fmt.Errorf("clip store adapter: no media retirement port wired (media SSOT closed): %w", mutations.ErrDispatcherUnavailable)
	}
	return a.retirer.SoftDeleteAsset(ctx, id)
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
