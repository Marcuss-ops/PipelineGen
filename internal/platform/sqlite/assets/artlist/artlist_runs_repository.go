// Package assets — artlist_runs_repository.go (PR-ARTLIST-PERSIST-FIX, 2026-07-04)
//
// SQLite concrete for the artlist_runs aggregate writer. Schema verified
// verbatim against migrations/sqlite/001_velox_core.sql:46-62.
//
// Import-cycle resolution: this file declares its OWN RunRecord struct +
// RecordRepository interface (rather than importing artlist.RunRecord
// + artlist.RunRepository) so the import graph remains acyclic:
//
//	artlist pkg  →  sqlite/assets pkg  (existing dep via ClipsRepository + func helpers)
//	sqlite/artlist_runs_repository.go does NOT import artlist pkg
//	composition root (internal/app/artlist_runs_adapter.go) holds the
//	adapter that bridges artlist.RunRecord ↔ this local RunRecord struct.
//	Compile-time pin `var _ artlist.RunRepository = (*adapter)(nil)`
//	lives in the adapter file, NOT here.
//
// Pattern precedent: ClipsRepository (same dir) does NOT import artlist
// even though it implements artlist.AssetStore implicitly — its
// compile-time pin lives in build_bundles_artlist.go (composition
// root). We follow the same minimum-blast-radius shape.
//
// Schema (verified 2026-07-04):
//
//	artlist_runs (
//	  id              TEXT PRIMARY KEY,
//	  term            TEXT NOT NULL,
//	  status          TEXT NOT NULL DEFAULT 'queued',
//	  root_folder_id  TEXT,
//	  tag_folder_id   TEXT,
//	  requested_count INTEGER DEFAULT 0,
//	  found_count     INTEGER DEFAULT 0,
//	  processed_count INTEGER DEFAULT 0,
//	  skipped_count   INTEGER DEFAULT 0,
//	  failed_count    INTEGER DEFAULT 0,
//	  error_message   TEXT,
//	  created_at      TEXT DEFAULT (datetime('now')),
//	  updated_at      TEXT DEFAULT (datetime('now'))
//	)
//
// Note: strategy / dry_run / started_at / completed_at / last_error are
// NOT present in the canonical schema — they were aspirational fields
// in earlier drafts of the bug closure and were rejected by the schema-
// reconciliation review (PR-ARTLIST-PERSIST-FIX schema validation,
// 2026-07-04). Don't reintroduce them without a new migration.
package artlist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// RunRecord is the local (pkg-internal) RunRecord equivalent. The fields
// match artlist_runs columns 1:1 — kept local to break the
// application-layer ↔ infrastructure-layer import cycle. The
// composition root owns the adapter (artlist_runs_adapter.go) that
// translates between artlist.RunRecord and assets.RunRecord.
//
// godlike/06 SSOT (column-level reconciliation 2026-07-04): every
// field here corresponds to a single SQLite column. created_at and
// updated_at are omitted because SQLite fills them via DEFAULT
// datetime('now'); the canonical writer never sets them.
type RunRecord struct {
	RunID        string
	Term         string
	Status       string
	RootFolderID string
	TagFolderID  string
	RequestedN   int
	FoundN       int
	ProcessedN   int
	SkippedN     int
	FailedN      int
	ErrorMessage string
}

// RunRepository is the LOCAL interface — concrete implementation type.
// Stays in this package to avoid importing the application-layer
// artlist.RunRepository. The adapter pattern (composition root)
// bridges the two surfaces.
//
// godlike/06 one-owner-per-fact: this interface is internal to the
// sqlite layer — the application layer's artlist.RunRepository is the
// single canonical port seen by NewService; the adapter in
// build_bundles_artlist.go is the ONLY place the two interface names
// meet.
type RunRepository interface {
	Record(ctx context.Context, rec RunRecord) error
	// LatestRun: PR-P2-DIAGNOSTICS-REALE (July 2026) — see
	// artlist.LatestRunSummary for the canonical read-shape.
	// Returns (nil, nil) on empty-table (fresh install); returns
	// wrapped error on transport-level SQL failure.
	LatestRun(ctx context.Context) (*LatestRunRow, error)
}

// LatestRunRow is the LOCAL read-shape of one artlist_runs row.
// Stays in this package (mirrors RunRecord's local-private status)
// to break the application-layer ↔ infrastructure-layer import
// cycle. The composition-root adapter
// internal/app/artlist_runs_adapter.go is the ONE place that maps
// this struct → artlist.LatestRunSummary.
type LatestRunRow struct {
	RunID        string
	Term         string
	Status       string
	ErrorMessage string
	CreatedAt    string
}

// ArtlistRunsRepository is the SQLite-backed concrete implementation
// of RunRepository. INSERT OR REPLACE keyed on RunID (id PK) ensures
// concurrent retry of the same logical run collapses into ONE row.
//
// godlike/07 no-fake-availability: returns a wrapped error if the
// SQL execution fails so callers can errors.Is() against driver-level
// failures. Composition-time wiring in build_bundles_artlist.go MUST
// pass the same *sql.DB used by media_assets so aggregate writes
// land in the same transaction context (subject to DB-wide WAL
// semantics).
type ArtlistRunsRepository struct {
	db  *sql.DB
	log *zap.Logger
}

