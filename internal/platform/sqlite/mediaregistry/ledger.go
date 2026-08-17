// Package mediaregistry is the SQLite adapter for the canonical registry
// ledger. It deliberately stores only registry history and projection
// metadata; transcript and summary content remain in their canonical tables.
package mediaregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

var ErrNotWired = errors.New("media registry sqlite ledger: not wired")

type Ledger struct{ db *sql.DB }

func NewLedger(db *sql.DB) (*Ledger, error) {
	if db == nil {
		return nil, errors.New("media registry sqlite ledger: nil database")
	}
	return &Ledger{db: db}, nil
}

var _ capregistry.Ledger = (*Ledger)(nil)
var _ capregistry.ProjectionReader = (*Ledger)(nil)
var _ capregistry.CountsReader = (*Ledger)(nil)
var _ capregistry.AssetContentRegistry = (*Ledger)(nil)
var _ capregistry.ProjectionSequenceAdvancer = (*Ledger)(nil)

func (l *Ledger) ReadCounts(ctx context.Context) (capregistry.Counts, error) {
	if l == nil || l.db == nil {
		return capregistry.Counts{}, ErrNotWired
	}
	var counts capregistry.Counts
	if err := l.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM media_assets
		WHERE lifecycle_state = 'ACTIVE' AND media_type != 'folder'`).Scan(&counts.Assets); err != nil {
		return counts, fmt.Errorf("read registry asset count: %w", err)
	}
	if err := l.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT asset_id) FROM asset_text_tracks
		WHERE text_kind = 'transcript' AND status = 'READY' AND is_current = 1`).Scan(&counts.Transcripts); err != nil {
		return counts, fmt.Errorf("read registry transcript count: %w", err)
	}
	if err := l.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT asset_id) FROM (
			SELECT id AS asset_id FROM media_assets
			WHERE lifecycle_state = 'ACTIVE'
			  AND COALESCE(json_extract(metadata_json, '$.semantic_description'), '') <> ''
			UNION
			SELECT asset_id FROM asset_text_tracks
			WHERE text_kind IN ('description', 'summary')
			  AND status = 'READY' AND is_current = 1
		)`).Scan(&counts.Descriptions); err != nil {
		return counts, fmt.Errorf("read registry description count: %w", err)
	}
	return counts, nil
}

func (l *Ledger) UpsertTaxonomy(ctx context.Context, t capregistry.AssetTaxonomy) error {
	if l == nil || l.db == nil {
		return ErrNotWired
	}
	return upsertTaxonomy(ctx, l.db, t)
}

func (l *Ledger) AppendEvent(ctx context.Context, event capregistry.Event) (int64, error) {
	if l == nil || l.db == nil {
		return 0, ErrNotWired
	}
	return appendEvent(ctx, l.db, event)
}

func (l *Ledger) StartRun(ctx context.Context, run capregistry.Run) error {
	if l == nil || l.db == nil {
		return ErrNotWired
	}
	if run.RunID == "" || run.RunType == "" || run.StartedAt == "" {
		return errors.New("media registry sqlite ledger: run_id, run_type and started_at are required")
	}
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO registry_runs
		(run_id, run_type, status, started_at, completed_at, git_sha,
		 parameters_json, assets_seen, assets_created, assets_updated,
		 transcripts_before, transcripts_after, descriptions_before,
		 descriptions_after, qdrant_points_before, qdrant_points_after, error)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.RunID, run.RunType, nonEmpty(run.Status, "RUNNING"), run.StartedAt, run.CompletedAt,
		run.GitSHA, defaultJSON(run.ParametersJSON), run.AssetsSeen, run.AssetsCreated,
		run.AssetsUpdated, run.TranscriptsBefore, run.TranscriptsAfter,
		run.DescriptionsBefore, run.DescriptionsAfter, run.QdrantBefore, run.QdrantAfter,
		run.Error)
	if err != nil {
		return fmt.Errorf("start registry run %q: %w", run.RunID, err)
	}
	return nil
}

func (l *Ledger) FinishRun(ctx context.Context, run capregistry.Run) error {
	if l == nil || l.db == nil {
		return ErrNotWired
	}
	if run.RunID == "" || run.Status == "" {
		return errors.New("media registry sqlite ledger: run_id and status are required")
	}
	res, err := l.db.ExecContext(ctx, `
		UPDATE registry_runs SET status=?, completed_at=NULLIF(?, ''),
		 assets_seen=?, assets_created=?, assets_updated=?, transcripts_before=?,
		 transcripts_after=?, descriptions_before=?, descriptions_after=?,
		 qdrant_points_before=?, qdrant_points_after=?, error=? WHERE run_id=?`,
		run.Status, run.CompletedAt, run.AssetsSeen, run.AssetsCreated, run.AssetsUpdated,
		run.TranscriptsBefore, run.TranscriptsAfter, run.DescriptionsBefore,
		run.DescriptionsAfter, run.QdrantBefore, run.QdrantAfter, run.Error, run.RunID)
	if err != nil {
		return fmt.Errorf("finish registry run %q: %w", run.RunID, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("finish registry run %q: not found", run.RunID)
	}
	return nil
}

func (l *Ledger) ListProjections(ctx context.Context) ([]capregistry.Projection, error) {
	if l == nil || l.db == nil {
		return nil, ErrNotWired
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT projection_id, projection_type, collection_name, alias_name, status,
		 source_registry_seq, embedding_model, embedding_dimensions, asset_count,
		 transcript_count, collection_hash, qdrant_version, created_at,
		 COALESCE(activated_at, '')
		FROM projection_registry
		ORDER BY created_at ASC, projection_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list projections: %w", err)
	}
	defer rows.Close()

	var projections []capregistry.Projection
	for rows.Next() {
		var projection capregistry.Projection
		if err := rows.Scan(
			&projection.ProjectionID,
			&projection.ProjectionType,
			&projection.CollectionName,
			&projection.AliasName,
			&projection.Status,
			&projection.SourceRegistrySeq,
			&projection.EmbeddingModel,
			&projection.EmbeddingDimensions,
			&projection.AssetCount,
			&projection.TranscriptCount,
			&projection.CollectionHash,
			&projection.QdrantVersion,
			&projection.CreatedAt,
			&projection.ActivatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan projection: %w", err)
		}
		// Older production databases use VALIDATED for the
		// pre-ACTIVE state, while the canonical domain contract names
		// the two in-memory phases VALIDATING and READY. Keep the
		// storage compatibility at this adapter boundary so the domain
		// state machine remains explicit and restart hydration works.
		if projection.Status == "VALIDATED" {
			projection.Status = string(capregistry.ProjectionReady)
		}
		projections = append(projections, projection)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projections: %w", err)
	}
	return projections, nil
}

func (l *Ledger) RegisterProjection(ctx context.Context, projection capregistry.Projection) error {
	if l == nil || l.db == nil {
		return ErrNotWired
	}
	if projection.ProjectionID == "" || projection.ProjectionType == "" || projection.CollectionName == "" || projection.Status == "" || projection.CreatedAt == "" {
		return errors.New("media registry sqlite ledger: projection identity, status and created_at are required")
	}
	storageStatus := projection.Status
	// Migration 203 production databases created the CHECK constraint
	// with VALIDATED, before the domain lifecycle was split into
	// VALIDATING -> READY. Persist both domain phases as VALIDATED;
	// ListProjections converts it back to READY on hydration.
	if storageStatus == string(capregistry.ProjectionValidating) || storageStatus == string(capregistry.ProjectionReady) {
		storageStatus = "VALIDATED"
	}
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO projection_registry
		(projection_id, projection_type, collection_name, alias_name, status,
		 source_registry_seq, embedding_model, embedding_dimensions, asset_count,
		 transcript_count, collection_hash, qdrant_version, created_at,
		 activated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''))
		ON CONFLICT(projection_id) DO UPDATE SET
		 projection_type=excluded.projection_type, collection_name=excluded.collection_name,
		 alias_name=excluded.alias_name, status=excluded.status,
		 source_registry_seq=excluded.source_registry_seq, embedding_model=excluded.embedding_model,
		 embedding_dimensions=excluded.embedding_dimensions, asset_count=excluded.asset_count,
		 transcript_count=excluded.transcript_count, collection_hash=excluded.collection_hash,
		 qdrant_version=excluded.qdrant_version, activated_at=excluded.activated_at`,
		projection.ProjectionID, projection.ProjectionType, projection.CollectionName,
		projection.AliasName, storageStatus, projection.SourceRegistrySeq,
		projection.EmbeddingModel, projection.EmbeddingDimensions, projection.AssetCount,
		projection.TranscriptCount, projection.CollectionHash, projection.QdrantVersion,
		projection.CreatedAt, projection.ActivatedAt)
	if err != nil {
		return fmt.Errorf("register projection %q: %w", projection.ProjectionID, err)
	}
	return nil
}

