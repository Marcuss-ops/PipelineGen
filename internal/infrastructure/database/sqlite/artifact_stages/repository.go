// Package artifactstages — internal/infrastructure/database/sqlite/artifact_stages/repository.go
//
// FASE 3 (Push 3.1a, July 2026): canonical concrete for the
// `artifact.Repository` port (internal/domain/artifact/stages.go).
// Backs the FASE 3 Spina Dorsale publication saga with the
// `artifact_stages` SQLite table (migration 147).
//
// godlike/06 SSOT: this file is the SINGLE canonical writer of the
// per-publication record. Application-layer code (staging.Store in
// Push 3.1b, publisher worker pool, finalizer) consumes the domain
// Repository port; the concrete is built at the composition root
// (internal/app/build_bundles_*.go) and receives *sql.DB.
//
// godlike/07 fail-closed:
//   - Pre-TX validation: State.IsValid() + Requirement.IsValid() +
//     Size > 0 + Hash non-empty; out-of-set values are rejected with
//     the typed sentinels (ErrInvalidArtifactStageState /
//     ErrInvalidRequirement / ErrArtifactStageEmpty /
//     ErrArtifactStageHashMismatch) WITHOUT touching the DB.
//   - Fenced CAS: every Mark* method gates the UPDATE on
//     `state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')` so a stale
//     leaseholder cannot silently re-patch a terminal row. The
//     disambiguation logic distinguishes "row absent" (NotFound)
//     from "row already terminal" (TerminalStateRejection) via a
//     post-UPDATE SELECT probe (single round-trip when the
//     UPDATE succeeds; two when it doesn't).
//   - All timestamps are UTC + RFC3339Nano per PipelineGen SSOT.
//
// Implementation notes (Push 3.1a follow-up): the 8 methods are
// enough for the publisher worker pool + finalizer to round-trip
// the FASE 3 saga. A future Push may add
// `(*Repository).MarkAttemptFailed(ctx, id, lastError)` that bumps
// attempt_count + last_error in a single UPDATE (the per-publisher
// path uses 2 calls today; combining them is a perf + atomicity
// win but out of scope for 3.1a).
package artifactstages

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	artifact "github.com/Marcuss-ops/PipelineGen/internal/domain/artifact"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Compile-time assertion: *Repository satisfies the domain port.
var _ artifact.Repository = (*Repository)(nil)

// Repository is the SQLite-backed implementation of
// artifact.Repository. Holds a *sql.DB; all 8 methods are safe for
// concurrent use (database/sql is the standard connection pool).
type Repository struct {
	db *sql.DB
	// nowFn is overridable for tests (default time.Now). UTC is
	// enforced via the helper to match PipelineGen's SSOT.
	nowFn func() time.Time
}

// NewRepository constructs the canonical artifact_stages
// repository. Caller MUST supply a non-nil *sql.DB; composition
// root fails-fast on nil at construction (godlike/07).
func NewRepository(db *sql.DB) *Repository {
	return &Repository{
		db:    db,
		nowFn: func() time.Time { return time.Now().UTC() },
	}
}

// ── Pre-TX validation helpers ────────────────────────────────────────────

// validateForWrite enforces the Insert contract. Returns the
// FIRST violation's typed sentinel so callers can errors.Is-probe
// the specific failure class.
func validateForWrite(stage *artifact.ArtifactStage) error {
	if stage == nil {
		return fmt.Errorf("artifact_stages.Insert: nil stage")
	}
	if stage.ID == "" {
		return fmt.Errorf("%w", artifact.ErrInvalidArtifactStageID)
	}
	if stage.JobID == "" {
		return fmt.Errorf("%w", artifact.ErrInvalidJobID)
	}
	if !stage.State.IsValid() {
		return fmt.Errorf("%w: %q", artifact.ErrInvalidArtifactStageState, stage.State)
	}
	// godlike/07 state-machine enforcement: Insert is the entry
	// point of the saga; only StateStaged is allowed. PUBLISHED /
	// SUCCEEDED / FAILED_PERMANENT can only be reached via the
	// fenced Mark* transitions. A caller that tries to insert a
	// non-STAGED row bypasses the state machine; rejected with
	// ErrInvalidArtifactStageState (same sentinel as non-canonical
	// values, so log-greppers get one consistent failure class).
	if stage.State != artifact.StateStaged {
		return fmt.Errorf("%w: Insert requires state=STAGED (canonical initial state of the saga), got %q", artifact.ErrInvalidArtifactStageState, stage.State)
	}
	if !stage.Requirement.IsValid() {
		return fmt.Errorf("%w: %q", artifact.ErrInvalidRequirement, stage.Requirement)
	}
	if stage.Size <= 0 {
		return fmt.Errorf("%w: size=%d", artifact.ErrArtifactStageEmpty, stage.Size)
	}
	if stage.Hash == "" {
		return fmt.Errorf("%w: hash is empty", artifact.ErrArtifactStageHashMismatch)
	}
	return nil
}

