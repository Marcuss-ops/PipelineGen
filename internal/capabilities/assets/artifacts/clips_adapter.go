package artifacts

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type ClipsRegistry struct {
	db  *sql.DB // operational-only: legacy reads scheduled for demolition (see MEDIA-SSOT items 6/7); never used for media writes
	log *zap.Logger
	// assets is retained for the post-dispatch SoftDelete path
	// (DeleteMedia). The pre-dispatcher media_assets UPSERT
	// (where `r.assets.Upsert(...)` was previously called) now routes
	// through `dispatcher.EnqueueAndIndex` (PR 7, June 2026,
	// codex/qdrant-app-writers-fail-closed).
	//
	// Pure-narrow write — the locations + processing writes below
	// stay on their respective narrow typed ports (asset_locations +
	// asset processing) and are NOT subject to the dispatcher SSOT.
	assets detail.Repository
	// committer is the canonical persistence.AssetCommitter SSOT
	// (QDRANT-002 PR7). Required for the media_assets UPSERT path so
	// the production write emits the matching outbox_events row in
	// the same tx (v1 conflation invariant).
	committer  persistence.AssetCommitter
	querySvc   *detail.Service
	processing detail.ProcessingRepository
}

// NewClipsRegistry is the canonical ctor. PR 7 (June 2026) added a 6th
// positional `committer` arg so the registry's UpsertMedia path
// enforces the canonical outbox+tx writer (QDRANT-002 atomicity
// invariant).
func NewClipsRegistry(
	db *sql.DB,
	assets detail.Repository,
	querySvc *detail.Service,
	processing detail.ProcessingRepository,
	committer persistence.AssetCommitter,
) *ClipsRegistry {
	return &ClipsRegistry{
		db:         db,
		assets:     assets,
		querySvc:   querySvc,
		processing: processing,
		committer:  committer,
		log:        zap.NewNop(),
	}
}

// NewClipsRegistryWithLogger is the wiring variant that threads the
// composition logger so operational best-effort warnings (P1-5) are
// observable.
func NewClipsRegistryWithLogger(
	db *sql.DB,
	assets detail.Repository,
	querySvc *detail.Service,
	processing detail.ProcessingRepository,
	committer persistence.AssetCommitter,
	log *zap.Logger,
) *ClipsRegistry {
	r := NewClipsRegistry(db, assets, querySvc, processing, committer)
	if log != nil {
		r.log = log
	}
	return r
}

