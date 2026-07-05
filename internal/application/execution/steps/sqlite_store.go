// Package steps — sqlite_store.go (Step 10 / Stock Cutover C1/4, July 2026).
//
// SQLiteStore is the persistent implementation of the canonical
// steps.Store port (see store.go in this package). It backs every
// method to the canonical execution_steps table (migration 121 +
// 122 lease_until extension) so the orchestrator's per-step
// checkpointing survives process restarts.
//
// godlike/06 SSOT (one owner per fact):
//   - This file is the SECOND concrete impl of steps.Store. The
//     first (InMemoryStore in in_memory_store.go) is the canonical
//     default for tests + dev modes. Production composition roots
//     wire this SQLiteStore so step state survives restarts.
//   - The orchestrator iterates dispatchSteps in canonical
//     pipeline order; on retry-after-crash, MarkStarted returns
//     steps.ErrStepAlreadyCompleted for any step whose latest row
//     is Completed — the orchestrator patches this to `continue`
//     (per Stock Cutover C2/4). Lease_until is OPTIONAL and
//     informational: a non-empty lease_until < now AND status
//     IN ('pending','running') row is the canonical "stalled run"
//     signal surfaced by ix_execution_steps_leased_stale.
//
// godlike/07 fail-closed typed-error contract:
//   - All sentinel errors are re-exported from package-level
//     vars; callers errors.Is(err, steps.ErrStepAlreadyCompleted)
//     etc. — no new error wrappers, no fmt.Errorf opaque strings.
//
// Driver lock (AGENTS.md Step 11A banned switching): mattn/go-sqlite3.
// Production composition roots enable WAL + busy_timeout via
// storage.OpenSQLiteDB; test fixtures use file-based :memory:
// equivalents via storage.OpenSQLiteDBInMemory.
//
// Concurrency: SQLite's UNIQUE INDEX uniq_execution_steps_dedup
// guarantees single-writer-per-(job_id, step_key, fingerprint)
// serialization when WAL mode + busy_timeout are in effect
// (production defaults: PRAGMA journal_mode=WAL + busy_timeout=5000).
// No application-level mutex is needed — the underlying SQL
// constraint model is sufficient (proven by 053_job_lifecycle_atomic.sql
// precedent on the jobs table).
package steps

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	// mattn/go-sqlite3 — canonical driver (AGENTS.md bans switching).
	_ "github.com/mattn/go-sqlite3"
)

// DefaultLeaseTTL is the canonical lease_until offset the
// SQLiteStore stamps on MarkStarted. A step whose lease_until is
// in the future is "active"; a step whose lease_until expired
// without MarkCompleted / MarkFailed is "stalled".
//
// The 1-hour default reflects the orchestrator's typical
// per-stage budget (Stock Cutover §12-5 step ladder: each stage
// runs seconds-to-minutes; 1 hour covers all uncomitted-stage
// work). A pipeline whose stock.publish stage genuinely runs
// > 1 hour would re-enter via a future MarkStarted call (which
// bumps attempt + refreshes lease_until), so the TTL is a
// crash-detection signal, not a hard timeout.
const DefaultLeaseTTL = 1 * time.Hour

// Compile-time assertion: *sqliteStore satisfies the canonical Store port.
var _ Store = (*sqliteStore)(nil)

// sqliteStore is the SQLite-backed Store implementation. The
// underlying *sql.DB is the production wiring target (media.db.sqlite
// per AGENTS.md canonical single-DB invariant). For test fixtures
// a per-test in-memory or temp-file DB is supplied via
// NewSQLiteStoreWithDB.
type sqliteStore struct {
	db *sql.DB
}

// NewSQLiteStore returns the canonical SQLite-backed Store
// implementation bound to db. Panics on db==nil so composition-
// root wiring fails loud (no silent nil passthrough — matches the
// qdrantprojection.NewCheckpointStore precedent at line 47).
func NewSQLiteStore(db *sql.DB) Store {
	if db == nil {
		panic("steps.NewSQLiteStore: db must not be nil (composition root must wire storage.OpenSQLiteDB before constructing the steps.Store)")
	}
	return &sqliteStore{db: db}
}

// NewSQLiteStoreWithDB returns a Store bound to db WITHOUT the
// nil panic — used by test fixtures that supply a fresh open
// DB and want explicit wiring without the NewSQLiteStore panic.
// The non-panicking variant is exported so the panic remains a
// composition-root contract (the in-line panic in NewSQLiteStore).
func NewSQLiteStoreWithDB(db *sql.DB) Store {
	return &sqliteStore{db: db}
}

// NowFn is the canonical clock injection point. Production wiring
// uses time.Now (the zero-value default); tests inject a fixed clock
// so the lease_until stamps are deterministic. Captured at
// construction, NOT per-call, so a single store is consistent
// across all calls.
type NowFn func() time.Time