// now returns the repository's monotonic time source, UTC-normalised.
func (r *Repository) now() time.Time {
	return r.nowFn().UTC()
}

// ── column list (single source of truth for INSERT + SELECT) ──────────────

const selectColumns = `id, job_id, local_path, hash, size, mime, requirement, destination, state, attempt_count, last_error, published_location, published_at, created_at, updated_at`

// scanRow materialises one row into a typed ArtifactStage. The
// time columns (created_at, updated_at, published_at) are stored
// as TEXT in the canonical schema (migration 147) and the
// mattn/go-sqlite3 driver's parseTime=true only auto-converts
// TIMESTAMP/DATETIME columns. We mirror the production pattern at
// internal/infrastructure/database/sqlite/jobs/finalize_attempt.go
// (which uses timeutil.FormatRFC3339 for the same columns): read
// TEXT, parse to time.Time with time.RFC3339Nano. published_at
// is NULL-able so we read into a *string + post-parse to *time.Time.
func scanRow(row interface{ Scan(...interface{}) error }) (artifact.ArtifactStage, error) {
	var (
		s            artifact.ArtifactStage
		publishedAtS *string
		publishedLoc string
		requirement  string
		state        string
		createdAtS   string
		updatedAtS   string
	)
	if err := row.Scan(
		&s.ID, &s.JobID, &s.LocalPath, &s.Hash, &s.Size, &s.Mime,
		&requirement, &s.Destination, &state, &s.AttemptCount,
		&s.LastError, &publishedLoc, &publishedAtS, &createdAtS, &updatedAtS,
	); err != nil {
		return s, err
	}
	createdAt, err := parseRFC3339Nano(createdAtS, "created_at")
	if err != nil {
		return s, err
	}
	updatedAt, err := parseRFC3339Nano(updatedAtS, "updated_at")
	if err != nil {
		return s, err
	}
	var publishedAt *time.Time
	if publishedAtS != nil && *publishedAtS != "" {
		pt, err := parseRFC3339Nano(*publishedAtS, "published_at")
		if err != nil {
			return s, err
		}
		publishedAt = &pt
	}
	s.Requirement = artifact.Requirement(requirement)
	s.State = artifact.ArtifactStageState(state)
	s.PublishedLocation = publishedLoc
	s.PublishedAt = publishedAt
	s.CreatedAt = createdAt
	s.UpdatedAt = updatedAt
	return s, nil
}

// parseRFC3339Nano parses a non-empty string as time.RFC3339Nano
// (the canonical PipelineGen wire format — see
// timeutil.FormatRFC3339Nano, used by every write site in this
// repository for nano-precision round-trip). Returns a typed
// sentinel error so the caller can errors.Is-probe the parse
// failure. Empty input returns the zero time + ErrTimestampEmpty
// so callers can distinguish a missing timestamp from a
// malformed one.
//
// godlike/07 fail-closed: only the canonical RFC3339Nano format
// is accepted. A row that holds a non-canonical format is a
// schema-drift signal — the upstream writer (a direct-DB INSERT
// bypassing the repository) is the bug, and the row MUST be
// remediated by the operator, NOT silently re-parsed by a
// permissive fallback.
func parseRFC3339Nano(s, columnName string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("%w: column=%s", ErrTimestampEmpty, columnName)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: column=%s value=%q parse_err=%v", ErrTimestampParse, columnName, s, err)
	}
	return t.UTC(), nil
}

// ── Insert ──────────────────────────────────────────────────────────────

