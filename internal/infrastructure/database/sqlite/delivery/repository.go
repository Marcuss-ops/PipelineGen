// Package delivery provides the canonical SQLite repository for the
// drive_folder_catalog table (F3, July 2026). The catalog acts as a
// local index of resolved Drive folder IDs so the publisher never
// needs to call Drive's folder listing API on every upload.
//
// Writers:
//   - admin drive bootstrap (F4 forward-pointer) — creates canonical
//     subdirectories under the unified media root
//   - admin drive reconcile (F5 forward-pointer) — discovers existing
//     folders on Drive and backfills the catalog
//   - publisher (F5 forward-pointer) — creates missing folders lazily
//     at first publish, then caches the resolved folder_id
//
// Readers:
//   - publisher.ResolveDestination — prefers catalog lookup over
//     Drive API calls
//   - admin drive doctor (F4 forward-pointer) — reports catalog
//     health per destination
//
// godlike/06 SSOT (one canonical owner per fact): this package is the
// canonical SOLE writer for drive_folder_catalog rows.
package delivery

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// CatalogEntry represents a single row in drive_folder_catalog.
type CatalogEntry struct {
	ID             int64
	Destination    string
	Namespace      string
	Path           string
	FolderID       string
	ParentFolderID string
	Source         string
	Status         string
	CreatedAt      string
	UpdatedAt      string
}

// Canonical source values for the source column.
const (
	SourceBootstrap  = "bootstrap"
	SourceDiscovered = "discovered"
	SourceCreated    = "created"
	SourceConfig     = "config"
)

// Canonical status values for the status column.
const (
	StatusActive  = "active"
	StatusMissing = "missing"
	StatusInvalid = "invalid"
)

// Repository wraps SQL access to the drive_folder_catalog table.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a Repository backed by db.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Upsert inserts or updates a catalog entry keyed on (destination, path).
// When a row already exists, folder_id, parent_folder_id, source, and
// status are updated; namespace and path are preserved.
//
// Returns the row ID (new or existing). SQLite with ON CONFLICT DO UPDATE
// returns the existing row's last_insert_rowid() — the ID of the row that
// was updated, NOT 0. Callers that need a definitive post-upsert ID in
// cross-DB scenarios should follow up with FindByDestinationAndPath.
//
// The entry pointer is mutated: CreatedAt (if empty) and UpdatedAt are
// set to the current time in RFC 3339 format (godlike/07 minimum-blast-
// radius: the caller owns the entry lifetime; the mutation avoids an
// extra allocation per call).
//
// Source and status values are NOT validated against the canonical
// constants — validation is deferred to the caller (the admin bootstrap
// and publisher are the canonical writers and own value correctness
// per godlike/06 SSOT).
func (r *Repository) Upsert(ctx context.Context, tx *sql.Tx, entry *CatalogEntry) (int64, error) {
	if err := validateEntry(entry); err != nil {
		return 0, err
	}
	now := timeutil.FormatRFC3339(time.Now())
	if entry.CreatedAt == "" {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now

	exec := r.db.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}

	result, err := exec(ctx, `
		INSERT INTO drive_folder_catalog
			(destination, namespace, path, folder_id, parent_folder_id, source, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(destination, path) DO UPDATE SET
			folder_id = excluded.folder_id,
			parent_folder_id = excluded.parent_folder_id,
			source = excluded.source,
			status = excluded.status,
			updated_at = excluded.updated_at
	`, entry.Destination, entry.Namespace, entry.Path,
		entry.FolderID, entry.ParentFolderID, entry.Source, entry.Status,
		entry.CreatedAt, entry.UpdatedAt)
	if err != nil {
		return 0, fmt.Errorf("delivery.catalog.Upsert(%s, %s): %w", entry.Destination, entry.Path, err)
	}
	id, _ := result.LastInsertId()
	return id, nil
}

// FindByDestination returns all catalog entries for the given destination
// key (e.g. "youtube_clip"), ordered by path ASC.
func (r *Repository) FindByDestination(ctx context.Context, destination string) ([]CatalogEntry, error) {
	if strings.TrimSpace(destination) == "" {
		return nil, fmt.Errorf("%w: destination is required", ErrInvalidEntry)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, destination, namespace, path, folder_id, parent_folder_id,
		       source, status, created_at, updated_at
		FROM drive_folder_catalog
		WHERE destination = ?
		ORDER BY path ASC
	`, destination)
	if err != nil {
		return nil, fmt.Errorf("delivery.catalog.FindByDestination(%q): %w", destination, err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// FindByDestinationAndPath returns a single catalog entry for the given
// destination + path pair, or sql.ErrNoRows if not found.
func (r *Repository) FindByDestinationAndPath(ctx context.Context, destination, path string) (*CatalogEntry, error) {
	if strings.TrimSpace(destination) == "" {
		return nil, fmt.Errorf("%w: destination is required", ErrInvalidEntry)
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: path is required", ErrInvalidEntry)
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id, destination, namespace, path, folder_id, parent_folder_id,
		       source, status, created_at, updated_at
		FROM drive_folder_catalog
		WHERE destination = ? AND path = ?
	`, destination, path)
	entry, err := scanEntry(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("delivery.catalog.FindByDestinationAndPath(%q, %q): %w", destination, path, ErrNotFound)
		}
		return nil, fmt.Errorf("delivery.catalog.FindByDestinationAndPath(%q, %q): %w", destination, path, err)
	}
	return entry, nil
}

