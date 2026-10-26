package entitycatalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

const (
	defaultEntityImageCandidateLimit = 10
	maxEntityImageCandidateLimit     = 100
)

// SQLiteEntityImageCatalogAdapter is the sole SQL owner for the durable
// Entity Image Catalog. The temporary VidRush provider cache remains a
// separate adapter/table with its own TTL semantics.
type SQLiteEntityImageCatalogAdapter struct {
	db *sql.DB
}

func NewSQLiteEntityImageCatalogAdapter(db *sql.DB) capentity.Repository {
	return &SQLiteEntityImageCatalogAdapter{db: db}
}

var _ capentity.Repository = (*SQLiteEntityImageCatalogAdapter)(nil)

func (a *SQLiteEntityImageCatalogAdapter) UpsertEntity(ctx context.Context, entity capentity.Entity) error {
	if a == nil || a.db == nil {
		return fmt.Errorf("entity image catalog: database unavailable")
	}
	identity, err := capentity.CanonicalizePersonIdentity(entity.CanonicalName, entity.CanonicalEntityID)
	if err != nil {
		return err
	}
	if err := capentity.ValidateEntity(entity); err != nil {
		return err
	}
	_, err = a.db.ExecContext(ctx, `
		INSERT INTO entity_image_catalog_entities (
			canonical_entity_id, entity_type, canonical_name,
			first_seen_at, last_seen_at, last_refresh_at, refresh_status,
			last_error, created_at, updated_at
		) VALUES (?, 'PERSON', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, '', ?, '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(canonical_entity_id) DO UPDATE SET
			entity_type = excluded.entity_type,
			canonical_name = excluded.canonical_name,
			last_seen_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
	`, identity.CanonicalEntityID, identity.CanonicalName, defaultRefreshStatus(entity.RefreshStatus))
	if err != nil {
		return fmt.Errorf("entity image catalog: upsert entity: %w", err)
	}
	return nil
}

func (a *SQLiteEntityImageCatalogAdapter) GetEntity(ctx context.Context, canonicalID string) (*capentity.Entity, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("entity image catalog: database unavailable")
	}
	canonicalID = capentity.NormalizePersonEntityID(canonicalID)
	if canonicalID == "" {
		return nil, capentity.ErrEntityNotFound
	}
	row := a.db.QueryRowContext(ctx, `
		SELECT canonical_entity_id, entity_type, canonical_name,
		       first_seen_at, last_seen_at, last_refresh_at, refresh_status,
		       last_error, created_at, updated_at
		FROM entity_image_catalog_entities
		WHERE canonical_entity_id = ?
	`, canonicalID)
	var (
		out                                                    capentity.Entity
		firstSeen, lastSeen, lastRefresh, createdAt, updatedAt string
	)
	if err := row.Scan(
		&out.CanonicalEntityID, &out.EntityType, &out.CanonicalName,
		&firstSeen, &lastSeen, &lastRefresh, &out.RefreshStatus,
		&out.LastError, &createdAt, &updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, capentity.ErrEntityNotFound
		}
		return nil, fmt.Errorf("entity image catalog: get entity: %w", err)
	}
	out.FirstSeenAt = parseCatalogTime(firstSeen)
	out.LastSeenAt = parseCatalogTime(lastSeen)
	out.LastRefreshAt = parseCatalogTime(lastRefresh)
	out.CreatedAt = parseCatalogTime(createdAt)
	out.UpdatedAt = parseCatalogTime(updatedAt)
	return &out, nil
}