func (l *Ledger) RegisterBackup(ctx context.Context, backup capregistry.Backup) error {
	if l == nil || l.db == nil {
		return ErrNotWired
	}
	if backup.BackupID == "" || backup.BackupType == "" || backup.CreatedAt == "" {
		return errors.New("media registry sqlite ledger: backup identity is required")
	}
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO backup_registry
		(backup_id, backup_type, source_revision, path, remote_uri, sha256,
		 size_bytes, status, app_git_sha, qdrant_version, created_at,
		 verified_at, restored_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''))
		ON CONFLICT(backup_id) DO UPDATE SET status=excluded.status,
		 sha256=excluded.sha256, size_bytes=excluded.size_bytes,
		 verified_at=excluded.verified_at, restored_at=excluded.restored_at`,
		backup.BackupID, backup.BackupType, backup.SourceRevision, backup.Path,
		backup.RemoteURI, backup.SHA256, backup.SizeBytes, backup.Status, backup.AppGitSHA,
		backup.QdrantVersion, backup.CreatedAt, backup.VerifiedAt, backup.RestoredAt)
	if err != nil {
		return fmt.Errorf("register backup %q: %w", backup.BackupID, err)
	}
	return nil
}

func (l *Ledger) LatestEventSequence(ctx context.Context) (int64, error) {
	if l == nil || l.db == nil {
		return 0, ErrNotWired
	}
	var seq sql.NullInt64
	if err := l.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM registry_events`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("latest registry event sequence: %w", err)
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}

