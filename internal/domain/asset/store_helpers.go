package asset

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// Repository is the canonical CRUD contract for asset persistence.
// Consumer-owned: each caller declares what it needs.
type Repository interface {
	Upsert(ctx context.Context, asset *Asset) error
	Get(ctx context.Context, id string) (*Asset, error)
	List(ctx context.Context, filter Filter) ([]*Asset, error)
	Count(ctx context.Context, filter Filter) (int64, error)
	SoftDelete(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	HardDelete(ctx context.Context, id string) error
	FindByExternalRef(ctx context.Context, provider, externalID string) (*Asset, error)
}

// LocationRepository is the contract for asset_locations persistence.
type LocationRepository interface {
	Upsert(ctx context.Context, loc *Location) error
	GetPrimary(ctx context.Context, assetID string) (*Location, error)
	ListByAsset(ctx context.Context, assetID string) ([]*Location, error)
	SetPrimary(ctx context.Context, assetID string, kind LocationKind) error
	Delete(ctx context.Context, assetID string, kind LocationKind) error
	DeleteAll(ctx context.Context, assetID string) error
}

// ProcessingRepository is the contract for asset_processing persistence.
type ProcessingRepository interface {
	Start(ctx context.Context, assetID, step string) error
	Complete(ctx context.Context, assetID, step string) error
	Fail(ctx context.Context, assetID, step, errMsg string) error
	Transition(ctx context.Context, assetID, step string, from, to ProcessingStatus) error
	Get(ctx context.Context, assetID, step string) (*ProcessingRecord, error)
	GetByAssetID(ctx context.Context, assetID string) ([]ProcessingRecord, error)
	GetFailed(ctx context.Context) ([]ProcessingRecord, error)
	Delete(ctx context.Context, assetID, step string) error
	DeleteAll(ctx context.Context, assetID string) error
}

// VersionRepository is the contract for asset_versions persistence.
type VersionRepository interface {
	GetCurrent(ctx context.Context, assetID string) (*Version, error)
	List(ctx context.Context, assetID string) ([]Version, error)
	Append(ctx context.Context, v *Version) error
}

// Store is the high-level unified CRUD repository for assets (with nested entities).
type Store interface {
	Get(ctx context.Context, id string) (*Details, error)
	List(ctx context.Context, filter Filter) ([]*Summary, error)
	Save(ctx context.Context, details *Details) error
	Delete(ctx context.Context, id string) error
}

// ArtifactStore manages metadata for stored artifacts.
type ArtifactStore interface {
	Create(ctx context.Context, a *Artifact) error
	Get(ctx context.Context, id string) (*Artifact, error)
	GetBySHA256(ctx context.Context, sha256 string) (*Artifact, error)
	UpdateStatus(ctx context.Context, id string, status ArtifactStatus, sha256 string, sizeBytes int64) error
	ListByJob(ctx context.Context, jobID string) ([]*Artifact, error)
}

// DeliveryStore manages delivery records.
type DeliveryStore interface {
	Create(ctx context.Context, d *Delivery) error
	Get(ctx context.Context, id string) (*Delivery, error)
	Update(ctx context.Context, d *Delivery) error
	ListPending(ctx context.Context) ([]*Delivery, error)
}

// ── AssetStoreSQLite ────────────────────────────────────────────────

// AssetStoreSQLite is the SQLite-backed implementation of the Store interface.
// It also provides folder, location, processing, and version repositories.
type AssetStoreSQLite struct {
	db  *sql.DB
	log *zap.Logger
}

// NewAssetStoreSQLite creates a new AssetStoreSQLite with the given database and logger.
func NewAssetStoreSQLite(db *sql.DB, log *zap.Logger) *AssetStoreSQLite {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetStoreSQLite{db: db, log: log}
}

// ── Service Class ───────────────────────────────────────────────────

type Service struct {
	store Store
	log   *zap.Logger
}

func NewService(store Store, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{store: store, log: log}
}

func (s *Service) Get(ctx context.Context, id string) (*Details, error) {
	return s.store.Get(ctx, id)
}

func (s *Service) List(ctx context.Context, filter Filter) ([]*Summary, error) {
	return s.store.List(ctx, filter)
}

func (s *Service) Save(ctx context.Context, details *Details) error {
	return s.store.Save(ctx, details)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

func (s *Service) Repository() Repository {
	if sqliteStore, ok := s.store.(*AssetStoreSQLite); ok {
		return sqliteStore.AssetRepository()
	}
	return nil
}

func (s *Service) LocationRepository() LocationRepository {
	if sqliteStore, ok := s.store.(*AssetStoreSQLite); ok {
		return sqliteStore.LocationRepository()
	}
	return nil
}

func (s *Service) ProcessingRepository() ProcessingRepository {
	if sqliteStore, ok := s.store.(*AssetStoreSQLite); ok {
		return sqliteStore.ProcessingRepository()
	}
	return nil
}

func (s *Service) VersionRepository() VersionRepository {
	if sqliteStore, ok := s.store.(*AssetStoreSQLite); ok {
		return sqliteStore.VersionRepository()
	}
	return nil
}

// mediaAssetColumns defines the columns selected from media_assets table.
//
// PR 1 (June 2026, Lifecycle state SSOT): the legacy `status` column
// is RETIRED as part of the canonical-lifecycle rewrite. Migration
// 101 normalises the data; migration 102 drops the column in
// production. The projection here reads lifecycle_state directly,
// so callers always observe the canonical-cased value at lookup
// time (the legacy COALESCE(status, …) fallback path is gone).
// For fresh test fixtures that never had the `status` column,
// removing it from the projection keeps SELECT — Scan alignment
// exact (the matching scanner in clips_repository.go has already
// dropped the corresponding destination).
const mediaAssetColumns = `
	id,
	COALESCE(source, '') AS source,
	COALESCE(name, '') AS name,
	COALESCE(tags, '[]') AS tags,
	COALESCE(tags_norm, '') AS tags_norm,
	COALESCE(embedding_json, '[]') AS embedding_json,
	COALESCE(duration_ms, 0) AS duration_ms,
	COALESCE(url, '') AS url,
	COALESCE(media_type, '') AS media_type,
	COALESCE(local_path, '') AS local_path,
	COALESCE(relative_path, '') AS relative_path,
	COALESCE(drive_file_id, '') AS drive_file_id,
	COALESCE(folder_id, '') AS drive_folder_id,
	COALESCE(drive_link, '') AS drive_link,
	COALESCE(download_link, '') AS download_link,
	COALESCE(file_hash, '') AS file_hash,
	COALESCE(metadata_json, '{}') AS metadata_json,
	COALESCE(visual_embedding, '[]') AS visual_embedding,
	COALESCE(transcript_embedding, '[]') AS transcript_embedding,
	created_at,
	COALESCE(updated_at, '') AS updated_at,
	COALESCE(width, 0) AS width,
	COALESCE(height, 0) AS height,
	COALESCE(lifecycle_state, 'ACTIVE') AS lifecycle_state,
	COALESCE(deleted_at, '') AS deleted_at,
	COALESCE(folder_id, '') AS folder_id,
	COALESCE(parent_folder_id, '') AS parent_folder_id,
	COALESCE(folder_path, '') AS folder_path,
	COALESCE(category, '') AS category,
	COALESCE(group_name, '') AS group_name,
	COALESCE(filename, '') AS filename,
	COALESCE(error, '') AS error,
	COALESCE(thumb_url, '') AS thumb_url,
	COALESCE(phash, '') AS phash,
	COALESCE(search_text, '') AS search_text,
	COALESCE(scene_type, '') AS scene_type,
	COALESCE(quality_score, 0.0) AS quality_score,
	COALESCE(reuse_count, 0) AS reuse_count,
	COALESCE(last_used_at, '') AS last_used_at`

const clipFolderColumns = `id, source, COALESCE(source_url, '') AS source_url, COALESCE(video_id, '') AS video_id, COALESCE(folder_id, '') AS folder_id, COALESCE(folder_path, '') AS folder_path, COALESCE(local_folder_path, '') AS local_folder_path, COALESCE(group_name, '') AS group_name, COALESCE(manifest_txt_path, '') AS manifest_txt_path, COALESCE(manifest_json_path, '') AS manifest_json_path, clip_count, processed_count, failed_count, skipped_count, COALESCE(last_error, '') AS last_error, COALESCE(metadata, '{}') AS metadata, created_at, updated_at`

// SoftDeleteFilter returns the canonical SQL fragment that excludes
// soft-deleted rows from query results.
//
// PR 1 (June 2026, Lifecycle state SSOT): the canonical tombstone is
// UPPERCASE 'DELETED'. Pre-PR1 readers accepted both 'deleted' and
// 'DELETED' (mixed-case writers); post-PR1 history rows are rewritten
// to UPPERCASE by migration 101, so a single equality check is enough.
// Compatibility with the pre-101 lower-case value is dropped because
// no production writer emits it anymore and migration 101 is a hard
// pre-condition for the canonical enum to be SSOT.
func SoftDeleteFilter() string {
	return "lifecycle_state != 'DELETED'"
}

func buildMediaAssetQuery(source string) string {
	query := "SELECT " + mediaAssetColumns + " FROM media_assets WHERE " + SoftDeleteFilter()
	if source != "" && source != "all" && source != "unified" {
		query += " AND source = ?"
	}
	return query
}

func buildClipFolderQuery(source string) string {
	query := "SELECT " + clipFolderColumns + " FROM clip_folders"
	if source != "" && source != "all" && source != "unified" {
		query += " WHERE source = ?"
	}
	return query
}

func clipSearchColumns() []string {
	return []string{
		"tags",
		"name",
		"search_text",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clean_title')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clip_summary')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.hook')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.topics')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.speakers')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.mentioned_people')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.people')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.clip_tags')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.search_keywords')",
		"json_extract(COALESCE(metadata_json,'{}'), '$.embedding_text')",
	}
}

