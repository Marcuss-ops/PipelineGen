// Package assetlocations provides the concrete SQLite-backed implementation
// of asset.LocationRepository. All public domain types (Location,
// LocationKind, LocationRepository) come from the canonical domain
// package internal/core/domain/asset — this package intentionally
// declares NO duplicate types.
//
// Method semantics, including transactional outbox emission, are
// documented on the asset.LocationRepository interface.
package assetlocations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/core/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/hashutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"
)

// Repository is the SQLite-backed implementation of asset.LocationRepository.
type Repository struct {
	db  *sql.DB
	log *zap.Logger
}

// Compile-time guard — concrete *Repository must satisfy the canonical interface.
var _ asset.LocationRepository = (*Repository)(nil)

// New wraps db in a Repository. log may be nil (zap.NewNop is used). New
// code should prefer this constructor.
func New(db *sql.DB, log *zap.Logger) *Repository {
	if log == nil {
		log = zap.NewNop()
	}
	return &Repository{db: db, log: log}
}

// NewRepository is the legacy constructor retained for backward compat
// with the previous API. Delegates to New(db, nil). New code should use
// New directly with an explicit logger.
func NewRepository(db *sql.DB) *Repository {
	return New(db, nil)
}

// selectColumns enumerates every column in asset_locations (after
// migration 061 added external_id/access_url/download_url). Single
// source of truth for all SELECTs.
const selectColumns = `
	id, asset_id, location_kind, uri,
	external_id, access_url, download_url,
	mime_type, file_size_bytes, file_hash, is_primary,
	created_at, updated_at
`

type scanner interface {
	Scan(dest ...any) error
}

// scanLocation reads a single asset_locations row into a canonical
// *asset.Location. Mirrors the assetrepo scanner.go pattern.
func scanLocation(s scanner) (*asset.Location, error) {
	var loc asset.Location
	var kindStr string
	var isPrimaryInt int
	var createdAtStr, updatedAtStr string

	err := s.Scan(
		&loc.ID, &loc.AssetID, &kindStr, &loc.URI,
		&loc.ExternalID, &loc.AccessURL, &loc.DownloadURL,
		&loc.MimeType, &loc.FileSizeBytes, &loc.FileHash, &isPrimaryInt,
		&createdAtStr, &updatedAtStr,
	)
	if err != nil {
		return nil, err
	}
	loc.LocationKind = asset.LocationKind(kindStr)
	loc.IsPrimary = isPrimaryInt == 1
	loc.CreatedAt = timeutil.ParseRFC3339(createdAtStr)
	loc.UpdatedAt = timeutil.ParseRFC3339(updatedAtStr)
	return &loc, nil
}

// ── CRUD ────────────────────────────────────────────────────────────────