func (a *SQLiteEntityImageCatalogAdapter) SetRefreshState(ctx context.Context, canonicalID, status string, refreshedAt time.Time, lastError string) error {
	if a == nil || a.db == nil {
		return fmt.Errorf("entity image catalog: database unavailable")
	}
	canonicalID = capentity.NormalizePersonEntityID(canonicalID)
	if canonicalID == "" {
		return capentity.ErrEntityNotFound
	}
	if status == "" {
		status = capentity.RefreshStatusNever
	}
	switch status {
	case capentity.RefreshStatusNever, capentity.RefreshStatusRunning, capentity.RefreshStatusSucceeded, capentity.RefreshStatusFailed:
	default:
		return fmt.Errorf("entity image catalog: invalid refresh status %q", status)
	}
	refreshValue := ""
	if !refreshedAt.IsZero() {
		refreshValue = refreshedAt.UTC().Format(time.RFC3339)
	}
	result, err := a.db.ExecContext(ctx, `
		UPDATE entity_image_catalog_entities
		SET last_refresh_at = ?, refresh_status = ?, last_error = ?, updated_at = CURRENT_TIMESTAMP
		WHERE canonical_entity_id = ?
	`, refreshValue, status, lastError, canonicalID)
	if err != nil {
		return fmt.Errorf("entity image catalog: set refresh state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("entity image catalog: inspect refresh state: %w", err)
	}
	if rows == 0 {
		return capentity.ErrEntityNotFound
	}
	return nil
}

// UpsertCandidate records one discovered candidate. The upsert mirrors the
// broken/retired status preservation with an equivalent verdict rule: a
// discovery write that carries NO pixel verdict (semantic_status 'unknown',
// the defaultSemanticStatus of an empty value) can never downgrade a durable
// accepted/rejected verdict. Re-observing a URL is a retrieval fact, not a
// re-certification; the verdict is rewritten only by a real verification
// outcome (accepted/rejected) through the same surface.
func (a *SQLiteEntityImageCatalogAdapter) UpsertCandidate(ctx context.Context, candidate capentity.Candidate) (int64, error) {
	if a == nil || a.db == nil {
		return 0, fmt.Errorf("entity image catalog: database unavailable")
	}
	if err := capentity.ValidateCandidate(candidate); err != nil {
		return 0, err
	}
	canonicalID := capentity.NormalizePersonEntityID(candidate.CanonicalEntityID)
	status := candidate.Status
	if status == "" || status == capentity.CandidateStatusActive {
		status = capentity.CandidateStatusFresh
	}
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO entity_image_catalog_candidates (
			canonical_entity_id, provider, rank, source_url, source_page_url, thumbnail_url,
			width, height, status, semantic_status, semantic_score,
			technical_score, quality_reason, first_seen_at, last_seen_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(canonical_entity_id, provider, source_url) DO UPDATE SET
			rank = excluded.rank,
			thumbnail_url = excluded.thumbnail_url,
			source_page_url = excluded.source_page_url,
			width = excluded.width,
			height = excluded.height,
			status = CASE
				WHEN entity_image_catalog_candidates.status IN ('broken', 'retired') THEN entity_image_catalog_candidates.status
				ELSE excluded.status
			END,
			semantic_status = CASE
				WHEN excluded.semantic_status = 'unknown'
					AND entity_image_catalog_candidates.semantic_status IN ('accepted', 'rejected')
					THEN entity_image_catalog_candidates.semantic_status
				ELSE excluded.semantic_status
			END,
			semantic_score = CASE
				WHEN excluded.semantic_status = 'unknown'
					AND entity_image_catalog_candidates.semantic_status IN ('accepted', 'rejected')
					THEN entity_image_catalog_candidates.semantic_score
				ELSE excluded.semantic_score
			END,
			technical_score = excluded.technical_score,
			quality_reason = CASE
				WHEN excluded.semantic_status = 'unknown'
					AND entity_image_catalog_candidates.semantic_status IN ('accepted', 'rejected')
					THEN entity_image_catalog_candidates.quality_reason
				ELSE excluded.quality_reason
			END,
			last_seen_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
	`, canonicalID, strings.TrimSpace(candidate.Provider), candidate.Rank,
		strings.TrimSpace(candidate.SourceURL), strings.TrimSpace(candidate.SourcePageURL), strings.TrimSpace(candidate.ThumbnailURL),
		candidate.Width, candidate.Height, status,
		defaultSemanticStatus(candidate.SemanticStatus), candidate.SemanticScore,
		candidate.TechnicalScore, strings.TrimSpace(candidate.QualityReason))
	if err != nil {
		return 0, fmt.Errorf("entity image catalog: upsert candidate: %w", err)
	}
	var id int64
	err = a.db.QueryRowContext(ctx, `
		SELECT candidate_id
		FROM entity_image_catalog_candidates
		WHERE canonical_entity_id = ? AND provider = ? AND source_url = ?
	`, canonicalID, strings.TrimSpace(candidate.Provider), strings.TrimSpace(candidate.SourceURL)).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("entity image catalog: read candidate id: %w", err)
	}
	return id, nil
}

func (a *SQLiteEntityImageCatalogAdapter) SetCandidateStatus(ctx context.Context, candidateID int64, status string) error {
	if a == nil || a.db == nil {
		return fmt.Errorf("entity image catalog: database unavailable")
	}
	if candidateID < 1 {
		return capentity.ErrCandidateNotFound
	}
	if err := capentity.ValidateCandidateStatus(status); err != nil {
		return err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	result, err := a.db.ExecContext(ctx, `
		UPDATE entity_image_catalog_candidates
		SET status = ?, updated_at = CURRENT_TIMESTAMP,
		    last_seen_at = CASE WHEN ? = 'fresh' THEN CURRENT_TIMESTAMP ELSE last_seen_at END,
		    validation_attempts = CASE WHEN ? = 'fresh' THEN 0 ELSE validation_attempts END,
		    last_validation_at = CASE WHEN ? = 'fresh' THEN '' ELSE last_validation_at END,
		    next_retry_at = CASE WHEN ? = 'fresh' THEN '' ELSE next_retry_at END,
		    last_validation_error = CASE WHEN ? = 'fresh' THEN '' ELSE last_validation_error END
		WHERE candidate_id = ?
	`, status, status, status, status, status, status, candidateID)
	if err != nil {
		return fmt.Errorf("entity image catalog: set candidate status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("entity image catalog: inspect candidate status: %w", err)
	}
	if rows == 0 {
		return capentity.ErrCandidateNotFound
	}
	return nil
}

func (a *SQLiteEntityImageCatalogAdapter) ListCandidates(ctx context.Context, canonicalID string, limit int) ([]capentity.Candidate, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("entity image catalog: database unavailable")
	}
	canonicalID = capentity.NormalizePersonEntityID(canonicalID)
	if canonicalID == "" {
		return nil, capentity.ErrEntityNotFound
	}
	if limit <= 0 {
		limit = defaultEntityImageCandidateLimit
	}
	if limit > maxEntityImageCandidateLimit {
		limit = maxEntityImageCandidateLimit
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT candidate_id, canonical_entity_id, provider, rank, source_url,
		       source_page_url, thumbnail_url, width, height, status, semantic_status,
		       semantic_score, technical_score, quality_reason,
		       first_seen_at, last_seen_at, updated_at
		FROM entity_image_catalog_candidates
		WHERE canonical_entity_id = ?
		ORDER BY rank ASC, candidate_id ASC
		LIMIT ?
	`, canonicalID, limit)
	if err != nil {
		return nil, fmt.Errorf("entity image catalog: list candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]capentity.Candidate, 0, limit)
	for rows.Next() {
		var candidate capentity.Candidate
		var firstSeen, lastSeen, updatedAt string
		if err := rows.Scan(
			&candidate.ID, &candidate.CanonicalEntityID, &candidate.Provider,
			&candidate.Rank, &candidate.SourceURL, &candidate.SourcePageURL, &candidate.ThumbnailURL,
			&candidate.Width, &candidate.Height, &candidate.Status,
			&candidate.SemanticStatus, &candidate.SemanticScore,
			&candidate.TechnicalScore, &candidate.QualityReason,
			&firstSeen, &lastSeen, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("entity image catalog: scan candidate: %w", err)
		}
		candidate.FirstSeenAt = parseCatalogTime(firstSeen)
		candidate.LastSeenAt = parseCatalogTime(lastSeen)
		candidate.UpdatedAt = parseCatalogTime(updatedAt)
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entity image catalog: list candidates rows: %w", err)
	}
	return out, nil
}

func (a *SQLiteEntityImageCatalogAdapter) UpsertMaterialization(ctx context.Context, materialization capentity.Materialization) error {
	if a == nil || a.db == nil {
		return fmt.Errorf("entity image catalog: database unavailable")
	}
	if err := capentity.ValidateMaterialization(materialization); err != nil {
		return err
	}
	status := materialization.Status
	if status == "" {
		status = capentity.MaterializationStatusPending
	}
	materializedAt := ""
	if !materialization.MaterializedAt.IsZero() {
		materializedAt = materialization.MaterializedAt.UTC().Format(time.RFC3339)
	}
	verifiedAt := ""
	if !materialization.LastVerifiedAt.IsZero() {
		verifiedAt = materialization.LastVerifiedAt.UTC().Format(time.RFC3339)
	}
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO entity_image_catalog_materializations (
			candidate_id, asset_id, content_sha256, drive_file_id, drive_link,
			status, materialized_at, last_verified_at, last_error,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(candidate_id) DO UPDATE SET
			asset_id = excluded.asset_id,
			content_sha256 = excluded.content_sha256,
			drive_file_id = excluded.drive_file_id,
			drive_link = excluded.drive_link,
			status = excluded.status,
			materialized_at = excluded.materialized_at,
			last_verified_at = excluded.last_verified_at,
			last_error = excluded.last_error,
			updated_at = CURRENT_TIMESTAMP
	`, materialization.CandidateID, strings.TrimSpace(materialization.AssetID),
		strings.TrimSpace(materialization.ContentSHA256), strings.TrimSpace(materialization.DriveFileID),
		strings.TrimSpace(materialization.DriveLink),
		status, materializedAt, verifiedAt, materialization.LastError)
	if err != nil {
		return fmt.Errorf("entity image catalog: upsert materialization: %w", err)
	}
	return nil
}

// ListCandidatesForRecertification returns a bounded, deterministic work set:
// stale/fresh-but-aged candidates and broken candidates whose persisted retry
// window is due.
//
// It deliberately reads ONLY the discovery row. It used to LEFT JOIN
// entity_image_catalog_materializations and reload asset_id / drive / digest
// metadata into each candidate, which made a remote-URL recertification job a
// second reader of the durable media identity (godlike/06: one owner per
// fact). A candidate is "the provider suggested this URL"; the durable asset
// identity lives in PostgreSQL (media_assets + asset_locations) and is never
// rehydrated from the discovery cache. The join also made every recertification
// batch scan the media-metadata table it must never mutate, which is the shape
// the media-identity gate exists to prevent.
func (a *SQLiteEntityImageCatalogAdapter) ListCandidatesForRecertification(ctx context.Context, now time.Time, limit, maxAttempts int) ([]capentity.RecertificationCandidate, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("entity image catalog: database unavailable")
	}
	if limit <= 0 {
		limit = defaultEntityImageCandidateLimit
	}
	if limit > maxEntityImageCandidateLimit {
		limit = maxEntityImageCandidateLimit
	}
	if maxAttempts <= 0 {
		maxAttempts = capentity.DefaultRecertificationMaxAttempts
	}
	cutoff := now.UTC().Add(-capentity.CandidateFreshAfter).Format("2006-01-02 15:04:05")
	nowValue := now.UTC().Format(time.RFC3339)
	rows, err := a.db.QueryContext(ctx, `
		SELECT c.candidate_id, c.canonical_entity_id, c.provider, c.rank,
		       c.source_url, c.thumbnail_url, c.width, c.height, c.status,
		       c.semantic_status, c.semantic_score, c.technical_score,
		       c.quality_reason, c.first_seen_at, c.last_seen_at, c.updated_at,
		       c.validation_attempts, c.last_validation_at, c.next_retry_at,
		       c.last_validation_error
		FROM entity_image_catalog_candidates c
		WHERE c.semantic_status = ?
		  AND (
			(c.status IN (?, ?, ?) AND c.last_seen_at < datetime(?))
			OR
			(c.status = ? AND c.validation_attempts < ?
			 AND (c.next_retry_at = '' OR datetime(c.next_retry_at) <= datetime(?)))
		  )
		ORDER BY c.last_seen_at ASC, c.rank ASC, c.candidate_id ASC
		LIMIT ?
	`, capentity.CandidateSemanticAccepted,
		capentity.CandidateStatusFresh, capentity.CandidateStatusActive, capentity.CandidateStatusStale,
		cutoff, capentity.CandidateStatusBroken, maxAttempts, nowValue, limit)
	if err != nil {
		return nil, fmt.Errorf("entity image catalog: list recertification candidates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]capentity.RecertificationCandidate, 0, limit)
	for rows.Next() {
		var item capentity.RecertificationCandidate
		var firstSeen, lastSeen, updatedAt sql.NullString
		var lastValidationAt, nextRetryAt sql.NullString
		var validationAttempts int
		if err := rows.Scan(
			&item.ID, &item.CanonicalEntityID, &item.Provider, &item.Rank,
			&item.SourceURL, &item.ThumbnailURL, &item.Width, &item.Height, &item.Status,
			&item.SemanticStatus, &item.SemanticScore, &item.TechnicalScore,
			&item.QualityReason, &firstSeen, &lastSeen, &updatedAt,
			&validationAttempts, &lastValidationAt, &nextRetryAt, &item.LastValidationError,
		); err != nil {
			return nil, fmt.Errorf("entity image catalog: scan recertification candidate: %w", err)
		}
		item.FirstSeenAt, item.LastSeenAt, item.UpdatedAt = parseCatalogTime(firstSeen.String), parseCatalogTime(lastSeen.String), parseCatalogTime(updatedAt.String)
		item.FailureCount = validationAttempts
		item.LastValidationAt, item.NextRetryAt = parseCatalogTime(lastValidationAt.String), parseCatalogTime(nextRetryAt.String)
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entity image catalog: recertification rows: %w", err)
	}
	return out, nil
}

func (a *SQLiteEntityImageCatalogAdapter) RecordCandidateValidation(ctx context.Context, candidateID int64, result capentity.ValidationResult) error {
	if a == nil || a.db == nil {
		return fmt.Errorf("entity image catalog: database unavailable")
	}
	if candidateID < 1 {
		return capentity.ErrCandidateNotFound
	}
	if result.CheckedAt.IsZero() {
		result.CheckedAt = time.Now().UTC()
	}
	checkedAt := result.CheckedAt.UTC().Format(time.RFC3339)
	if result.Success {
		res, err := a.db.ExecContext(ctx, `
			UPDATE entity_image_catalog_candidates
			SET status = CASE WHEN status = ? THEN ? ELSE ? END,
			    last_seen_at = ?, updated_at = CURRENT_TIMESTAMP,
			    validation_attempts = CASE WHEN status = ? THEN ? ELSE 0 END,
			    last_validation_at = ?, next_retry_at = '',
			    last_validation_error = ''
			WHERE candidate_id = ?
		`, capentity.CandidateStatusBroken, capentity.CandidateStatusBroken, capentity.CandidateStatusFresh,
			checkedAt, capentity.CandidateStatusBroken, result.FailureCount, checkedAt, candidateID)
		if err != nil {
			return fmt.Errorf("entity image catalog: record validation success: %w", err)
		}
		if rows, _ := res.RowsAffected(); rows == 0 {
			return capentity.ErrCandidateNotFound
		}
		return nil
	}
	if result.FailureCount < 1 {
		result.FailureCount = 1
	}
	nextRetryAt := ""
	if !result.NextRetryAt.IsZero() {
		nextRetryAt = result.NextRetryAt.UTC().Format(time.RFC3339)
	}
	res, err := a.db.ExecContext(ctx, `
		UPDATE entity_image_catalog_candidates
		SET status = ?, updated_at = CURRENT_TIMESTAMP,
		    validation_attempts = ?, last_validation_at = ?, next_retry_at = ?,
		    last_validation_error = ?
		WHERE candidate_id = ?
	`, capentity.CandidateStatusBroken, result.FailureCount, checkedAt, nextRetryAt, result.Error, candidateID)
	if err != nil {
		return fmt.Errorf("entity image catalog: record validation failure: %w", err)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return capentity.ErrCandidateNotFound
	}
	return nil
}

func (a *SQLiteEntityImageCatalogAdapter) GetMaterialization(ctx context.Context, candidateID int64) (*capentity.Materialization, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("entity image catalog: database unavailable")
	}
	if candidateID < 1 {
		return nil, capentity.ErrCandidateNotFound
	}
	row := a.db.QueryRowContext(ctx, `
		SELECT candidate_id, asset_id, content_sha256, drive_file_id, drive_link,
		       status, materialized_at, last_verified_at, last_error,
		       created_at, updated_at
		FROM entity_image_catalog_materializations
		WHERE candidate_id = ?
	`, candidateID)
	var out capentity.Materialization
	var materializedAt, verifiedAt, createdAt, updatedAt string
	if err := row.Scan(
		&out.CandidateID, &out.AssetID, &out.ContentSHA256, &out.DriveFileID,
		&out.DriveLink, &out.Status, &materializedAt,
		&verifiedAt, &out.LastError, &createdAt, &updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("entity image catalog: get materialization: %w", err)
	}
	out.MaterializedAt = parseCatalogTime(materializedAt)
	out.LastVerifiedAt = parseCatalogTime(verifiedAt)
	out.CreatedAt = parseCatalogTime(createdAt)
	out.UpdatedAt = parseCatalogTime(updatedAt)
	return &out, nil
}

func defaultSemanticStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return capentity.CandidateSemanticUnknown
	}
	return status
}

func defaultRefreshStatus(status string) string {
	if status == "" {
		return capentity.RefreshStatusNever
	}
	return status
}

func parseCatalogTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
