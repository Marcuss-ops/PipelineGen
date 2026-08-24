// Package mediamemory (sqlite infrastructure) — candidates_repository.go
// is the canonical concrete impl of mediamemory.CandidateRepository.
//
// godlike/06 SSOT (one canonical owner per fact): the SQL ↔ Go row
// conversion for media_candidates lives ONLY here. DDL canonical home:
// migrations/sqlite/166_mediamemory_candidates.sql. Wire canonical
// home: internal/capabilities/mediamemory/types.go::MediaCandidate.
// This file is the bridge.
//
// godlike/07 NO-FAKE-AVAILABILITY: UNIQUE(provider,
// provider_asset_id) violations surface as wrapped ErrDuplicateBinding
// (re-using the canonical envelope — the semantic intent "duplicate
// of media_candidates" is fail-closed). RightsUnknown candidates
// are NEVER promoted to Hot; that decision lives at the
// AcquisitionPlanner (composition-root) boundary, not here.
//
// godlike/06 SSOT (hot/warm/cold): materialization_status is the
// canonical tier (architecture doc section 8). UpdateStatus is the
// only writer; the AcquisitionPlanner owns the tier transition
// policy. This file is the durable backing store only.
package mediamemory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
)

// candidatesRepository is the canonical concrete
// mediamemory.CandidateRepository backed by SQLite.
type candidatesRepository struct {
	db *sql.DB
}

// NewCandidatesRepository constructs the canonical repository.
func NewCandidatesRepository(db *sql.DB) mediamemory.CandidateRepository {
	return &candidatesRepository{db: db}
}

// Compile-time assertion.
var _ mediamemory.CandidateRepository = (*candidatesRepository)(nil)

const candidatesSelectColumns = `id, provider, provider_asset_id, source_url,
		thumbnail_url, title, description, duration_ms,
		candidate_score, rights_status, license_basis,
		allowed_channels, allowed_regions, owner, expiration,
		discovery_status, materialization_status, asset_id,
		created_at, updated_at`

// UpsertInsert inserts a NEW candidate row keyed by (provider,
// provider_asset_id). Conflicts on the unique key MUST surface
// as wrapped ErrDuplicateBinding — re-using the canonical
// envelope (semantic intent "duplicate row" is fail-closed).
//
// godlike/06 SSOT: this is the INSERT_ONLY path. The discovery
// worker writes-once-per-upstream-asset; a higher-level orchestrator
// (Phase-3 linker) owns the update flow for materialization_status
// transitions via UpdateStatus.
func (r *candidatesRepository) UpsertInsert(ctx context.Context, c mediamemory.MediaCandidate) (mediamemory.MediaCandidate, error) {
	if !mediamemory.IsKnownRightsStatus(c.RightsStatus) {
		return mediamemory.MediaCandidate{}, fmt.Errorf(
			"mediamemory: candidate rights_status=%q: %w",
			string(c.RightsStatus), mediamemory.ErrApprovalRequired,
		)
	}
	if !mediamemory.IsKnownDiscoveryStatus(c.DiscoveryStatus) {
		return mediamemory.MediaCandidate{}, fmt.Errorf(
			"mediamemory: candidate discovery_status=%q: not in canonical closed set",
			string(c.DiscoveryStatus),
		)
	}
	if !mediamemory.IsKnownMaterializationStatus(c.MaterializationStatus) {
		return mediamemory.MediaCandidate{}, fmt.Errorf(
			"mediamemory: candidate materialization_status=%q: not in canonical closed set",
			string(c.MaterializationStatus),
		)
	}
	now := time.Now().UTC()
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now

	allowedChannels, err := encodeStringArray(c.AllowedChannels)
	if err != nil {
		return mediamemory.MediaCandidate{}, fmt.Errorf("mediamemory: candidate %q allowed_channels JSON encode: %w", c.ID, err)
	}
	allowedRegions, err := encodeStringArray(c.AllowedRegions)
	if err != nil {
		return mediamemory.MediaCandidate{}, fmt.Errorf("mediamemory: candidate %q allowed_regions JSON encode: %w", c.ID, err)
	}

	q := `INSERT INTO media_candidates
		(` + candidatesSelectColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	row := r.db.QueryRowContext(ctx, q,
		c.ID, c.Provider, c.ProviderAssetID, c.SourceURL,
		nullableString(c.ThumbnailURL),
		nullableString(c.Title),
		nullableString(c.Description),
		c.DurationMs,
		c.CandidateScore, string(c.RightsStatus),
		nullableString(c.LicenseBasis),
		nullableString(allowedChannels),
		nullableString(allowedRegions),
		nullableString(c.Owner),
		nullableTimePtr(c.Expiration),
		string(c.DiscoveryStatus), string(c.MaterializationStatus),
		nullableString(c.AssetID),
		c.CreatedAt.Format(time.RFC3339Nano), c.UpdatedAt.Format(time.RFC3339Nano),
	)
	out, err := scanCandidateRow(row)
	if err != nil {
		if isUniqueViolation(err) {
			return mediamemory.MediaCandidate{}, fmt.Errorf(
				"mediamemory: candidate (provider=%q, asset_id=%q): %w",
				c.Provider, c.ProviderAssetID, mediamemory.ErrDuplicateBinding,
			)
		}
		return mediamemory.MediaCandidate{}, fmt.Errorf("mediamemory: insert candidate: %w", err)
	}
	return out, nil
}