// Upsert inserts or replaces a location record.
func (r *Repository) Upsert(ctx context.Context, loc *asset.Location) error {
	if loc == nil || loc.AssetID == "" || loc.URI == "" {
		return asset.ErrInvalidID
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetlocations.Upsert begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	if loc.CreatedAt.IsZero() {
		loc.CreatedAt = now
	}
	loc.UpdatedAt = now

	isPrimary := 0
	if loc.IsPrimary {
		isPrimary = 1
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO asset_locations (
			asset_id, location_kind, uri,
			external_id, access_url, download_url,
			mime_type, file_size_bytes, file_hash, is_primary,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri             = excluded.uri,
			external_id     = excluded.external_id,
			access_url      = excluded.access_url,
			download_url    = excluded.download_url,
			mime_type       = excluded.mime_type,
			file_size_bytes = excluded.file_size_bytes,
			file_hash       = excluded.file_hash,
			is_primary      = excluded.is_primary,
			updated_at      = excluded.updated_at
	`,
		loc.AssetID, string(loc.LocationKind), loc.URI,
		loc.ExternalID, loc.AccessURL, loc.DownloadURL,
		loc.MimeType, loc.FileSizeBytes, loc.FileHash, isPrimary,
		timeutil.FormatRFC3339(loc.CreatedAt), nowStr,
	)
	if err != nil {
		return fmt.Errorf("assetlocations.Upsert(%s, %s): %w", loc.AssetID, loc.LocationKind, err)
	}

	if err := writeOutbox(ctx, tx, loc.AssetID, "location.upserted", loc); err != nil {
		return fmt.Errorf("assetlocations.Upsert outbox: %w", err)
	}

	return tx.Commit()
}

// GetPrimary returns the primary location (is_primary=1) for an asset.
func (r *Repository) GetPrimary(ctx context.Context, assetID string) (*asset.Location, error) {
	if assetID == "" {
		return nil, asset.ErrInvalidID
	}
	row := r.db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM asset_locations WHERE asset_id = ? AND is_primary = 1`,
		assetID)
	loc, err := scanLocation(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("assetlocations.GetPrimary(%s): %w", assetID, err)
	}
	return loc, nil
}

// ListByAsset returns all location records for an asset, primary first.
func (r *Repository) ListByAsset(ctx context.Context, assetID string) ([]*asset.Location, error) {
	if assetID == "" {
		return nil, asset.ErrInvalidID
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM asset_locations WHERE asset_id = ? ORDER BY is_primary DESC, location_kind`,
		assetID)
	if err != nil {
		return nil, fmt.Errorf("assetlocations.ListByAsset(%s): %w", assetID, err)
	}
	defer rows.Close()

	var out []*asset.Location
	for rows.Next() {
		loc, err := scanLocation(rows)
		if err != nil {
			return nil, fmt.Errorf("assetlocations.ListByAsset scan: %w", err)
		}
		out = append(out, loc)
	}
	return out, rows.Err()
}

// SetPrimary atomically unmarks the current primary (if any) and marks
// the (assetID, kind) location as primary.
func (r *Repository) SetPrimary(ctx context.Context, assetID string, kind asset.LocationKind) error {
	if assetID == "" || kind == "" {
		return asset.ErrInvalidID
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetlocations.SetPrimary(%s, %s) begin: %w", assetID, kind, err)
	}
	defer tx.Rollback()

	nowStr := timeutil.FormatRFC3339(time.Now())

	// Unset the existing primary.
	if _, err := tx.ExecContext(ctx,
		`UPDATE asset_locations SET is_primary = 0, updated_at = ? WHERE asset_id = ? AND is_primary = 1`,
		nowStr, assetID); err != nil {
		return fmt.Errorf("assetlocations.SetPrimary unset: %w", err)
	}

	// Set the new primary.
	res, err := tx.ExecContext(ctx,
		`UPDATE asset_locations SET is_primary = 1, updated_at = ? WHERE asset_id = ? AND location_kind = ?`,
		nowStr, assetID, string(kind))
	if err != nil {
		return fmt.Errorf("assetlocations.SetPrimary set: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return asset.ErrNotFound
	}

	if err := writeOutbox(ctx, tx, assetID, "location.primary_set",
		struct{ AssetID, LocationKind string }{assetID, string(kind)}); err != nil {
		return fmt.Errorf("assetlocations.SetPrimary outbox: %w", err)
	}
	return tx.Commit()
}

// Delete removes a single (assetID, kind) location row.
func (r *Repository) Delete(ctx context.Context, assetID string, kind asset.LocationKind) error {
	if assetID == "" || kind == "" {
		return asset.ErrInvalidID
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetlocations.Delete begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`DELETE FROM asset_locations WHERE asset_id = ? AND location_kind = ?`,
		assetID, string(kind))
	if err != nil {
		return fmt.Errorf("assetlocations.Delete(%s, %s): %w", assetID, kind, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return asset.ErrNotFound
	}

	if err := writeOutbox(ctx, tx, assetID, "location.deleted",
		struct{ AssetID, LocationKind string }{assetID, string(kind)}); err != nil {
		return fmt.Errorf("assetlocations.Delete outbox: %w", err)
	}
	return tx.Commit()
}

// DeleteAll removes every location row for assetID.
func (r *Repository) DeleteAll(ctx context.Context, assetID string) error {
	if assetID == "" {
		return asset.ErrInvalidID
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetlocations.DeleteAll begin: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`DELETE FROM asset_locations WHERE asset_id = ?`, assetID)
	if err != nil {
		return fmt.Errorf("assetlocations.DeleteAll(%s): %w", assetID, err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return asset.ErrNotFound
	}

	if err := writeOutbox(ctx, tx, assetID, "location.all_deleted",
		struct{ AssetID string }{assetID}); err != nil {
		return fmt.Errorf("assetlocations.DeleteAll outbox: %w", err)
	}
	return tx.Commit()
}

// ── Transactional API ───────────────────────────────────────────────────

// Tx exposes the underlying transaction for compound operations across
// multiple locations (and across tables).
type Tx struct {
	tx  *sql.Tx
	log *zap.Logger
}

// OnCommit schedules an outbox event to be written if the transaction
// commits successfully.
func (t *Tx) OnCommit(ctx context.Context, assetID, event string, payload any) error {
	return writeOutbox(ctx, t.tx, assetID, event, payload)
}

// Commit finalises the transaction.
func (t *Tx) Commit() error { return t.tx.Commit() }

// Rollback undoes the transaction.
func (t *Tx) Rollback() error { return t.tx.Rollback() }

// WithTx begins a transaction and runs fn with a Tx handle. If fn returns
// an error, the transaction is rolled back; otherwise it is committed.
// Outbox events scheduled via Tx.OnCommit share the transaction.
func (r *Repository) WithTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("assetlocations.WithTx begin: %w", err)
	}
	defer tx.Rollback()
	if err := fn(&Tx{tx: tx, log: r.log}); err != nil {
		return err
	}
	return tx.Commit()
}

// ── Outbox helpers ──────────────────────────────────────────────────────

// writeOutbox enters one row into outbox_events, transaction-shared with
// the caller's mutating SQL. Same signature as assetrepo.writeOutbox
// so callers can compose both pipelines.
func writeOutbox(ctx context.Context, tx *sql.Tx, aggregateID, event string, payload any) error {
	var payloadJSON []byte
	if payload == nil {
		payloadJSON = []byte("{}")
	} else {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal outbox payload: %w", err)
		}
		payloadJSON = b
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (id, aggregate_id, event_type, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, outboxID(), aggregateID, event, string(payloadJSON),
		timeutil.FormatRFC3339(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("write outbox row: %w", err)
	}
	return nil
}

// outboxID matches the project convention used in jobs/events.go (UnixNano
// + RandomString suffix). Avoids collisions on retries within the same
// nanosecond.
func outboxID() string {
	return fmt.Sprintf("outbox_%d_%s", time.Now().UnixNano(), hashutil.RandomString(6))
}