// NowFnDefault is the canonical clock — UTC normalized so RFC3339
// strings sort lexicographically as chronology.
func NowFnDefault() time.Time { return time.Now().UTC() }

// MarkStarted records that work for (jobID, stepKey) is beginning
// with inputFingerprint. Idempotent on re-call with the same triple
// (bumps attempt, refreshes lease_until). Returns ErrStepAlreadyCompleted
// if the latest row is already Completed (terminal-immutability).
//
// godlike/07 race-safety: a single UPSERT statement (INSERT ... ON
// CONFLICT DO UPDATE) resolves the SELECT-then-INSERT race window
// where two concurrent goroutines on the same triple both observe
// "no prior row" and one loses on UNIQUE CONSTRAINT. The SQLite
// UNIQUE INDEX uniq_execution_steps_dedup serializes concurrent
// UPSERTs at the SQL level so the first-call + idempotent re-call
// paths coalesce into one statement.
//
// The "latest row" semantics follow Design A (per-step versioning):
// a retry with the same fingerprint bumps the existing row's
// attempt counter via ON CONFLICT; a retry with a DIFFERENT
// fingerprint INSERTs a new row (acts as a fingerprint-version
// audit log for downstream auditing).
//
// The CASE expressions in the ON CONFLICT clause preserve
// terminal-immutability: if execution_steps.status = 'completed'
// the existing values are kept unchanged (attempt NOT bumped,
// started_at / lease_until NOT refreshed). The RETURNING clause
// reports the post-state so the caller can detect a Completed
// prior row and surface ErrStepAlreadyCompleted.
//
// lease_until is stamped with DefaultLeaseTTL offset on every
// non-completed MarkStarted. A stalled row (lease_until < now AND
// status NOT IN ('completed','failed')) is the canonical crash-
// detection signal surfaced by ix_execution_steps_leased_stale.
func (s *sqliteStore) MarkStarted(ctx context.Context, key StepKey) error {
	if err := key.Validated(); err != nil {
		return err
	}

	now := NowFnDefault()
	leaseUntil := now.Add(DefaultLeaseTTL).Format(time.RFC3339)
	startedAt := now.Format(time.RFC3339)

	var statusAfter string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO execution_steps (
			job_id, step_key, input_fingerprint,
			status, attempt,
			started_at, lease_until
		) VALUES (?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT (job_id, step_key, input_fingerprint) DO UPDATE SET
		    attempt = CASE WHEN execution_steps.status = 'completed'
		                   THEN execution_steps.attempt
		                   ELSE execution_steps.attempt + 1 END,
		    status = CASE WHEN execution_steps.status = 'completed'
		                  THEN execution_steps.status
		                  ELSE excluded.status END,
		    started_at = CASE WHEN execution_steps.status = 'completed'
		                      THEN execution_steps.started_at
		                      ELSE excluded.started_at END,
		    lease_until = CASE WHEN execution_steps.status = 'completed'
		                       THEN execution_steps.lease_until
		                       ELSE excluded.lease_until END
		RETURNING status
	`, key.JobID, key.StepKey, key.InputFingerprint,
		string(StatusPending), startedAt, leaseUntil).Scan(&statusAfter)
	if err != nil {
		return fmt.Errorf("steps.SqliteStore.MarkStarted: UPSERT: %w", err)
	}
	if statusAfter == string(StatusCompleted) {
		// ON CONFLICT clause preserved the prior Completed row's
		// state (terminal-immutability); RETURNING reports it.
		// Surface as the typed sentinel so caller errors.Is works.
		return ErrStepAlreadyCompleted
	}
	return nil
}

// MarkCompleted transitions the row to Completed and stamps
// result + artifact_refs. Idempotent on re-call with the same
// result (byte-equal); returns ErrStepAlreadyCompleted if the row
// is already Completed with a DIFFERENT result. Returns
// ErrStepNotFound if no row exists for the triple (pre-MarkStarted
// completion is a programming error).
//
// Per Design A, lease_until is CLEARED on terminal transition:
// completed rows are "done" and the heartbeat is no longer relevant.
func (s *sqliteStore) MarkCompleted(ctx context.Context, key StepKey, result, artifactRefs json.RawMessage) error {
	if err := key.Validated(); err != nil {
		return err
	}

	// Read prior row to honor byte-equality idempotency + ErrStepAlreadyCompleted contract.
	row, err := s.scanRow(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStepNotFound
	}
	if err != nil {
		return fmt.Errorf("steps.SqliteStore.MarkCompleted: read latest row: %w", err)
	}

	if row.Status == StatusCompleted {
		if bytesEqual(row.Result, result) && bytesEqual(row.ArtifactRefs, artifactRefs) {
			return nil // idempotent re-completion with same shape
		}
		return ErrStepAlreadyCompleted
	}

	// Transition to Completed + clear lease_until.
	completedAt := NowFnDefault().Format(time.RFC3339)
	res, execErr := s.db.ExecContext(ctx, `
		UPDATE execution_steps
		SET status = ?,
		    completed_at = ?,
		    result_json = ?,
		    artifact_refs_json = ?,
		    lease_until = ?
		WHERE id = ? AND status != ?
	`, string(StatusCompleted), completedAt, jsonOrEmpty(result), jsonOrEmpty(artifactRefs),
		"", row.ID, string(StatusCompleted))
	if execErr != nil {
		return fmt.Errorf("steps.SqliteStore.MarkCompleted: UPDATE: %w", execErr)
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		// Defensive: row transitioned to Completed via a concurrent
		// goroutine between scan and update — surface as the typed
		// already-completed sentinel so callers can errors.Is it.
		return ErrStepAlreadyCompleted
	}
	return nil
}

// MarkFailed transitions the row to Failed and stamps LastError.
// If the row is Completed, returns ErrStepAlreadyCompleted. If no
// prior row exists, INSERTs a Failed row at attempt=1 — this
// matches the canonical contract where a fatal-error path before
// MarkStarted still produces an audit-trail row.
//
// lease_until is CLEARED on terminal transition (Completed|Failed).
func (s *sqliteStore) MarkFailed(ctx context.Context, key StepKey, errMessage string) error {
	if err := key.Validated(); err != nil {
		return err
	}

	now := NowFnDefault()
	nowStr := now.Format(time.RFC3339)

	// Read prior row.
	row, err := s.scanRow(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		// No prior row — INSERT a Failed row at attempt=1.
		_, insertErr := s.db.ExecContext(ctx, `
			INSERT INTO execution_steps (
				job_id, step_key, input_fingerprint,
				status, attempt,
				started_at, completed_at, last_error, lease_until
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, key.JobID, key.StepKey, key.InputFingerprint,
			string(StatusFailed), 1, nowStr, nowStr, errMessage, "")
		return insertErr
	}
	if err != nil {
		return fmt.Errorf("steps.SqliteStore.MarkFailed: read latest row: %w", err)
	}

	if row.Status == StatusCompleted {
		return ErrStepAlreadyCompleted
	}

	// Transition to Failed + clear lease_until.
	_, updateErr := s.db.ExecContext(ctx, `
		UPDATE execution_steps
		SET status = ?,
		    completed_at = ?,
		    last_error = ?,
		    lease_until = ?
		WHERE id = ? AND status != ?
	`, string(StatusFailed), nowStr, errMessage, "", row.ID, string(StatusFailed))
	return updateErr
}