// Insert appends a new ArtifactStage row. State is forced to STAGED
// (the initial state of the saga); callers MAY supply a non-STAGED
// value but the repository will surface ErrInvalidArtifactStageState
// unless the state is in the canonical 4-value set.
func (r *Repository) Insert(ctx context.Context, stage *artifact.ArtifactStage) error {
	if err := validateForWrite(stage); err != nil {
		return err
	}
	now := r.now()
	if stage.CreatedAt.IsZero() {
		stage.CreatedAt = now
	}
	stage.UpdatedAt = now

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO artifact_stages (`+selectColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		stage.ID, stage.JobID, stage.LocalPath, stage.Hash, stage.Size, stage.Mime,
		string(stage.Requirement), stage.Destination, string(stage.State),
		stage.AttemptCount, stage.LastError, stage.PublishedLocation,
		timeutil.FormatPtrRFC3339Nano(stage.PublishedAt),
		timeutil.FormatRFC3339Nano(stage.CreatedAt),
		timeutil.FormatRFC3339Nano(stage.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("artifact_stages.Insert (id=%s): %w", stage.ID, err)
	}
	return nil
}

// ── GetByID ──────────────────────────────────────────────────────────────

func (r *Repository) GetByID(ctx context.Context, id string) (*artifact.ArtifactStage, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM artifact_stages WHERE id = ?`, id)
	stage, err := scanRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, artifact.WrapArtifactStageNotFound(id)
		}
		return nil, fmt.Errorf("artifact_stages.GetByID (id=%s): %w", id, err)
	}
	return &stage, nil
}

// ── ListByJob ───────────────────────────────────────────────────────────

func (r *Repository) ListByJob(ctx context.Context, jobID string) ([]artifact.ArtifactStage, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM artifact_stages WHERE job_id = ? ORDER BY created_at ASC`,
		jobID)
	if err != nil {
		return nil, fmt.Errorf("artifact_stages.ListByJob (job_id=%s): %w", jobID, err)
	}
	defer rows.Close()
	var out []artifact.ArtifactStage
	for rows.Next() {
		stage, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("artifact_stages.ListByJob: scan: %w", scanErr)
		}
		out = append(out, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("artifact_stages.ListByJob: rows: %w", err)
	}
	return out, nil
}

// ── ListByState ─────────────────────────────────────────────────────────

func (r *Repository) ListByState(ctx context.Context, state artifact.ArtifactStageState, limit int) ([]artifact.ArtifactStage, error) {
	if !state.IsValid() {
		return nil, fmt.Errorf("%w: %q", artifact.ErrInvalidArtifactStageState, state)
	}
	if limit <= 0 {
		limit = 100 // safe default
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM artifact_stages WHERE state = ? ORDER BY created_at ASC LIMIT ?`,
		string(state), limit)
	if err != nil {
		return nil, fmt.Errorf("artifact_stages.ListByState (state=%s): %w", state, err)
	}
	defer rows.Close()
	var out []artifact.ArtifactStage
	for rows.Next() {
		stage, scanErr := scanRow(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("artifact_stages.ListByState: scan: %w", scanErr)
		}
		out = append(out, stage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("artifact_stages.ListByState: rows: %w", err)
	}
	return out, nil
}

// ── MarkPublished ───────────────────────────────────────────────────────

func (r *Repository) MarkPublished(ctx context.Context, id, publishedLocation string, publishedAt time.Time) error {
	now := r.now()
	publishedAt = publishedAt.UTC()
	res, err := r.db.ExecContext(ctx,
		`UPDATE artifact_stages SET state = 'PUBLISHED', published_location = ?, published_at = ?, updated_at = ? WHERE id = ? AND state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')`,
		publishedLocation, timeutil.FormatRFC3339Nano(publishedAt), timeutil.FormatRFC3339Nano(now), id)
	if err != nil {
		return fmt.Errorf("artifact_stages.MarkPublished (id=%s): %w", id, err)
	}
	return r.checkFencedCAS(ctx, res, id, "MarkPublished")
}

// ── MarkSucceeded ───────────────────────────────────────────────────────

func (r *Repository) MarkSucceeded(ctx context.Context, id string) error {
	now := r.now()
	res, err := r.db.ExecContext(ctx,
		`UPDATE artifact_stages SET state = 'SUCCEEDED', updated_at = ? WHERE id = ? AND state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')`,
		timeutil.FormatRFC3339Nano(now), id)
	if err != nil {
		return fmt.Errorf("artifact_stages.MarkSucceeded (id=%s): %w", id, err)
	}
	return r.checkFencedCAS(ctx, res, id, "MarkSucceeded")
}