// LatestQdrantEventSequence advances over the registry ledger using the same
// eligibility boundary as the Qdrant asset projection. Artifact-only events
// (for example final_audio and script_json) do not create points and must not
// make an otherwise current projection appear stale.
func (l *Ledger) LatestQdrantEventSequence(ctx context.Context) (int64, error) {
	if l == nil || l.db == nil {
		return 0, ErrNotWired
	}
	var seq sql.NullInt64
	err := l.db.QueryRowContext(ctx, `
		SELECT MAX(e.seq)
		FROM registry_events e
		WHERE e.asset_id IS NULL
		   OR EXISTS (
			SELECT 1 FROM media_assets a
			WHERE a.id = e.asset_id
			  AND a.media_type != 'folder'
			  AND (a.deleted_at IS NULL OR a.deleted_at = '')
			  AND COALESCE(a.embedding_json, '') NOT IN ('', '[]', '{}')
		   )`).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("latest qdrant registry event sequence: %w", err)
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}

// AdvanceActiveProjectionSequence bumps the ACTIVE projection's
// source_registry_seq to the latest Qdrant-eligible registry sequence. It is
// the incremental-indexing counterpart to the reindex-time checkpoint: a full
// reindex records the build sequence via RegisterProjection, while this
// advances the checkpoint after each incremental upsert so the startup gate
// does not fail closed on a projection that has been kept current by the
// outbox. The update is monotonic (never rewinds) and touches only ACTIVE
// rows, so a retired/rolled-back projection is unaffected.
func (l *Ledger) AdvanceActiveProjectionSequence(ctx context.Context) error {
	if l == nil || l.db == nil {
		return ErrNotWired
	}
	seq, err := l.LatestQdrantEventSequence(ctx)
	if err != nil {
		return fmt.Errorf("advance active projection sequence: %w", err)
	}
	if _, err := l.db.ExecContext(ctx, `
		UPDATE projection_registry
		SET source_registry_seq = ?
		WHERE status = 'ACTIVE' AND source_registry_seq < ?`, seq, seq); err != nil {
		return fmt.Errorf("advance active projection sequence: %w", err)
	}
	return nil
}

