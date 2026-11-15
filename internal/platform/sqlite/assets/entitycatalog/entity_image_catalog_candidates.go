package entitycatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	capentity "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
)

func (a *SQLiteEntityImageCatalogAdapter) UpsertCandidate(ctx context.Context, candidate capentity.Candidate) (int64, error) {
	if a == nil || a.db == nil {
		return 0, fmt.Errorf("entity image catalog: database unavailable")
	}
	if err := capentity.ValidateCandidate(candidate); err != nil {
		return 0, err
	}
	canonicalID := capentity.NormalizePersonEntityID(candidate.CanonicalEntityID)
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	sourceURL := capentity.NormalizeSourceURL(candidate.SourceURL)
	contentSHA256 := strings.ToLower(strings.TrimSpace(candidate.ContentSHA256))
	verifierVersion := strings.TrimSpace(candidate.VerifierVersion)
	status := candidate.Status
	if status == "" || status == capentity.CandidateStatusActive {
		status = capentity.CandidateStatusFresh
	}
	// The INSERT, the content-identity comparison and the id read must observe
	// the same row. Without a transaction a concurrent writer can change
	// rank/status/content between the statements, and the returned id can
	// belong to another writer's view.
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("entity image catalog: begin candidate upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Content identity is the verdict boundary. If the same URL now resolves
	// to different bytes, an accepted/rejected verdict about the old bytes is
	// not evidence about the new ones. A new current-verifier verdict is a real
	// re-certification and is allowed to replace the verdict together with the
	// new hash; everything else is invalidated to unknown.
	invalidateVerdict := false
	var existingContentSHA256 string
	switch err := tx.QueryRowContext(ctx, `
		SELECT content_sha256
		FROM entity_image_catalog_candidates
		WHERE canonical_entity_id = ? AND provider = ? AND source_url = ?
	`, canonicalID, provider, sourceURL).Scan(&existingContentSHA256); err {
	case nil:
		if existingContentSHA256 != "" && contentSHA256 != "" &&
			!strings.EqualFold(existingContentSHA256, contentSHA256) {
			currentVerdict := (candidate.SemanticStatus == capentity.CandidateSemanticAccepted ||
				candidate.SemanticStatus == capentity.CandidateSemanticRejected) &&
				verifierVersion == capentity.CandidateVerifierVersion
			if !currentVerdict {
				invalidateVerdict = true
				candidate.SemanticStatus = capentity.CandidateSemanticUnknown
				candidate.SemanticScore = 0
				candidate.QualityReason = "content changed; visual verdict invalidated"
				verifierVersion = ""
			}
		}
	case sql.ErrNoRows:
	default:
		return 0, fmt.Errorf("entity image catalog: read candidate content identity: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO entity_image_catalog_candidates (
			canonical_entity_id, provider, rank, source_url, source_page_url, thumbnail_url,
			width, height, status, semantic_status, semantic_score, verifier_version,
			content_sha256, technical_score, quality_reason,
			first_seen_at, last_seen_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
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
				WHEN ? = 1 THEN 'unknown'
				WHEN excluded.semantic_status = 'unknown'
					AND entity_image_catalog_candidates.semantic_status IN ('accepted', 'rejected')
					THEN entity_image_catalog_candidates.semantic_status
				ELSE excluded.semantic_status
			END,
			semantic_score = CASE
				WHEN ? = 1 THEN 0
				WHEN excluded.semantic_status = 'unknown'
					AND entity_image_catalog_candidates.semantic_status IN ('accepted', 'rejected')
					THEN entity_image_catalog_candidates.semantic_score
				ELSE excluded.semantic_score
			END,
			verifier_version = CASE
				WHEN ? = 1 THEN ''
				WHEN excluded.semantic_status = 'unknown'
					AND entity_image_catalog_candidates.semantic_status IN ('accepted', 'rejected')
					THEN entity_image_catalog_candidates.verifier_version
				ELSE excluded.verifier_version
			END,
			content_sha256 = CASE
				WHEN excluded.content_sha256 <> '' THEN excluded.content_sha256
				ELSE entity_image_catalog_candidates.content_sha256
			END,
			technical_score = excluded.technical_score,
			quality_reason = CASE
				WHEN ? = 1 THEN 'content changed; visual verdict invalidated'
				WHEN excluded.semantic_status = 'unknown'
					AND entity_image_catalog_candidates.semantic_status IN ('accepted', 'rejected')
					THEN entity_image_catalog_candidates.quality_reason
				ELSE excluded.quality_reason
			END,
			last_seen_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
	`, canonicalID, provider, candidate.Rank,
		sourceURL, strings.TrimSpace(candidate.SourcePageURL), strings.TrimSpace(candidate.ThumbnailURL),
		candidate.Width, candidate.Height, status,
		defaultSemanticStatus(candidate.SemanticStatus), candidate.SemanticScore, verifierVersion,
		contentSHA256, candidate.TechnicalScore, strings.TrimSpace(candidate.QualityReason),
		invalidateVerdict, invalidateVerdict, invalidateVerdict, invalidateVerdict)
	if err != nil {
		return 0, fmt.Errorf("entity image catalog: upsert candidate: %w", err)
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		SELECT candidate_id
		FROM entity_image_catalog_candidates
		WHERE canonical_entity_id = ? AND provider = ? AND source_url = ?
	`, canonicalID, provider, sourceURL).Scan(&id); err != nil {
		return 0, fmt.Errorf("entity image catalog: read candidate id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("entity image catalog: commit candidate upsert: %w", err)
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
	// A status write is a state-machine event, not an unconditional column
	// assignment. In particular, a retired row is terminal: only the explicit
	// revive event may bring it back, and a broken row cannot be relabelled
	// stale to skip its retry budget.
	var current string
	if err := a.db.QueryRowContext(ctx, `
		SELECT status FROM entity_image_catalog_candidates WHERE candidate_id = ?
	`, candidateID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return capentity.ErrCandidateNotFound
		}
		return fmt.Errorf("entity image catalog: read candidate status: %w", err)
	}
	current = strings.ToLower(strings.TrimSpace(current))
	if current == capentity.CandidateStatusRetired && status != capentity.CandidateStatusRetired {
		return capentity.ErrInvalidCandidateTransition
	}
	if current == capentity.CandidateStatusBroken && status == capentity.CandidateStatusStale {
		return capentity.ErrInvalidCandidateTransition
	}
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
	return a.ListCandidatesPage(ctx, canonicalID, 0, limit)
}

func (a *SQLiteEntityImageCatalogAdapter) ListCandidatesPage(ctx context.Context, canonicalID string, offset, limit int) ([]capentity.Candidate, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("entity image catalog: database unavailable")
	}
	canonicalID = capentity.NormalizePersonEntityID(canonicalID)
	if canonicalID == "" {
		return nil, capentity.ErrEntityNotFound
	}
	if offset < 0 {
		offset = 0
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
		       content_sha256, verifier_version,
		       first_seen_at, last_seen_at, updated_at
		FROM entity_image_catalog_candidates
		WHERE canonical_entity_id = ?
		ORDER BY rank ASC, candidate_id ASC
		LIMIT ? OFFSET ?
	`, canonicalID, limit, offset)
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
			&candidate.ContentSHA256, &candidate.VerifierVersion,
			&firstSeen, &lastSeen, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("entity image catalog: scan candidate: %w", err)
		}
		candidate.LegacyTimestamp = parseCatalogTimeInto(&candidate.FirstSeenAt, firstSeen)
		candidate.LegacyTimestamp = parseCatalogTimeInto(&candidate.LastSeenAt, lastSeen) || candidate.LegacyTimestamp
		candidate.LegacyTimestamp = parseCatalogTimeInto(&candidate.UpdatedAt, updatedAt) || candidate.LegacyTimestamp
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
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("entity image catalog: begin materialization upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
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
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO entity_image_catalog_materialization_attempts (
			candidate_id, asset_id, content_sha256, status, last_error, attempted_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, materialization.CandidateID, strings.TrimSpace(materialization.AssetID),
		strings.TrimSpace(materialization.ContentSHA256), status, strings.TrimSpace(materialization.LastError),
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("entity image catalog: append materialization attempt: %w", err)
	}
	// The materialization is the authoritative byte identity. Fill the
	// candidate's content identity when discovery had none. If the materializer
	// produced different bytes from the ones the verifier judged, the old
	// verdict is not evidence about the new bytes: invalidate it instead of
	// silently moving the accepted status onto a different payload.
	if hash := strings.ToLower(strings.TrimSpace(materialization.ContentSHA256)); capentity.IsValidContentSHA256(hash) {
		var existingContentSHA256 string
		switch err := tx.QueryRowContext(ctx, `
			SELECT content_sha256 FROM entity_image_catalog_candidates WHERE candidate_id = ?
		`, materialization.CandidateID).Scan(&existingContentSHA256); err {
		case nil:
		case sql.ErrNoRows:
			return fmt.Errorf("entity image catalog: materialization candidate %d not found: %w", materialization.CandidateID, capentity.ErrCandidateNotFound)
		default:
			return fmt.Errorf("entity image catalog: read materialization content identity: %w", err)
		}
		invalidateVerdict := existingContentSHA256 != "" && !strings.EqualFold(existingContentSHA256, hash)
		if _, err := tx.ExecContext(ctx, `
			UPDATE entity_image_catalog_candidates
			SET content_sha256 = ?,
			    semantic_status = CASE WHEN ? = 1 THEN 'unknown' ELSE semantic_status END,
			    semantic_score = CASE WHEN ? = 1 THEN 0 ELSE semantic_score END,
			    verifier_version = CASE WHEN ? = 1 THEN '' ELSE verifier_version END,
			    quality_reason = CASE WHEN ? = 1 THEN 'content changed; materialization bytes differ from verdict bytes' ELSE quality_reason END,
			    updated_at = CURRENT_TIMESTAMP
			WHERE candidate_id = ?
		`, hash, invalidateVerdict, invalidateVerdict, invalidateVerdict, invalidateVerdict, materialization.CandidateID); err != nil {
			return fmt.Errorf("entity image catalog: bind materialization content identity: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("entity image catalog: commit materialization upsert: %w", err)
	}
	return nil
}

// GetMaterializations is the batch form of GetMaterialization. It preserves
// the same row shape and timestamp parsing while issuing one query per pool.
// ListMaterializationAttempts returns the append-only writes for one
// candidate, oldest first. It is intentionally separate from the current-row
// read: callers that only need the latest state pay for one row, while audits
// can still reconstruct every transition.
func (a *SQLiteEntityImageCatalogAdapter) ListMaterializationAttempts(ctx context.Context, candidateID int64) ([]capentity.MaterializationAttempt, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("entity image catalog: database unavailable")
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT attempt_id, candidate_id, asset_id, content_sha256, status, last_error, attempted_at
		FROM entity_image_catalog_materialization_attempts
		WHERE candidate_id = ?
		ORDER BY attempt_id ASC
	`, candidateID)
	if err != nil {
		return nil, fmt.Errorf("entity image catalog: list materialization attempts: %w", err)
	}
	defer rows.Close()
	var out []capentity.MaterializationAttempt
	for rows.Next() {
		var attempt capentity.MaterializationAttempt
		var attemptedAt string
		if err := rows.Scan(&attempt.ID, &attempt.CandidateID, &attempt.AssetID, &attempt.ContentSHA256,
			&attempt.Status, &attempt.LastError, &attemptedAt); err != nil {
			return nil, fmt.Errorf("entity image catalog: scan materialization attempt: %w", err)
		}
		attempt.AttemptedAt = parseCatalogTime(attemptedAt)
		out = append(out, attempt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entity image catalog: materialization attempt rows: %w", err)
	}
	return out, nil
}

func (a *SQLiteEntityImageCatalogAdapter) GetMaterializations(ctx context.Context, candidateIDs []int64) (map[int64]*capentity.Materialization, error) {
	out := make(map[int64]*capentity.Materialization, len(candidateIDs))
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("entity image catalog: database unavailable")
	}
	if len(candidateIDs) == 0 {
		return out, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(candidateIDs)), ",")
	args := make([]any, len(candidateIDs))
	for i, id := range candidateIDs {
		args[i] = id
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT candidate_id, asset_id, content_sha256, drive_file_id, drive_link,
		       status, materialized_at, last_verified_at, last_error, created_at, updated_at
		FROM entity_image_catalog_materializations
		WHERE candidate_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("entity image catalog: batch materializations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item capentity.Materialization
		var materializedAt, verifiedAt, createdAt, updatedAt string
		if err := rows.Scan(
			&item.CandidateID, &item.AssetID, &item.ContentSHA256, &item.DriveFileID,
			&item.DriveLink, &item.Status, &materializedAt, &verifiedAt,
			&item.LastError, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("entity image catalog: scan batch materialization: %w", err)
		}
		item.LegacyTimestamp = parseCatalogTimeInto(&item.MaterializedAt, materializedAt)
		item.LegacyTimestamp = parseCatalogTimeInto(&item.LastVerifiedAt, verifiedAt) || item.LegacyTimestamp
		item.LegacyTimestamp = parseCatalogTimeInto(&item.CreatedAt, createdAt) || item.LegacyTimestamp
		item.LegacyTimestamp = parseCatalogTimeInto(&item.UpdatedAt, updatedAt) || item.LegacyTimestamp
		copy := item
		out[item.CandidateID] = &copy
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("entity image catalog: batch materialization rows: %w", err)
	}
	return out, nil
}

// ClaimRefresh acquires the single refresh lease for an entity. A running row
// whose lease is older than the caller's window can be reclaimed; a live
// refresh cannot.
