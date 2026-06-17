// Package asset_subtables is the SQLite-backed persistence layer for the
// canonical asset subtables introduced in migration 036:
//   - asset_locations  : many storage locations per asset (local/drive/s3/...)
//   - asset_processing : one row per (asset, step) tracking pipeline state
//   - asset_relations  : directed edges between assets (project→clip, ...)
//   - asset_versions   : immutable per-asset version-history audit log
//
// The package exposes a single Repository with typed CRUD per subtable. The
// kinds of methods follow two patterns:
//
//   - Tx-aware methods (`UpsertLocationTx`, etc.): use the caller's *sql.Tx
//     for atomicity with other writes (e.g. media_assets). Preferred when
//     composing with a parent write.
//
//   - Auto-tx wrappers (`UpsertLocation`, etc.): open/commit their own tx.
//     Use for standalone operations outside a parent write.
//
// Validation: every enum written to the DB is verified against the canonical
// set in internal/media/models/asset_subtables.go (mirrors the CHECK
// constraints in 036_canonical_asset_subtables.sql). Attempting to write
// an invalid enum returns an error before hitting the DB — no surprise
// constraint failures.
package asset_subtables

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"velox/go-master/internal/media/models"
)

// ErrPrimaryLocationExists is returned when an UpsertLocationTx call tries
// to mark a second location as `is_primary=true` for an asset that already
// has a primary location with a different id. There is exactly one primary
// per asset (enforced by the partial UNIQUE index
// `idx_asset_locations_one_primary`); a "switch primary" operation must
// either demote the existing primary first or use UpsertLocation in the
// same transaction that demotes the previous one.
var ErrPrimaryLocationExists = errors.New("asset_subtables: asset already has a primary location")

// Repository is the SQLite-backed implementation of the asset subtables
// persistence. Construct one per DB connection; the repository has no
// mutable state and is safe for concurrent use.
type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewRepository creates a Repository bound to the supplied *sql.DB.
// Returns nil if db is nil so callers can short-circuit in test fixtures.
func NewRepository(db *sql.DB, log *zap.Logger) *Repository {
	if db == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Repository{db: db, log: log}
}

// DB returns the underlying *sql.DB for callers that need to compose
// transactions or run ad-hoc queries. Not part of any interface — this is
// an escape hatch for advanced use.
func (r *Repository) DB() *sql.DB {
	return r.db
}

// Log returns the adapter logger.
func (r *Repository) Log() *zap.Logger {
	return r.log
}

// validateLocation rejects invalid enums before hitting the DB.
func validateLocation(loc *models.AssetLocation) error {
	if loc == nil {
		return errors.New("asset_subtables: location is nil")
	}
	if strings.TrimSpace(loc.AssetID) == "" {
		return errors.New("asset_subtables: location.AssetID is required")
	}
	if strings.TrimSpace(loc.URI) == "" {
		return errors.New("asset_subtables: location.URI is required")
	}
	if !models.ValidLocationKinds[loc.LocationKind] {
		return fmt.Errorf("asset_subtables: invalid location_kind %q", loc.LocationKind)
	}
	if loc.Status != "" && !isLocationStatusValid(loc.Status) {
		return fmt.Errorf("asset_subtables: invalid location status %q", loc.Status)
	}
	return nil
}

func isLocationStatusValid(s string) bool {
	switch s {
	case "pending", "available", "missing", "corrupted":
		return true
	}
	return false
}

// UpsertLocation inserts or updates an asset location in its own transaction.
// For tx-atomic composition, use UpsertLocationTx.
func (r *Repository) UpsertLocation(ctx context.Context, loc *models.AssetLocation) error {
	if r == nil || r.db == nil {
		return errors.New("asset_subtables.Repository: nil")
	}
	if err := validateLocation(loc); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("asset_subtables: begin tx: %w", err)
	}
	if err := r.UpsertLocationTx(ctx, tx, loc); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// locationColumns is the SELECT projection for asset_locations.
const locationColumns = `id, asset_id, location_kind, uri, COALESCE(path, '') AS path,
	COALESCE(external_id, '') AS external_id, is_primary, status,
	COALESCE(checksum, '') AS checksum, size_bytes, COALESCE(mime_type, '') AS mime_type,
	created_at, updated_at`