// FindAll returns all catalog entries ordered by destination, path ASC.
func (r *Repository) FindAll(ctx context.Context) ([]CatalogEntry, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, destination, namespace, path, folder_id, parent_folder_id,
		       source, status, created_at, updated_at
		FROM drive_folder_catalog
		ORDER BY destination ASC, path ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("delivery.catalog.FindAll: %w", err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// FindByStatus returns all catalog entries with the given status,
// ordered by destination, path ASC.
func (r *Repository) FindByStatus(ctx context.Context, status string) ([]CatalogEntry, error) {
	if strings.TrimSpace(status) == "" {
		return nil, fmt.Errorf("%w: status is required", ErrInvalidEntry)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, destination, namespace, path, folder_id, parent_folder_id,
		       source, status, created_at, updated_at
		FROM drive_folder_catalog
		WHERE status = ?
		ORDER BY destination ASC, path ASC
	`, status)
	if err != nil {
		return nil, fmt.Errorf("delivery.catalog.FindByStatus(%q): %w", status, err)
	}
	defer rows.Close()
	return scanEntries(rows)
}

// Delete removes a catalog entry by destination + path. Returns
// ErrNotFound if no row matched. Accepts an optional *sql.Tx; pass
// nil for standalone execution.
func (r *Repository) Delete(ctx context.Context, tx *sql.Tx, destination, path string) error {
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("%w: destination is required", ErrInvalidEntry)
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: path is required", ErrInvalidEntry)
	}
	exec := r.db.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	result, err := exec(ctx, `
		DELETE FROM drive_folder_catalog
		WHERE destination = ? AND path = ?
	`, destination, path)
	if err != nil {
		return fmt.Errorf("delivery.catalog.Delete(%q, %q): %w", destination, path, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("delivery.catalog.Delete(%q, %q): %w", destination, path, ErrNotFound)
	}
	return nil
}

// MarkStatus updates ONLY the status column for the given (destination, path)
// pair. Used by the reconcile command (F5) to flip entries to StatusMissing
// or StatusInvalid without touching other fields. Returns ErrNotFound if no
// row matched.
func (r *Repository) MarkStatus(ctx context.Context, tx *sql.Tx, destination, path, status string) error {
	if strings.TrimSpace(destination) == "" {
		return fmt.Errorf("%w: destination is required", ErrInvalidEntry)
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: path is required", ErrInvalidEntry)
	}
	if strings.TrimSpace(status) == "" {
		return fmt.Errorf("%w: status is required", ErrInvalidEntry)
	}
	now := timeutil.FormatRFC3339(time.Now())
	exec := r.db.ExecContext
	if tx != nil {
		exec = tx.ExecContext
	}
	result, err := exec(ctx, `
		UPDATE drive_folder_catalog
		SET status = ?, updated_at = ?
		WHERE destination = ? AND path = ?
	`, status, now, destination, path)
	if err != nil {
		return fmt.Errorf("delivery.catalog.MarkStatus(%q, %q): %w", destination, path, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("delivery.catalog.MarkStatus(%q, %q): %w", destination, path, ErrNotFound)
	}
	return nil
}

// Count returns the total number of rows in the catalog.
func (r *Repository) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM drive_folder_catalog").Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("delivery.catalog.Count: %w", err)
	}
	return n, nil
}

// ── helpers ──────────────────────────────────────────────────────────────

// validateEntry checks required fields before Upsert.
func validateEntry(entry *CatalogEntry) error {
	if entry == nil {
		return fmt.Errorf("%w: entry is nil", ErrInvalidEntry)
	}
	if strings.TrimSpace(entry.Destination) == "" {
		return fmt.Errorf("%w: destination is required", ErrInvalidEntry)
	}
	if strings.TrimSpace(entry.Path) == "" {
		return fmt.Errorf("%w: path is required", ErrInvalidEntry)
	}
	return nil
}

func scanEntry(s scanner) (*CatalogEntry, error) {
	e := &CatalogEntry{}
	if err := s.Scan(&e.ID, &e.Destination, &e.Namespace, &e.Path,
		&e.FolderID, &e.ParentFolderID, &e.Source, &e.Status,
		&e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	return e, nil
}

func scanEntries(rows *sql.Rows) ([]CatalogEntry, error) {
	var entries []CatalogEntry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("delivery.catalog scan: %w", err)
		}
		entries = append(entries, *e)
	}
	return entries, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}