// logger returns the wired logger, falling back to a no-op for a zero-value
// ClipsRegistry. Both constructors guarantee a non-nil logger, so this is a
// guard for direct struct literals only — never a silent-logging decision.
func (r *ClipsRegistry) logger() *zap.Logger {
	if r == nil || r.log == nil {
		return zap.NewNop()
	}
	return r.log
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
	lifecycleState := asset.StateActive
	if rec.Status == "DELETED" {
		lifecycleState = asset.StateDeleted
	}

	// MEDIA-SSOT P0-1 (Sept 2026): asset_locations is part of the media aggregate.
	// The entire media row + locations + outbox MUST commit in ONE PG transaction.
	// The previous PG→SQLite two-phase (CommitAndIndex then LocationRepository.Upsert)
	// produced a partial-commit divergence: PG could succeed while SQLite failed and
	// the caller would retry an already-durable asset. The locations ride inside the
	// CommitRequest so the canonical PG committer upserts media_assets +
	// asset_locations + outbox atomically. The SQLite LocationRepository is
	// intentionally NOT called from this path (legacy backfill only).
	var locations []persistence.LocationCommit
	if rec.LocalPath != "" {
		locations = append(locations, persistence.LocationCommit{
			Kind: string(asset.LocationKindLocal), URI: rec.LocalPath,
			LegacyFileMD5: rec.LegacyFileMD5, IsPrimary: true,
		})
	}
	if rec.DriveLink != "" || rec.DriveFileID != "" {
		locations = append(locations, persistence.LocationCommit{
			Kind: string(asset.LocationKindDrive), URI: "drive://" + rec.DriveFileID,
			ExternalID: rec.DriveFileID, WebViewLink: rec.DriveLink, DownloadURL: rec.DownloadLink,
			IsPrimary: rec.LocalPath == "", LegacyFileMD5: rec.LegacyFileMD5,
		})
	}
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): route the
	// media_assets UPSERT through the canonical persistence.AssetCommitter
	// so the QDRANT-002 atomicity invariant (media_assets UPSERT + outbox_events
	// INSERT in one tx) applies uniformly to artifacts-driven write paths.
	if _, err := r.committer.CommitAndIndex(ctx, persistence.CommitRequest{
		AssetID: rec.ID, Source: rec.Source, Name: rec.Name, Filename: rec.Filename,
		MediaType: rec.MediaType, GroupName: rec.Group,
		ContentHash: rec.LegacyFileMD5, LifecycleState: string(lifecycleState),
		IndexState: mediaIndexState(rec.Metadata), Locations: locations, EmitIndexEvent: true,
	}); err != nil {
		return fmt.Errorf("committer enqueue: %w", err)
	}

	// P1-5 (Sept 2026): asset_processing is operational / observability
	// (SQLite) and MUST NOT make a durable PG media commit fail closed.
	// The media row + locations + outbox are already durably committed in
	// PG above; a SQLite processing write failure is best-effort and is
	// logged but does not return an error (the caller already has a
	// durable asset). This eliminates the PG COMMIT -> SQLite write ->
	// return error partial-commit boundary.
	if rec.Status != "" && r.processing != nil {
		step := string(asset.StageUpload)
		if rec.MediaType == "audio" {
			step = string(asset.StageDownload)
		}
		if err := persistMediaProcessingState(ctx, r.processing, rec, step); err != nil {
			r.logger().Warn("clips registry: processing state best-effort write failed (media commit already durable)",
				zap.String("asset_id", rec.ID), zap.String("step", step), zap.Error(err))
		}
	}

	return nil
}

// mediaIndexState extracts the media_assets.index_state mirror from a
// metadata_json string. Returns empty on parse failure (same tolerance as the
// metadata port).
//
// It decodes into a typed envelope rather than a map[string]any: this runs on
// EVERY UpsertMedia (the ingest hot path), and the map form allocated a map,
// string keys and interface-boxed values just to read one field.
func mediaIndexState(metadataJSON string) string {
	if metadataJSON == "" {
		return ""
	}
	var probe struct {
		IndexState string `json:"index_state"`
	}
	if json.Unmarshal([]byte(metadataJSON), &probe) != nil {
		return ""
	}
	return probe.IndexState
}

func persistMediaProcessingState(ctx context.Context, processing detail.ProcessingRepository, rec *MediaRecord, step string) error {
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
	if pgDB := r.pgDB(); pgDB != nil {
		return r.getMediaPG(ctx, pgDB, id)
	}
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
	if mut := r.pgMutator(); mut != nil {
		// MEDIA-SSOT P0-2/P1-6: lifecycle mutation via PG media SSOT.
		if err := mut.UpdateLifecycle(ctx, id, string(asset.StateDeleted), "", ""); err != nil {
			return fmt.Errorf("clips registry: pg delete %s: %w", id, err)
		}
		return nil
	}
	return r.assets.SoftDelete(ctx, id)
}