// FindByID wraps ErrCandidateNotFound when the row is missing.
// godlike/07 NO-FAKE-AVAILABILITY: the row-not-found case is
// DISTINCT from ErrCandidateMaterializationFailed (which is
// reserved for stockpipeline returned-no-asset_id cases) so
// the ranker can branch correctly via errors.Is.
func (r *candidatesRepository) FindByID(ctx context.Context, id string) (mediamemory.MediaCandidate, error) {
	q := `SELECT ` + candidatesSelectColumns + ` FROM media_candidates WHERE id = ?`
	row := r.db.QueryRowContext(ctx, q, id)
	c, err := scanCandidateRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return mediamemory.MediaCandidate{}, fmt.Errorf("mediamemory: find candidate %q: %w", id, mediamemory.ErrCandidateNotFound)
		}
		return mediamemory.MediaCandidate{}, err
	}
	return c, nil
}

// ListByProvider returns cold/warm/hot candidates for a provider.
// The worker iterates to pick top-K for materialization.
func (r *candidatesRepository) ListByProvider(ctx context.Context, provider string, limit int) ([]mediamemory.MediaCandidate, error) {
	args := []any{provider}
	q := `SELECT ` + candidatesSelectColumns + ` FROM media_candidates WHERE provider = ? ORDER BY candidate_score DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return r.queryCandidates(ctx, q, args, "list by provider")
}

// ListPendingMaterialization is the Warm→Hot promotion read
// (rights_verified only — exactly the criteria the
// AcquisitionPlanner uses).
func (r *candidatesRepository) ListPendingMaterialization(ctx context.Context, limit int) ([]mediamemory.MediaCandidate, error) {
	args := []any{string(mediamemory.RightsVerified), string(mediamemory.MaterializationWarm)}
	q := `SELECT ` + candidatesSelectColumns + `
		FROM media_candidates
		WHERE rights_status = ? AND materialization_status = ?
		ORDER BY candidate_score DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	return r.queryCandidates(ctx, q, args, "list pending materialization")
}