// ── MarkFailedPermanent ─────────────────────────────────────────────────

func (r *Repository) MarkFailedPermanent(ctx context.Context, id, lastError string) error {
	now := r.now()
	res, err := r.db.ExecContext(ctx,
		`UPDATE artifact_stages SET state = 'FAILED_PERMANENT', last_error = ?, updated_at = ? WHERE id = ? AND state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')`,
		lastError, timeutil.FormatRFC3339Nano(now), id)
	if err != nil {
		return fmt.Errorf("artifact_stages.MarkFailedPermanent (id=%s): %w", id, err)
	}
	return r.checkFencedCAS(ctx, res, id, "MarkFailedPermanent")
}

// ── IncrementAttemptCount ───────────────────────────────────────────────

func (r *Repository) IncrementAttemptCount(ctx context.Context, id string) error {
	now := r.now()
	res, err := r.db.ExecContext(ctx,
		`UPDATE artifact_stages SET attempt_count = attempt_count + 1, updated_at = ? WHERE id = ? AND state NOT IN ('SUCCEEDED', 'FAILED_PERMANENT')`,
		timeutil.FormatRFC3339Nano(now), id)
	if err != nil {
		return fmt.Errorf("artifact_stages.IncrementAttemptCount (id=%s): %w", id, err)
	}
	return r.checkFencedCAS(ctx, res, id, "IncrementAttemptCount")
}

// ── Fenced CAS disambiguation ───────────────────────────────────────────

// checkFencedCAS converts a 0-rowsAffected UPDATE into a typed
// error. The disambiguation probe (SELECT state FROM artifact_stages
// WHERE id = ?) costs one extra round-trip on the failure path;
// the success path stays single-roundtrip (godlike/07: never
// silently accept a fence-mismatch as success).
//
// The ctx is threaded through so a cancelled request aborts the
// disambiguation probe too (otherwise a slow probe would leak past
// the ctx deadline).
func (r *Repository) checkFencedCAS(ctx context.Context, res sql.Result, id, op string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("artifact_stages.%s: rows-affected (id=%s): %w", op, id, err)
	}
	if affected > 0 {
		return nil
	}
	// Disambiguate: row absent vs row already-terminal.
	var state string
	scanErr := r.db.QueryRowContext(ctx, `SELECT state FROM artifact_stages WHERE id = ?`, id).Scan(&state)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return artifact.WrapArtifactStageNotFound(id)
	}
	if scanErr != nil {
		return fmt.Errorf("artifact_stages.%s: disambiguate probe (id=%s): %w", op, id, scanErr)
	}
	return fmt.Errorf("%w: id=%s current_state=%s op=%s", artifact.ErrTerminalStateRejection, id, state, op)
}

// ── JSON helper (placeholder for future typed PublishedLocation) ─────────

// MarshalPublishedLocation is a helper for callers who want to
// serialise a typed PublishedLocation. The repository stores the
// canonical wire format (compact JSON); the field is TEXT in
// SQLite (per migration 147). Callers MAY pass any string-format
// payload; the repository does not interpret the bytes (forward-
// compat with shape evolution).
func MarshalPublishedLocation(loc any) (string, error) {
	b, err := json.Marshal(loc)
	if err != nil {
		return "", fmt.Errorf("artifact_stages.MarshalPublishedLocation: %w", err)
	}
	return string(b), nil
}

// ErrTimestampEmpty is the canonical repository-level sentinel
// returned by parseRFC3339Nano when the column value is the empty
// string. godlike/07 fail-closed: an empty TEXT timestamp is a
// schema-violation signal (every canonical column has DEFAULT
// datetime('now')) and is rejected as a parse failure rather
// than silently coerced to the zero time.
var ErrTimestampEmpty = errors.New("artifact_stages: timestamp column is empty (canonical format is RFC3339Nano)")

// ErrTimestampParse is the canonical repository-level sentinel
// returned by parseRFC3339Nano when the column value is non-empty
// but cannot be parsed in any known format. godlike/07 fail-closed.
var ErrTimestampParse = errors.New("artifact_stages: timestamp column value cannot be parsed as RFC3339Nano")