// FirstNonCompleted returns the canonical first non-completed step
// (lexically smallest stepKey whose latest row is NOT Completed).
// Returns (nil, nil) when no rows exist OR all latest rows are
// Completed for the given jobID.
//
// Implementation: a single SELECT joining the canonical
// execution_steps table with a per-step MAX(id) subquery (so
// retries with the same key but different fingerprints stay
// versioned in the audit log; we read only the latest). The
// Resumed selector scans stepKey ASC and filters status !=
// 'completed', taking LIMIT 1.
func (s *sqliteStore) FirstNonCompleted(ctx context.Context, jobID string) (*StepState, error) {
	if jobID == "" {
		return nil, fmt.Errorf("%w: missing JobID", ErrInvalidStepKey)
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT t1.id, t1.job_id, t1.step_key, t1.input_fingerprint,
		       t1.status, t1.attempt,
		       t1.result_json, t1.artifact_refs_json,
		       t1.started_at, t1.completed_at, t1.last_error
		FROM execution_steps t1
		INNER JOIN (
			SELECT step_key, MAX(id) AS max_id
			FROM execution_steps
			WHERE job_id = ?
			GROUP BY step_key
		) latest ON t1.id = latest.max_id
		WHERE t1.status != ?
		ORDER BY t1.step_key ASC
		LIMIT 1
	`, jobID, string(StatusCompleted))

	state, err := scanStepState(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("steps.SqliteStore.FirstNonCompleted: %w", err)
	}
	return state, nil
}

// ListByJob returns ALL rows for jobID, ordered by stepKey ASC then
// id ASC, so callers can reconstruct the fingerprint-version audit
// log (not just the latest per stepKey). Returns (nil, nil) for
// unseen jobID.
func (s *sqliteStore) ListByJob(ctx context.Context, jobID string) ([]StepState, error) {
	if jobID == "" {
		return nil, fmt.Errorf("%w: missing JobID", ErrInvalidStepKey)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, job_id, step_key, input_fingerprint,
		       status, attempt,
		       result_json, artifact_refs_json,
		       started_at, completed_at, last_error
		FROM execution_steps
		WHERE job_id = ?
		ORDER BY step_key ASC, id ASC
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("steps.SqliteStore.ListByJob: %w", err)
	}
	defer rows.Close()

	var out []StepState
	for rows.Next() {
		state, err := scanStepState(rows)
		if err != nil {
			return nil, fmt.Errorf("steps.SqliteStore.ListByJob: scan: %w", err)
		}
		out = append(out, *state)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("steps.SqliteStore.ListByJob: rows.Err: %w", err)
	}
	if out == nil {
		return nil, nil // never return a nil slice — convention from InMemoryStore
	}
	return out, nil
}

// ────────────────────────────────────────────────────────────────────
// Private row helpers
// ────────────────────────────────────────────────────────────────────

// scanRow returns the latest StepState matching the (job_id,
// step_key, input_fingerprint) triple. Returns sql.ErrNoRows when
// no row exists for callers that distinguish "no row" from "error"
// (MarkCompleted + MarkFailed use it as the ErrStepNotFound trigger).
//
// findLatestRow WAS the prerequisite lookup for the legacy
// SELECT-then-INSERT-or-UPDATE MarkStarted path; it was removed
// in the Step 10 C1/4 UPSERT rewrite since the atomic
// `INSERT ... ON CONFLICT DO UPDATE ... RETURNING status` collapses
// the no-prior / prior-non-Completed / prior-Completed branches
// into one statement. The SQL-level UNIQUE INDEX serializes
// concurrent UPSERTs so no application-level SELECT-first guard is
// needed (godlike/07 race-safe).
func (s *sqliteStore) scanRow(ctx context.Context, key StepKey) (*StepState, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, job_id, step_key, input_fingerprint,
		       status, attempt,
		       result_json, artifact_refs_json,
		       started_at, completed_at, last_error
		FROM execution_steps
		WHERE job_id = ? AND step_key = ? AND input_fingerprint = ?
		ORDER BY id DESC LIMIT 1
	`, key.JobID, key.StepKey, key.InputFingerprint)
	return scanStepState(row)
}