// UpsertLocationTx writes an asset location using the supplied *sql.Tx.
// Use this when you need atomic composition with another write (e.g. with
// media_assets UpsertClipTx).
//
// SQLite ON CONFLICT(id) DO UPDATE replaces the row by primary key.
// The unique partial index on (asset_id) WHERE is_primary = 1 enforces
// "exactly one primary per asset" — this method pre-checks that invariant
// before the INSERT to surface ErrPrimaryLocationExists as a typed error
// rather than an opaque UNIQUE-constraint failure. The pre-check is
// best-effort (race-tolerant): if a concurrent transaction wins the
// primary-slot after our SELECT but before our INSERT, the SQL UNIQUE
// trigger still catches the violation and we convert it to the same
// typed error.
func (r *Repository) UpsertLocationTx(ctx context.Context, tx *sql.Tx, loc *models.AssetLocation) error {
	if err := validateLocation(loc); err != nil {
		return err
	}
	isPrimary := 0
	if loc.IsPrimary {
		isPrimary = 1
		// Best-effort pre-check: detect a primary already present for the
		// same asset (different id) and surface a typed error before SQL.
		var existingID string
		err := tx.QueryRowContext(ctx,
			"SELECT id FROM asset_locations WHERE asset_id = ? AND is_primary = 1 AND id != ?",
			loc.AssetID, loc.ID,
		).Scan(&existingID)
		if err == nil && existingID != "" {
			return fmt.Errorf("%w: existing primary id=%s", ErrPrimaryLocationExists, existingID)
		}
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("asset_subtables: pre-check primary for %s: %w", loc.AssetID, err)
		}
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_locations (id, asset_id, location_kind, uri, path,
			external_id, is_primary, status, checksum, size_bytes, mime_type,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			asset_id      = excluded.asset_id,
			location_kind = excluded.location_kind,
			uri           = excluded.uri,
			path          = excluded.path,
			external_id   = excluded.external_id,
			is_primary    = excluded.is_primary,
			status        = excluded.status,
			checksum      = excluded.checksum,
			size_bytes    = excluded.size_bytes,
			mime_type     = excluded.mime_type,
			updated_at    = excluded.updated_at
	`, loc.ID, loc.AssetID, string(loc.LocationKind), loc.URI, loc.Path,
		loc.ExternalID, isPrimary, loc.Status, loc.Checksum, loc.SizeBytes,
		loc.MimeType, loc.CreatedAt.Format("2006-01-02 15:04:05"),
		loc.UpdatedAt.Format("2006-01-02 15:04:05"))
	if err != nil {
		// Convert a UNIQUE-constraint race into the same typed error so
		// callers don't see opaque SQL text.
		if strings.Contains(err.Error(), "idx_asset_locations_one_primary") {
			return fmt.Errorf("%w: race detected at insert", ErrPrimaryLocationExists)
		}
		return fmt.Errorf("asset_subtables: upsert location %s: %w", loc.ID, err)
	}
	return nil
}

// GetLocation fetches a single location by primary key. Returns (nil, nil)
// when no row exists — callers should not treat that as an error.
func (r *Repository) GetLocation(ctx context.Context, id string) (*models.AssetLocation, error) {
	return r.queryOneLocation(ctx, r.db, "WHERE id = ?", id)
}

// ListLocationsByAsset returns every location for an asset, ordered by
// (is_primary DESC, created_at ASC) so the primary comes first.
func (r *Repository) ListLocationsByAsset(ctx context.Context, assetID string) ([]*models.AssetLocation, error) {
	return r.queryManyLocations(ctx, r.db,
		"WHERE asset_id = ? ORDER BY is_primary DESC, created_at ASC", assetID)
}

// FindLocationByURI reverse-looks-up a location by (location_kind, uri).
// Useful for "where did we put this Drive file?"
func (r *Repository) FindLocationByURI(ctx context.Context, kind models.LocationKind, uri string) (*models.AssetLocation, error) {
	return r.queryOneLocation(ctx, r.db, "WHERE location_kind = ? AND uri = ?", string(kind), uri)
}

// DeleteLocation removes a location by primary key. Returns nil when the
// row was already absent (idempotent).
func (r *Repository) DeleteLocation(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM asset_locations WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("asset_subtables: delete location %s: %w", id, err)
	}
	return nil
}

func (r *Repository) queryOneLocation(ctx context.Context, q querier, where string, args ...any) (*models.AssetLocation, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+locationColumns+" FROM asset_locations "+where+" LIMIT 1", args...)
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: query location: %w", err)
	}
	defer rows.Close()
	loc, err := scanLocation(rows)
	if err != nil {
		return nil, err
	}
	return loc, nil
}

func (r *Repository) queryManyLocations(ctx context.Context, q querier, where string, args ...any) ([]*models.AssetLocation, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+locationColumns+" FROM asset_locations "+where, args...)
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: query locations: %w", err)
	}
	defer rows.Close()
	var out []*models.AssetLocation
	for rows.Next() {
		loc, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, loc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("asset_subtables: iterate locations: %w", err)
	}
	return out, nil
}

func scanLocation(rows *sql.Rows) (*models.AssetLocation, error) {
	var (
		loc        models.AssetLocation
		kindStr    string
		isPrimary  int
		createdAt  string
		updatedAt  string
	)
	err := rows.Scan(&loc.ID, &loc.AssetID, &kindStr, &loc.URI, &loc.Path,
		&loc.ExternalID, &isPrimary, &loc.Status, &loc.Checksum,
		&loc.SizeBytes, &loc.MimeType, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: scan location: %w", err)
	}
	loc.LocationKind = models.LocationKind(kindStr)
	loc.IsPrimary = isPrimary != 0
	if t, ok := parseRFC3339(createdAt); ok {
		loc.CreatedAt = t
	}
	if t, ok := parseRFC3339(updatedAt); ok {
		loc.UpdatedAt = t
	}
	return &loc, nil
}

// --- asset_processing ------------------------------------------------------

func validateProcessingStep(s *models.AssetProcessingStep) error {
	if s == nil {
		return errors.New("asset_subtables: processing step is nil")
	}
	if strings.TrimSpace(s.AssetID) == "" {
		return errors.New("asset_subtables: processing.AssetID is required")
	}
	if !models.ValidProcessingSteps[s.Step] {
		return fmt.Errorf("asset_subtables: invalid step %q", s.Step)
	}
	if s.Status != "" && !models.ValidProcessingStatuses[s.Status] {
		return fmt.Errorf("asset_subtables: invalid processing status %q", s.Status)
	}
	if s.MaxAttempts < 0 {
		return errors.New("asset_subtables: max_attempts must be >= 0")
	}
	return nil
}

// UpsertProcessingStep inserts/updates a (asset_id, step) row in its own tx.
func (r *Repository) UpsertProcessingStep(ctx context.Context, s *models.AssetProcessingStep) error {
	if r == nil || r.db == nil {
		return errors.New("asset_subtables.Repository: nil")
	}
	if err := validateProcessingStep(s); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("asset_subtables: begin tx: %w", err)
	}
	if err := r.UpsertProcessingStepTx(ctx, tx, s); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// UpsertProcessingStepTx is the tx-aware variant. UNIQUE(asset_id, step)
// makes the ON CONFLICT compound target resolve "rerun the same step".
func (r *Repository) UpsertProcessingStepTx(ctx context.Context, tx *sql.Tx, s *models.AssetProcessingStep) error {
	if err := validateProcessingStep(s); err != nil {
		return err
	}
	lastAttempt := nullTimeStr(s.LastAttemptAt)
	lastSuccess := nullTimeStr(s.LastSuccessAt)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_processing (id, asset_id, step, status, attempt_count,
			max_attempts, last_error, last_attempt_at, last_success_at, worker_id,
			metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, step) DO UPDATE SET
			status          = excluded.status,
			attempt_count   = excluded.attempt_count,
			max_attempts    = excluded.max_attempts,
			last_error      = excluded.last_error,
			last_attempt_at = excluded.last_attempt_at,
			last_success_at = excluded.last_success_at,
			worker_id       = excluded.worker_id,
			metadata_json   = excluded.metadata_json,
			updated_at      = excluded.updated_at
	`, s.ID, s.AssetID, string(s.Step), string(s.Status), s.AttemptCount,
		s.MaxAttempts, s.LastError, lastAttempt, lastSuccess, s.WorkerID,
		s.MetadataJSON, s.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
		s.UpdatedAt.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return fmt.Errorf("asset_subtables: upsert processing %s/%s: %w", s.AssetID, s.Step, err)
	}
	return nil
}

