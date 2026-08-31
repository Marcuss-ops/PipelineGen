package imagesregistry

import (
	"database/sql"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

var _ detail.SourceVersionQuerier = (*ClipsRepository)(nil)

// MediaAssetColumns is the canonical SELECT projection used by every
// Get/List/Search/Resolve path in this package. It is read-only topology; all
// writes route through the composition-wired canonical asset writer.
const MediaAssetColumns = `
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
	COALESCE(drive_folder_id, '') AS drive_folder_id,
	COALESCE(drive_link, '') AS drive_link,
	COALESCE(download_link, '') AS download_link,
	COALESCE(legacy_file_md5, '') AS legacy_file_md5,
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

type ClipsRepository struct {
	*AssetStoreSQLite
	db              *sql.DB
	log             *zap.Logger
	assetCommitter  persistence.AssetCommitter
	assetMutator    persistence.AssetMutator
	canonicalWriter persistence.CanonicalAssetWriter
}

func NewClipsRepository(db *sql.DB, log *zap.Logger) *ClipsRepository {
	return &ClipsRepository{
		AssetStoreSQLite: NewAssetStoreSQLite(db, log),
		db:               db,
		log:              log,
	}
}

func NewClipsRepositoryCanonical(db *sql.DB, log *zap.Logger, canonical any) *ClipsRepository {
	repo := NewClipsRepository(db, log)
	if committer, ok := canonical.(persistence.AssetCommitter); ok {
		repo.SetCanonicalWriter(committer)
	}
	return repo
}

// SetCanonicalWriter attaches the single production writer to the repository.
// The repository keeps reader/query responsibilities; compatibility mutation
// methods delegate to these narrow ports and fail closed if they are absent.
func (r *ClipsRepository) SetCanonicalWriter(committer persistence.AssetCommitter) {
	if r == nil {
		return
	}
	r.assetCommitter = committer
	if mutator, ok := committer.(persistence.AssetMutator); ok {
		r.assetMutator = mutator
	}
	if writer, ok := committer.(persistence.CanonicalAssetWriter); ok {
		r.canonicalWriter = writer
	}
}

func (r *ClipsRepository) Canonical() *ClipsRepository { return r }

func (r *ClipsRepository) SoftDeleteFilter() string {
	return detail.SoftDeleteFilter()
}

func (r *ClipsRepository) Log() *zap.Logger { return r.log }
func (r *ClipsRepository) DB() *sql.DB      { return r.db }