// rowScanner abstracts over *sql.Row and *sql.Rows so scanStepState
// can serve both single-row reads (MarkCompleted/MarkFailed/scanRow)
// and iterated reads (ListByJob).
type rowScanner interface {
	Scan(dest ...any) error
}

// scanStepState decodes a row into a StepState. The column order
// MUST match the SELECT projections in scanRow + FirstNonCompleted +
// ListByJob — adding a column requires a paired SELECT-update.
func scanStepState(row rowScanner) (*StepState, error) {
	var (
		state          StepState
		statusStr      string
		resultRaw      sql.NullString
		artifactRaw    sql.NullString
		startedAtRaw   sql.NullString
		completedAtRaw sql.NullString
		lastErrorRaw   sql.NullString
		idRaw          int64
		attemptRaw     int
	)
	if err := row.Scan(
		&idRaw,
		&state.JobID,
		&state.StepKey,
		&state.Fingerprint,
		&statusStr,
		&attemptRaw,
		&resultRaw,
		&artifactRaw,
		&startedAtRaw,
		&completedAtRaw,
		&lastErrorRaw,
	); err != nil {
		return nil, err
	}
	state.ID = idRaw
	state.Status = StepStatus(statusStr)
	state.Attempt = attemptRaw
	if resultRaw.Valid {
		state.Result = json.RawMessage(resultRaw.String)
	}
	if artifactRaw.Valid {
		state.ArtifactRefs = json.RawMessage(artifactRaw.String)
	}
	if startedAtRaw.Valid && startedAtRaw.String != "" {
		if t, err := time.Parse(time.RFC3339, startedAtRaw.String); err == nil {
			state.StartedAt = t
		}
	}
	if completedAtRaw.Valid && completedAtRaw.String != "" {
		if t, err := time.Parse(time.RFC3339, completedAtRaw.String); err == nil {
			state.CompletedAt = t
		}
	}
	if lastErrorRaw.Valid {
		state.LastError = lastErrorRaw.String
	}
	return &state, nil
}

// jsonOrEmpty converts a nil/empty json.RawMessage into an empty
// string so the bytesEqual byte-equality check passes symmetrically
// (bytes.Equal([]byte(""), nil) == true) and the MarkCompleted
// idempotency contract holds for "byte-equal re-completion with
// nil refs" callers.
//
// Why NOT default to "{}" or "[]": the migration 121 column default
// is '{}' for result_json and '[]' for artifact_refs_json, but the
// UPSERT/UPDATE paths ALWAYS provide explicit values — so we
// standardize on the empty string as the canonical "no value"
// sentinel (symmetric with nil json.RawMessage).
func jsonOrEmpty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return string(raw)
}