// NewArtlistRunsRepository builds a SQLite-backed ArtlistRunsRepository.
// Returns an error for nil db at composition time (fail-closed
// mirrors the other repo constructors' discipline; missing-log is
// treated as recoverable — zap.NewNop() is substituted).
func NewArtlistRunsRepository(db *sql.DB, log *zap.Logger) (*ArtlistRunsRepository, error) {
	if db == nil {
		return nil, errors.New("artlist_runs_repository: sql.DB is nil — production wiring requires a valid media.db.sqlite handle")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &ArtlistRunsRepository{db: db, log: log}, nil
}

// compile-time assertion: *ArtlistRunsRepository satisfies the LOCAL
// RunRepository interface declared in this package. NO
// artlist-package assertion here — that belongs to the adapter in
// composition root (mirrors the ClipsRepository precedent).
var _ RunRepository = (*ArtlistRunsRepository)(nil)

// RunRecord persists the aggregate stats for one Artlist run, keyed on
// RunID. Idempotent across concurrent retries: SQLite's INSERT OR
// REPLACE on the PRIMARY KEY collapses two retries of the same
// logical run into ONE row rather than producing duplicate
// aggregate stats.
//
// Schema verification (PR-ARTLIST-PERSIST-FIX, 2026-07-04): the 11
// placeholders here MAP VERBATIM onto the 11 writer-managed columns
// of artlist_runs (the remaining 2 — created_at + updated_at — are
// DEFAULT datetime('now') in the migration). DO NOT add columns
// here without a corresponding migration amendment.
func (r *ArtlistRunsRepository) Record(ctx context.Context, rec RunRecord) error {
	if rec.RunID == "" {
		return errors.New("artlist_runs_repository.RunRecord: RunID is required (godlike/06 SSOT: every artlist_runs row must be keyed on a non-empty RunID)")
	}
	// godlike/07 fail-fast on app layer vs relying on SQLite NOT NULL
	// constraint failure: status TEXT NOT NULL DEFAULT 'queued' per
	// migrations/sqlite/001_velox_core.sql:46-49. Returning a typed
	// diagnostic at the application seam lets callers branch on intent
	// (e.g. /api/artlist/run handler can return 400 BadRequest with the
	// diagnostic rather than letting the SQL Exec wrap leak "NOT NULL
	// constraint failed: artlist_runs.status" into the HTTP wire).
	if rec.Status == "" {
		return errors.New("artlist_runs_repository.RunRecord: Status is required (status TEXT NOT NULL DEFAULT 'queued' per migrations/sqlite/001_velox_core.sql:48)")
	}

	stmt := `INSERT OR REPLACE INTO artlist_runs (
		id, term, status, root_folder_id, tag_folder_id,
		requested_count, found_count, processed_count, skipped_count,
		failed_count, error_message
	) VALUES (?,?,?,?,?,?,?,?,?,?,?)`

	_, err := r.db.ExecContext(ctx, stmt,
		rec.RunID,
		rec.Term,
		rec.Status,
		rec.RootFolderID,
		rec.TagFolderID,
		rec.RequestedN,
		rec.FoundN,
		rec.ProcessedN,
		rec.SkippedN,
		rec.FailedN,
		rec.ErrorMessage,
	)
	if err != nil {
		r.log.Error("artlist_runs_repository.Record failed",
			zap.String("run_id", rec.RunID),
			zap.String("term", rec.Term),
			zap.Error(err),
		)
		return fmt.Errorf("artlist_runs_repository.Record(run_id=%q): %w", rec.RunID, err)
	}
	r.log.Info("artlist_runs_repository.Record persisted",
		zap.String("run_id", rec.RunID),
		zap.String("term", rec.Term),
		zap.Int("processed", rec.ProcessedN),
		zap.Int("failed", rec.FailedN),
	)
	return nil
}

// LatestRun returns the most-recent row from artlist_runs, sorted by
// created_at DESC with id DESC as tie-breaker. SELECT reads exactly
// 5 columns (id, term, status, error_message, created_at) — the
// narrow read surface for the diagnostics endpoint; the canonical
// writer Record touches 11 columns (omit ottimistic DEFAULT columns).
//
// godlike/06 column-level locking: SELECT clause here must mirror
// the artlist_runs schema verbatim. Any schema column add/drop must
// cascade through this read shape + the adapter in
// artlist_runs_adapter.go.
//
// PR-P2-DIAGNOSTICS-REALE (July 2026): the diagnostics endpoint
// surfaces this row as DiagnosticsResponse.LatestRun + LastError +
// Status. Selection: most recent run, irrespective of status (failed
// runs are operator signal — sometimes the operator's most recent
// action was a failed attempt; surfacing the success-only tail would
// hide the real operator-visible state).
//
// Return contract:
//   - (nil, nil) when the table is empty (fresh install — operator
//     interprets nil as "no runs yet", distinct from sentinel-zero
//     LatestRunRow struct that would manifest as `run_id=""`)
func (r *ArtlistRunsRepository) LatestRun(ctx context.Context) (*LatestRunRow, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("artlist_runs_repository.LatestRun: nil receiver/db")
	}
	const stmt = `SELECT id, term, status, error_message, created_at
		FROM artlist_runs
		ORDER BY created_at DESC, id DESC
		LIMIT 1`
	row := &LatestRunRow{}
	err := r.db.QueryRowContext(ctx, stmt).Scan(
		&row.RunID,
		&row.Term,
		&row.Status,
		&row.ErrorMessage,
		&row.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			// Fresh install — no runs yet. godlike/07: surface honestly
			// (nil, nil) rather than (LatestRunRow{}, nil) which would
			// confuse operator dashboards with `run_id=""` strings.
			return nil, nil
		}
		r.log.Warn("artlist_runs_repository.LatestRun query failed",
			zap.Error(err),
		)
		return nil, fmt.Errorf("artlist_runs_repository.LatestRun: %w", err)
	}
	return row, nil
}
