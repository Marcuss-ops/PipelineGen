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
	out.LegacyTimestamp = parseCatalogTimeInto(&out.FirstSeenAt, firstSeen)
	out.LegacyTimestamp = parseCatalogTimeInto(&out.LastSeenAt, lastSeen) || out.LegacyTimestamp
	out.LegacyTimestamp = parseCatalogTimeInto(&out.LastRefreshAt, lastRefresh) || out.LegacyTimestamp
	out.LegacyTimestamp = parseCatalogTimeInto(&out.CreatedAt, createdAt) || out.LegacyTimestamp
	out.LegacyTimestamp = parseCatalogTimeInto(&out.UpdatedAt, updatedAt) || out.LegacyTimestamp
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
func (a *SQLiteEntityImageCatalogAdapter) ClaimRefresh(ctx context.Context, canonicalID string, now time.Time, lease time.Duration) (bool, error) {
	if a == nil || a.db == nil {
		return false, fmt.Errorf("entity image catalog: database unavailable")
	}
	canonicalID = capentity.NormalizePersonEntityID(canonicalID)
	if canonicalID == "" {
		return false, capentity.ErrEntityNotFound
	}
	if lease <= 0 {
		lease = time.Hour
	}
	cutoff := now.UTC().Add(-lease).Format(time.RFC3339)
	result, err := a.db.ExecContext(ctx, `
		UPDATE entity_image_catalog_entities
		SET refresh_status = 'running', last_refresh_at = ?, last_error = '', updated_at = CURRENT_TIMESTAMP
		WHERE canonical_entity_id = ?
		  AND (refresh_status != 'running' OR last_refresh_at < ?)
	`, now.UTC().Format(time.RFC3339), canonicalID, cutoff)
	if err != nil {
		return false, fmt.Errorf("entity image catalog: claim refresh: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("entity image catalog: claim refresh rows: %w", err)
	}
	return rows == 1, nil
}

// RecoverStaleRefresh moves a crashed running refresh to failed with a
// diagnostic. It is the bounded timeout side of ClaimRefresh.
func (a *SQLiteEntityImageCatalogAdapter) RecoverStaleRefresh(ctx context.Context, olderThan time.Time) (int64, error) {
	if a == nil || a.db == nil {
		return 0, fmt.Errorf("entity image catalog: database unavailable")
	}
	result, err := a.db.ExecContext(ctx, `
		UPDATE entity_image_catalog_entities
		SET refresh_status = 'failed', last_error = 'refresh lease expired', updated_at = CURRENT_TIMESTAMP
		WHERE refresh_status = 'running' AND last_refresh_at < ?
	`, olderThan.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("entity image catalog: recover stale refresh: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("entity image catalog: recover stale refresh rows: %w", err)
	}
	return rows, nil
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
		       c.quality_reason, c.content_sha256, c.verifier_version,
		       c.first_seen_at, c.last_seen_at, c.updated_at,
		       c.validation_attempts, c.last_validation_at, c.next_retry_at,
		       c.last_validation_error
		FROM entity_image_catalog_candidates c
		WHERE c.semantic_status IN (?, ?)
		  AND (
			(c.status IN (?, ?, ?) AND c.last_seen_at < datetime(?))
			OR
			(c.status = ? AND c.validation_attempts < ?
			 AND (c.next_retry_at = '' OR datetime(c.next_retry_at) <= datetime(?)))
		  )
		ORDER BY c.last_seen_at ASC, c.rank ASC, c.candidate_id ASC
		LIMIT ?
	`, capentity.CandidateSemanticAccepted, capentity.CandidateSemanticRejected,
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
			&item.QualityReason, &item.ContentSHA256, &item.VerifierVersion,
			&firstSeen, &lastSeen, &updatedAt,
			&validationAttempts, &lastValidationAt, &nextRetryAt, &item.LastValidationError,
		); err != nil {
			return nil, fmt.Errorf("entity image catalog: scan recertification candidate: %w", err)
		}
		item.LegacyTimestamp = parseCatalogTimeInto(&item.FirstSeenAt, firstSeen.String)
		item.LegacyTimestamp = parseCatalogTimeInto(&item.LastSeenAt, lastSeen.String) || item.LegacyTimestamp
		item.LegacyTimestamp = parseCatalogTimeInto(&item.UpdatedAt, updatedAt.String) || item.LegacyTimestamp
		item.FailureCount = validationAttempts
		item.LegacyTimestamp = parseCatalogTimeInto(&item.LastValidationAt, lastValidationAt.String) || item.LegacyTimestamp
		item.LegacyTimestamp = parseCatalogTimeInto(&item.NextRetryAt, nextRetryAt.String) || item.LegacyTimestamp
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
		// A successful URL probe restores the remote source. The materialization
		// row is deliberately untouched: if the old Drive asset was broken, the
		// common finalizer must acquire and persist the URL again. Keeping the
		// candidate 'broken' here made every recovered source a permanent dead
		// end, because the recertification selector only retries broken rows.
		res, err := a.db.ExecContext(ctx, `
			UPDATE entity_image_catalog_candidates
			SET status = ?,
			    last_seen_at = ?, updated_at = CURRENT_TIMESTAMP,
			    validation_attempts = 0,
			    last_validation_at = ?, next_retry_at = '',
			    last_validation_error = ''
			WHERE candidate_id = ?
		`, capentity.CandidateStatusFresh, checkedAt, checkedAt, candidateID)
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

// PruneRetiredCandidates enforces the catalog retention policy. Active,
// fresh, stale and broken rows are retained because recertification owns their
// retry lifecycle; only old rejected/retired rows are removed.
func (a *SQLiteEntityImageCatalogAdapter) PruneRetiredCandidates(ctx context.Context, cutoff time.Time) (int64, error) {
	if a == nil || a.db == nil {
		return 0, fmt.Errorf("entity image catalog: database unavailable")
	}
	result, err := a.db.ExecContext(ctx, `
		DELETE FROM entity_image_catalog_candidates
		WHERE updated_at < ?
		  AND (status = 'retired' OR semantic_status = 'rejected')`,
		cutoff.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, fmt.Errorf("entity image catalog: prune retired candidates: %w", err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("entity image catalog: prune rows affected: %w", err)
	}
	return removed, nil
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
	out.LegacyTimestamp = parseCatalogTimeInto(&out.MaterializedAt, materializedAt)
	out.LegacyTimestamp = parseCatalogTimeInto(&out.LastVerifiedAt, verifiedAt) || out.LegacyTimestamp
	out.LegacyTimestamp = parseCatalogTimeInto(&out.CreatedAt, createdAt) || out.LegacyTimestamp
	out.LegacyTimestamp = parseCatalogTimeInto(&out.UpdatedAt, updatedAt) || out.LegacyTimestamp
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
	parsed, _ := parseCatalogTimeWithLegacy(value)
	return parsed
}

func parseCatalogTimeInto(target *time.Time, value string) bool {
	parsed, legacy := parseCatalogTimeWithLegacy(value)
	*target = parsed
	return legacy
}

func parseCatalogTimeWithLegacy(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
		time.RFC3339Nano,
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, false
		}
	}
	return time.Time{}, true
}