const processingColumns = `id, asset_id, step, status, attempt_count, max_attempts,
	COALESCE(last_error, '') AS last_error, last_attempt_at, last_success_at,
	COALESCE(worker_id, '') AS worker_id, COALESCE(metadata_json, '{}') AS metadata_json,
	created_at, updated_at`

// GetProcessingStep returns the row for a specific (asset, step) pair.
func (r *Repository) GetProcessingStep(ctx context.Context, assetID string, step models.ProcessingStep) (*models.AssetProcessingStep, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+processingColumns+" FROM asset_processing WHERE asset_id = ? AND step = ?",
		assetID, string(step))
	s, err := scanProcessing(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: get processing %s/%s: %w", assetID, step, err)
	}
	return s, nil
}

// ListProcessingByAsset returns every step for an asset, in canonical order.
func (r *Repository) ListProcessingByAsset(ctx context.Context, assetID string) ([]*models.AssetProcessingStep, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+processingColumns+" FROM asset_processing WHERE asset_id = ? ORDER BY step ASC",
		assetID)
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: list processing: %w", err)
	}
	defer rows.Close()
	var out []*models.AssetProcessingStep
	for rows.Next() {
		s, err := scanProcessing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListPendingProcessing scans for steps the sweeper should pick up.
// Returns at most `limit` rows.
func (r *Repository) ListPendingProcessing(ctx context.Context, status models.ProcessingStatus, limit int) ([]*models.AssetProcessingStep, error) {
	if limit <= 0 {
		return []*models.AssetProcessingStep{}, nil
	}
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+processingColumns+" FROM asset_processing WHERE status = ? ORDER BY updated_at ASC LIMIT ?",
		string(status), limit)
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: list pending processing: %w", err)
	}
	defer rows.Close()
	var out []*models.AssetProcessingStep
	for rows.Next() {
		s, err := scanProcessing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteProcessingStep removes a (asset, step) row. Idempotent.
func (r *Repository) DeleteProcessingStep(ctx context.Context, assetID string, step models.ProcessingStep) error {
	_, err := r.db.ExecContext(ctx,
		"DELETE FROM asset_processing WHERE asset_id = ? AND step = ?",
		assetID, string(step))
	if err != nil {
		return fmt.Errorf("asset_subtables: delete processing %s/%s: %w", assetID, step, err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanProcessing(s rowScanner) (*models.AssetProcessingStep, error) {
	var (
		step        models.AssetProcessingStep
		stepStr     string
		statusStr   string
		lastAttempt sql.NullString
		lastSuccess sql.NullString
		createdAt   string
		updatedAt   string
	)
	err := s.Scan(&step.ID, &step.AssetID, &stepStr, &statusStr, &step.AttemptCount,
		&step.MaxAttempts, &step.LastError, &lastAttempt, &lastSuccess,
		&step.WorkerID, &step.MetadataJSON, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	step.Step = models.ProcessingStep(stepStr)
	step.Status = models.ProcessingStatus(statusStr)
	if lastAttempt.Valid {
		if t, ok := parseRFC3339(lastAttempt.String); ok {
			step.LastAttemptAt = &t
		}
	}
	if lastSuccess.Valid {
		if t, ok := parseRFC3339(lastSuccess.String); ok {
			step.LastSuccessAt = &t
		}
	}
	if t, ok := parseRFC3339(createdAt); ok {
		step.CreatedAt = t
	}
	if t, ok := parseRFC3339(updatedAt); ok {
		step.UpdatedAt = t
	}
	return &step, nil
}

// --- asset_relations -------------------------------------------------------

func validateRelation(rel *models.AssetRelation) error {
	if rel == nil {
		return errors.New("asset_subtables: relation is nil")
	}
	if strings.TrimSpace(rel.ParentAssetID) == "" {
		return errors.New("asset_subtables: relation.ParentAssetID is required")
	}
	if strings.TrimSpace(rel.ChildAssetID) == "" {
		return errors.New("asset_subtables: relation.ChildAssetID is required")
	}
	if rel.ParentAssetID == rel.ChildAssetID {
		return fmt.Errorf("asset_subtables: self-relation forbidden (%s)", rel.ParentAssetID)
	}
	if !models.ValidRelationKinds[rel.RelationKind] {
		return fmt.Errorf("asset_subtables: invalid relation_kind %q", rel.RelationKind)
	}
	return nil
}

// UpsertRelation inserts/updates an asset relation in its own tx.
func (r *Repository) UpsertRelation(ctx context.Context, rel *models.AssetRelation) error {
	if r == nil || r.db == nil {
		return errors.New("asset_subtables.Repository: nil")
	}
	if err := validateRelation(rel); err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("asset_subtables: begin tx: %w", err)
	}
	if err := r.UpsertRelationTx(ctx, tx, rel); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// UpsertRelationTx is the tx-aware variant. UNIQUE(parent, child, kind) is
// the conflict target — re-applying a relation is idempotent.
func (r *Repository) UpsertRelationTx(ctx context.Context, tx *sql.Tx, rel *models.AssetRelation) error {
	if err := validateRelation(rel); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_relations (id, parent_asset_id, child_asset_id, relation_kind,
			metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(parent_asset_id, child_asset_id, relation_kind) DO UPDATE SET
			metadata_json = excluded.metadata_json
	`, rel.ID, rel.ParentAssetID, rel.ChildAssetID, string(rel.RelationKind),
		rel.MetadataJSON, rel.CreatedAt.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return fmt.Errorf("asset_subtables: upsert relation: %w", err)
	}
	return nil
}

const relationColumns = `id, parent_asset_id, child_asset_id, relation_kind,
	COALESCE(metadata_json, '{}') AS metadata_json, created_at`

// ListChildren returns the children of an asset for a given kind (or all kinds
// when kind is empty), in created_at ASC order.
func (r *Repository) ListChildren(ctx context.Context, parentID string, kind models.RelationKind) ([]*models.AssetRelation, error) {
	q := "SELECT " + relationColumns + " FROM asset_relations WHERE parent_asset_id = ?"
	args := []any{parentID}
	if kind != "" {
		q += " AND relation_kind = ?"
		args = append(args, string(kind))
	}
	q += " ORDER BY created_at ASC"
	return r.queryManyRelations(ctx, q, args...)
}

// ListParents returns the parents of an asset (reverse lookup). Mirrors
// ListChildren but uses the (child_asset_id) index.
func (r *Repository) ListParents(ctx context.Context, childID string, kind models.RelationKind) ([]*models.AssetRelation, error) {
	q := "SELECT " + relationColumns + " FROM asset_relations WHERE child_asset_id = ?"
	args := []any{childID}
	if kind != "" {
		q += " AND relation_kind = ?"
		args = append(args, string(kind))
	}
	q += " ORDER BY created_at ASC"
	return r.queryManyRelations(ctx, q, args...)
}

func (r *Repository) queryManyRelations(ctx context.Context, query string, args ...any) ([]*models.AssetRelation, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: query relations: %w", err)
	}
	defer rows.Close()
	var out []*models.AssetRelation
	for rows.Next() {
		var (
			rel     models.AssetRelation
			kindStr string
			created string
		)
		err := rows.Scan(&rel.ID, &rel.ParentAssetID, &rel.ChildAssetID, &kindStr,
			&rel.MetadataJSON, &created)
		if err != nil {
			return nil, fmt.Errorf("asset_subtables: scan relation: %w", err)
		}
		rel.RelationKind = models.RelationKind(kindStr)
		if t, ok := parseRFC3339(created); ok {
			rel.CreatedAt = t
		}
		out = append(out, &rel)
	}
	return out, rows.Err()
}

// DeleteRelation removes a specific edge (parent, child, kind).
func (r *Repository) DeleteRelation(ctx context.Context, parentID, childID string, kind models.RelationKind) error {
	_, err := r.db.ExecContext(ctx,
		"DELETE FROM asset_relations WHERE parent_asset_id = ? AND child_asset_id = ? AND relation_kind = ?",
		parentID, childID, string(kind))
	if err != nil {
		return fmt.Errorf("asset_subtables: delete relation: %w", err)
	}
	return nil
}

// --- asset_versions --------------------------------------------------------

func validateVersion(v *models.AssetVersion) error {
	if v == nil {
		return errors.New("asset_subtables: version is nil")
	}
	if strings.TrimSpace(v.AssetID) == "" {
		return errors.New("asset_subtables: version.AssetID is required")
	}
	if v.Version <= 0 {
		return fmt.Errorf("asset_subtables: version must be > 0, got %d", v.Version)
	}
	if strings.TrimSpace(v.SnapshotJSON) == "" {
		return errors.New("asset_subtables: version.SnapshotJSON is required")
	}
	if !models.ValidVersionChangeKinds[v.ChangeKind] {
		return fmt.Errorf("asset_subtables: invalid change_kind %q", v.ChangeKind)
	}
	return nil
}

// AppendVersion adds a new version entry. UNIQUE(asset_id, version) ensures
// the sequence is gap-free from the caller; re-using an existing version
// returns a UNIQUE-constraint violation (do not silently overwrite an
// immutable audit row).
func (r *Repository) AppendVersion(ctx context.Context, v *models.AssetVersion) error {
	if r == nil || r.db == nil {
		return errors.New("asset_subtables.Repository: nil")
	}
	if err := validateVersion(v); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO asset_versions (id, asset_id, version, snapshot_json,
			change_kind, changed_by, change_reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, v.ID, v.AssetID, v.Version, v.SnapshotJSON, string(v.ChangeKind),
		v.ChangedBy, v.ChangeReason, v.CreatedAt.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return fmt.Errorf("asset_subtables: append version %s/v%d: %w", v.AssetID, v.Version, err)
	}
	return nil
}

const versionColumns = `id, asset_id, version, snapshot_json, change_kind,
	COALESCE(changed_by, '') AS changed_by,
	COALESCE(change_reason, '') AS change_reason, created_at`

// GetLatestVersion returns the most recent version for an asset (highest
// version number). Returns (nil, nil) when no versions exist.
func (r *Repository) GetLatestVersion(ctx context.Context, assetID string) (*models.AssetVersion, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+versionColumns+" FROM asset_versions WHERE asset_id = ? ORDER BY version DESC LIMIT 1",
		assetID)
	v, err := scanVersion(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: get latest version: %w", err)
	}
	return v, nil
}

// GetVersion fetches a specific version of an asset. Returns (nil, nil)
// when that version was never recorded.
func (r *Repository) GetVersion(ctx context.Context, assetID string, version int) (*models.AssetVersion, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT "+versionColumns+" FROM asset_versions WHERE asset_id = ? AND version = ?",
		assetID, version)
	v, err := scanVersion(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: get version %d: %w", version, err)
	}
	return v, nil
}

// ListVersions returns every version for an asset, newest first.
func (r *Repository) ListVersions(ctx context.Context, assetID string) ([]*models.AssetVersion, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT "+versionColumns+" FROM asset_versions WHERE asset_id = ? ORDER BY version DESC",
		assetID)
	if err != nil {
		return nil, fmt.Errorf("asset_subtables: list versions: %w", err)
	}
	defer rows.Close()
	var out []*models.AssetVersion
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanVersion(s rowScanner) (*models.AssetVersion, error) {
	var (
		v         models.AssetVersion
		kindStr   string
		createdAt string
	)
	err := s.Scan(&v.ID, &v.AssetID, &v.Version, &v.SnapshotJSON, &kindStr,
		&v.ChangedBy, &v.ChangeReason, &createdAt)
	if err != nil {
		return nil, err
	}
	v.ChangeKind = models.VersionChangeKind(kindStr)
	if t, ok := parseRFC3339(createdAt); ok {
		v.CreatedAt = t
	}
	return &v, nil
}

// --- helpers ---------------------------------------------------------------

// querier is the minimal interface satisfied by *sql.DB and *sql.Tx.
// Enables the same query helpers to be reused inside or outside a tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func parseRFC3339(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	// SQLite stores text timestamps; we write in "2006-01-02 15:04:05" UTC.
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z07:00",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// nullTimeStr formats an optional time as a SQLite-friendly wallclock string
// or nil. The pointer-or-nil shape matches the domain type
// (*time.Time) so callers don't have to materialize a sql.NullTime wrapper
// at every call site.
//
// IMPORTANT: callers may pass a time in any timezone. We normalize to UTC
// before serializing because the DB stores wallclock strings WITHOUT a
// timezone marker, and the read path (parseRFC3339) treats them as UTC.
// Skipping .UTC() here would silently produce TZ-split rows: an asset
// written from a +02:00 client would land with last_attempt_at="14:00:00"
// (local) but created_at="12:00:00" (UTC via inline call), and both would
// round-trip as UTC. UTC normalization must be uniform across all four
// timestamp columns in the INSERT.
func nullTimeStr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}