func (r *ClipsRegistry) GetAllWithDriveFileID(ctx context.Context) ([]*MediaRecord, error) {
	if pgDB := r.pgDB(); pgDB != nil {
		return r.getAllWithDriveFileIDPG(ctx, pgDB)
	}
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
	if pgDB := r.pgDB(); pgDB != nil {
		var id string
		err := pgDB.QueryRowContext(ctx, `SELECT id FROM media_assets WHERE phash = $1 AND lifecycle_state != 'DELETED' LIMIT 1`, phash).Scan(&id)
		if err == sql.ErrNoRows {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return id, nil
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

// pgDB returns the PG media DB when the committer is the PG media
// committer; nil otherwise (SQLite-only / degraded mode).
func (r *ClipsRegistry) pgDB() *sql.DB {
	if r == nil || r.committer == nil {
		return nil
	}
	type pgDBGetter interface{ DB() *sql.DB }
	if g, ok := r.committer.(pgDBGetter); ok && g != nil {
		return g.DB()
	}
	return nil
}

func (r *ClipsRegistry) pgMutator() persistence.AssetMutationCommitter {
	if r == nil || r.committer == nil {
		return nil
	}
	if m, ok := r.committer.(persistence.AssetMutationCommitter); ok {
		return m
	}
	return nil
}

// mediaRecordPGSelect is the SINGLE canonical MediaRecord projection for the
// PG media path. Both the single-row read and the list read use it so the
// column list and the scan order cannot drift apart.
const mediaRecordPGSelect = `
		SELECT id, COALESCE(source,''), COALESCE(name,''), COALESCE(filename,''),
		       COALESCE(media_type,''), COALESCE(category,''), COALESCE(group_name,''),
		       COALESCE(lifecycle_state,''), COALESCE(index_state,''),
		       COALESCE(metadata_json,'{}'), COALESCE(search_text,''),
		       COALESCE(drive_file_id,''), COALESCE(drive_link,''),
		       COALESCE(download_link,''), COALESCE(local_path,''),
		       COALESCE(legacy_file_md5,''), COALESCE(phash,'')
		FROM media_assets`

// mediaRecordScanner is the common row surface of *sql.Row and *sql.Rows.
type mediaRecordScanner interface {
	Scan(dest ...any) error
}

// scanMediaRecordPG decodes one row projected by mediaRecordPGSelect.
func scanMediaRecordPG(scanner mediaRecordScanner) (*MediaRecord, error) {
	var (
		aID, source, name, filename, mediaType, category, groupName string
		lifecycleState, indexState, metadataJSON                    string
		searchText, driveFileID, driveLink, downloadLink, localPath string
		legacyMD5, phash                                            string
	)
	if err := scanner.Scan(
		&aID, &source, &name, &filename, &mediaType, &category, &groupName,
		&lifecycleState, &indexState, &metadataJSON, &searchText,
		&driveFileID, &driveLink, &downloadLink, &localPath, &legacyMD5, &phash); err != nil {
		return nil, err
	}
	rec := &MediaRecord{
		ID: aID, Source: source, Name: name, Filename: filename,
		MediaType: mediaType, Category: category, Group: groupName,
		Metadata: metadataJSON, LegacyFileMD5: legacyMD5, PHash: phash,
		DriveFileID: driveFileID, DriveLink: driveLink, DownloadLink: downloadLink,
		LocalPath: localPath, Status: "ACTIVE",
	}
	if lifecycleState == string(asset.StateDeleted) {
		rec.Status = "DELETED"
	}
	return rec, nil
}

func (r *ClipsRegistry) getMediaPG(ctx context.Context, pgDB *sql.DB, id string) (*MediaRecord, error) {
	rec, err := scanMediaRecordPG(pgDB.QueryRowContext(ctx, mediaRecordPGSelect+` WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// getAllWithDriveFileIDPG returns every live asset that carries a Drive file
// id in ONE query. The previous implementation selected the ids and then
// re-fetched each row by id (a classic N+1: N+1 round-trips and N record
// decodes for a single sweep).
func (r *ClipsRegistry) getAllWithDriveFileIDPG(ctx context.Context, pgDB *sql.DB) ([]*MediaRecord, error) {
	rows, err := pgDB.QueryContext(ctx, mediaRecordPGSelect+
		` WHERE drive_file_id IS NOT NULL AND drive_file_id != '' AND lifecycle_state != 'DELETED'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*MediaRecord
	for rows.Next() {
		rec, scanErr := scanMediaRecordPG(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