// LinkContent sets media_assets.content_sha256 for assetID. Idempotent
// upsert; fails closed when the asset row does not exist (godlike/07).
func (l *Ledger) LinkContent(ctx context.Context, assetID, contentSHA256 string) error {
	if l == nil || l.db == nil {
		return ErrNotWired
	}
	return linkContent(ctx, l.db, assetID, contentSHA256)
}

// ContentForAsset returns the linked content sha256 for assetID, or "" when
// no link exists (missing asset and missing link both surface empty).
func (l *Ledger) ContentForAsset(ctx context.Context, assetID string) (string, error) {
	if l == nil || l.db == nil {
		return "", ErrNotWired
	}
	var sha256 string
	err := l.db.QueryRowContext(ctx,
		`SELECT content_sha256 FROM media_assets WHERE id = ?`, assetID).Scan(&sha256)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read content link for %q: %w", assetID, err)
	}
	return sha256, nil
}

// RegisterSource upserts a provenance record keyed by SourceID. Re-putting
// the same source refreshes the content link, version and primary flag.
func (l *Ledger) RegisterSource(ctx context.Context, src capregistry.AssetSource) error {
	if l == nil || l.db == nil {
		return ErrNotWired
	}
	return registerSource(ctx, l.db, src)
}

// SourcesForAsset returns every provenance record for assetID, primary
// sources first, then discovery order.
func (l *Ledger) SourcesForAsset(ctx context.Context, assetID string) ([]capregistry.AssetSource, error) {
	if l == nil || l.db == nil {
		return nil, ErrNotWired
	}
	rows, err := l.db.QueryContext(ctx, `
		SELECT source_id, asset_id, content_sha256, source_type, source_uri,
		 source_version, discovered_at, is_primary
		FROM media_asset_sources
		WHERE asset_id = ?
		ORDER BY is_primary DESC, discovered_at ASC`, assetID)
	if err != nil {
		return nil, fmt.Errorf("list asset sources for %q: %w", assetID, err)
	}
	defer rows.Close()
	var sources []capregistry.AssetSource
	for rows.Next() {
		var (
			src     capregistry.AssetSource
			primary int
		)
		if err := rows.Scan(&src.SourceID, &src.AssetID, &src.ContentSHA256, &src.SourceType,
			&src.SourceURI, &src.SourceVersion, &src.DiscoveredAt, &primary); err != nil {
			return nil, fmt.Errorf("scan asset source for %q: %w", assetID, err)
		}
		src.IsPrimary = primary == 1
		sources = append(sources, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate asset sources for %q: %w", assetID, err)
	}
	return sources, nil
}

func validateAssetSource(src capregistry.AssetSource) error {
	if src.SourceID == "" || src.AssetID == "" || src.SourceType == "" || src.SourceURI == "" || src.DiscoveredAt == "" {
		return fmt.Errorf("%w: source_id, asset_id, source_type, source_uri and discovered_at are required", capregistry.ErrAssetSourceInvalid)
	}
	return nil
}

func defaultJSON(value string) string { return nonEmpty(value, "{}") }

func nonEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