// UpdateStatus mutates the two status columns. asset_id is
// occasionally updated alongside (Phase-3 linker's promotion
// flow surfaces asset_id once the materialize worker produces
// the canonical media_assets row).
func (r *candidatesRepository) UpdateStatus(ctx context.Context, id string, discovery mediamemory.DiscoveryStatus, mat mediamemory.MaterializationStatus) error {
	if !mediamemory.IsKnownDiscoveryStatus(discovery) {
		return fmt.Errorf(
			"mediamemory: update status for %q discovery_status=%q: not in canonical closed set",
			id, string(discovery),
		)
	}
	if !mediamemory.IsKnownMaterializationStatus(mat) {
		return fmt.Errorf(
			"mediamemory: update status for %q materialization_status=%q: not in canonical closed set",
			id, string(mat),
		)
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE media_candidates
		    SET discovery_status = ?, materialization_status = ?, updated_at = ?
		  WHERE id = ?`,
		string(discovery), string(mat), time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return fmt.Errorf("mediamemory: update status %q: %w", id, err)
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────

func (r *candidatesRepository) queryCandidates(ctx context.Context, q string, args []any, op string) ([]mediamemory.MediaCandidate, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("mediamemory: %s: %w", op, err)
	}
	defer rows.Close()
	out := make([]mediamemory.MediaCandidate, 0, 8)
	for rows.Next() {
		c, err := scanCandidateRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mediamemory: %s iterate: %w", op, err)
	}
	return out, nil
}

func scanCandidateRow(s rowScanner) (mediamemory.MediaCandidate, error) {
	var (
		c               mediamemory.MediaCandidate
		thumbnailURL    sql.NullString
		title           sql.NullString
		description     sql.NullString
		durationMs      sql.NullInt64
		rightsStatus    string
		licenseBasis    sql.NullString
		allowedChannels sql.NullString
		allowedRegions  sql.NullString
		owner           sql.NullString
		expiration      sql.NullString
		discoveryStatus string
		materStatus     string
		assetID         sql.NullString
		createdAt       string
		updatedAt       string
	)
	if err := s.Scan(
		&c.ID, &c.Provider, &c.ProviderAssetID, &c.SourceURL,
		&thumbnailURL, &title, &description, &durationMs,
		&c.CandidateScore, &rightsStatus, &licenseBasis,
		&allowedChannels, &allowedRegions, &owner, &expiration,
		&discoveryStatus, &materStatus, &assetID,
		&createdAt, &updatedAt,
	); err != nil {
		return mediamemory.MediaCandidate{}, err
	}
	c.RightsStatus = mediamemory.RightsStatus(rightsStatus)
	if !mediamemory.IsKnownRightsStatus(c.RightsStatus) {
		return mediamemory.MediaCandidate{}, fmt.Errorf(
			"mediamemory: candidate %q has unknown rights_status %q",
			c.ID, rightsStatus,
		)
	}
	c.DiscoveryStatus = mediamemory.DiscoveryStatus(discoveryStatus)
	if !mediamemory.IsKnownDiscoveryStatus(c.DiscoveryStatus) {
		return mediamemory.MediaCandidate{}, fmt.Errorf(
			"mediamemory: candidate %q has unknown discovery_status %q",
			c.ID, discoveryStatus,
		)
	}
	c.MaterializationStatus = mediamemory.MaterializationStatus(materStatus)
	if !mediamemory.IsKnownMaterializationStatus(c.MaterializationStatus) {
		return mediamemory.MediaCandidate{}, fmt.Errorf(
			"mediamemory: candidate %q has unknown materialization_status %q",
			c.ID, materStatus,
		)
	}
	if thumbnailURL.Valid {
		c.ThumbnailURL = thumbnailURL.String
	}
	if title.Valid {
		c.Title = title.String
	}
	if description.Valid {
		c.Description = description.String
	}
	if durationMs.Valid {
		c.DurationMs = durationMs.Int64
	}
	if licenseBasis.Valid {
		c.LicenseBasis = licenseBasis.String
	}
	if owner.Valid {
		c.Owner = owner.String
	}
	if assetID.Valid {
		c.AssetID = assetID.String
	}
	if allowedChannels.Valid && allowedChannels.String != "" {
		out, err := decodeStringArray(allowedChannels.String)
		if err != nil {
			return mediamemory.MediaCandidate{}, fmt.Errorf("mediamemory: candidate %q allowed_channels JSON: %w", c.ID, err)
		}
		c.AllowedChannels = out
	}
	if allowedRegions.Valid && allowedRegions.String != "" {
		out, err := decodeStringArray(allowedRegions.String)
		if err != nil {
			return mediamemory.MediaCandidate{}, fmt.Errorf("mediamemory: candidate %q allowed_regions JSON: %w", c.ID, err)
		}
		c.AllowedRegions = out
	}
	if expiration.Valid {
		t, err := parseTime(expiration.String)
		if err != nil {
			return mediamemory.MediaCandidate{}, fmt.Errorf("mediamemory: candidate %q expiration: %w", c.ID, err)
		}
		c.Expiration = &t
	}
	var err error
	if c.CreatedAt, err = parseTime(createdAt); err != nil {
		return mediamemory.MediaCandidate{}, fmt.Errorf("mediamemory: candidate %q created_at: %w", c.ID, err)
	}
	if c.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return mediamemory.MediaCandidate{}, fmt.Errorf("mediamemory: candidate %q updated_at: %w", c.ID, err)
	}
	return c, nil
}

// ── JSON helpers (godlike/06 SSOT: array fields are TEXT(JSON)) ──

func encodeStringArray(v []string) (string, error) {
	if v == nil {
		return "", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeStringArray(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// godlike/06 SSOT notes:
//   - parseTime moved to helpers.go (godlike/06 SSOT: shared with 5+ repos).
//   - sqlNullInt64 dropped (DurationMs is non-nullable in the struct).
//   - isUniqueViolation moved to helpers.go; uses strings.Contains directly.
//   - stringsContains removed (anti-pattern: re-implemented stdlib).