func (s *AssetStoreSQLite) GetFolderChildren(ctx context.Context, parentID string) ([]*Asset, error) {
	query := `SELECT ` + mediaAssetColumns + `
		FROM media_assets
		WHERE ` + SoftDeleteFilter() + ` AND parent_folder_id = ?
		ORDER BY name ASC`

	rows, err := s.db.QueryContext(ctx, query, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clips []*Asset
	for rows.Next() {
		clip, err := scanCanonicalAssetRows(rows)
		if err != nil {
			s.log.Error("failed to scan clip", zap.Error(err))
			continue
		}
		clips = append(clips, clip)
	}

	return clips, rows.Err()
}

// FindByPHash searches for a clip with the given perceptual hash (canonical column after migration 059).
// Returns the clip ID if found, empty string if not.
func (s *AssetStoreSQLite) FindByPHash(ctx context.Context, phash string) (string, error) {
	if phash == "" {
		return "", nil
	}
	var id string
	query := `SELECT id FROM media_assets WHERE phash = ? AND ` + SoftDeleteFilter() + ` LIMIT 1`
	err := s.db.QueryRowContext(ctx, query, phash).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("FindByPHash: %w", err)
	}
	return id, nil
}

// MarkUsed marks a clip as used, incrementing reuse_count and setting last_used_at
// on the canonical columns (migration 059).
func (s *AssetStoreSQLite) MarkUsed(ctx context.Context, clipID string) error {
	if clipID == "" {
		return nil
	}
	now := timeutil.FormatRFC3339(time.Now())
	_, err := s.db.ExecContext(ctx, `
		UPDATE media_assets
		SET reuse_count = reuse_count + 1,
		    last_used_at = ?
		WHERE id = ?
	`, now, clipID)
	return err
}

// MarkClipsUsed marks multiple clips as used in a single operation.
func (s *AssetStoreSQLite) MarkClipsUsed(ctx context.Context, clipIDs []string) error {
	for _, id := range clipIDs {
		if err := s.MarkUsed(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
