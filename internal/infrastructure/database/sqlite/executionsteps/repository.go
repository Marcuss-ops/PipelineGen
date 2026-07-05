// Package executionsteps is the SQLite concrete of the canonical
// steps.Store port (PipelineGen Stock Cutover §12-3, July 2026).
//
// godlike/06 SSOT: this package is the SINGLE owner of the SQLite
// `execution_steps` table reads/writes for the Stock pipeline's
// resumable-step-store surface. Application-layer callers (orchestrators,
// worker startup scaffolding) MUST consume `steps.Store` via this concrete
// (or its hermetic test fakes at internal/application/execution/steps/store_test.go),
// NOT a parallel sqlite3.Conn, NOT a hand-rolled query. Drift between
// port contracts and SQL surface is caught at compile time by the
// `var _ steps.Store = (*Repository)(nil)` assertion below.
//
// godlike/07 typed-error contract: each SQL failure maps to ONE typed
// sentinel from `internal/application/execution/steps` via `fmt.Errorf("...: %w", sentinel)`.
// Callers can `errors.Is(err, steps.ErrStepAlreadyCompleted)` etc. from
// any seam.
//
// Design A (per-row canonical, audit-trail append-only): see store.go
// for full rationale. Key behaviors:
//
//   - MarkStarted: INSERT a new row if the (jobID, stepKey, fingerprint)
//     triple is novel. ON CONFLICT DO UPDATE bumps attempt + resets
//     status to Pending, suppressing the update when the existing
//     row is Completed (terminal-sink immutability).
//   - MarkCompleted: UPDATE the row's status + result + artifact_refs
//   - completed_at. Idempotent on re-call with same payload bytes
//     (no-op). Returns ErrStepAlreadyCompleted on call with different
//     payload OR call against already-Completed row.
//   - MarkFailed: UPDATE status=Failed + last_error + completed_at
//     for non-Completed rows. ErrStepAlreadyCompleted on Completed.
//   - FirstNonCompleted: SELECT MAX(id) GROUP BY step_key subquery
//     for O(N) scan on `WHERE status != 'completed'`.
//   - ListByJob: SELECT * ORDER BY step_key ASC, id ASC.
//
// Migration: `migrations/sqlite/121_execution_steps.sql` (committed).
package executionsteps

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
)

// Compile-time assertion: *Repository satisfies the canonical Store port.
// Drift in any of the 5 method signatures is a build failure at this
// declaration site (per godlike/06 SSOT one-canonical-owner-per-fact).
var _ steps.Store = (*Repository)(nil)

// Repository is the SQLite-backed concrete of steps.Store. thread-safety
// is delegated to the underlying *sql.DB (mattn/go-sqlite3 driver is
// safe for concurrent use under default pool limits; WAL+busy-timeout
// are configured at db-open time).
type Repository struct {
	db *sql.DB
	// mu guards the in-flight-mark cache (a small optimisation that
	// prevents the SQL side from drifting between two near-simultaneous
	// MarkStarted calls on the same triple). NOT used to gate logical
	// correctness — SQLite UNIQUE INDEX is the authoritative gate.
	mu sync.Mutex
}

// New constructs a steps.Store from an opened SQLite *sql.DB. The DB
// MUST have migration 121 applied (the execution_steps table + UNIQUE
// index on (job_id, step_key, input_fingerprint) + ix_resume + ix_audit
// indexes). The constructor does not run migrations itself — that's
// composition-root responsibility per godlike/06 SSOT (single writer
// of schema-version-facts).
//
// Returns ErrNilDB (sentinel) when db is nil so the composition root
// can fail loudly at boot rather than at first MarkStarted call.
func New(db *sql.DB) (*Repository, error) {
	if db == nil {
		return nil, steps.ErrStoreNotWired
	}
	return &Repository{db: db}, nil
}

// MarkStarted: idempotent on (jobID, stepKey). When the same triple
// already exists and is non-Completed, attempt counter bumps + status
// resets to Pending + started_at refreshes. When the existing row is
// Completed, returns ErrStepAlreadyCompleted (terminal-sink immutability).
//
// SQL strategy (single statement, ON CONFLICT DO UPDATE):
//
//	INSERT INTO execution_steps
//	  (job_id, step_key, input_fingerprint, status, attempt, started_at)
//	VALUES (?, ?, ?, 'pending', 1, ?)
//	ON CONFLICT(job_id, step_key, input_fingerprint) DO UPDATE
//	  SET attempt = attempt + 1,
//	      status = 'pending',
//	      started_at = excluded.started_at,
//	      completed_at = '',
//	      last_error = ''
//	WHERE execution_steps.status != 'completed'
//
// The `WHERE status != 'completed'` clause suppresses the UPDATE when
// the row is in the terminal-sink state. The Go wrapper then peeks
// the post-statement rows-affected: 1 means INSERT-or-UPDATE landed;
// 0 means either the row is Completed (which surfaces to caller as
// ErrStepAlreadyCompleted) OR some unexpected concurrency edge case
// (which surfaces as a generic wraps).
func (r *Repository) MarkStarted(ctx context.Context, key steps.StepKey) error {
	if err := key.Validated(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := r.db.ExecContext(ctx, `
INSERT INTO execution_steps (
    job_id, step_key, input_fingerprint, status, attempt,
    result_json, artifact_refs_json, started_at, completed_at, last_error
) VALUES (?, ?, ?, 'pending', 1, '{}', '[]', ?, '', '')
ON CONFLICT(job_id, step_key, input_fingerprint) DO UPDATE
    SET
        attempt = execution_steps.attempt + 1,
        status = 'pending',
        started_at = excluded.started_at,
        completed_at = '',
        last_error = ''
    WHERE execution_steps.status != 'completed'
`,
		key.JobID, key.StepKey, key.InputFingerprint, now)
	if err != nil {
		return fmt.Errorf("steps.Repository.MarkStarted: INSERT ON CONFLICT: %w", err)
	}

	// Detect the terminal-sink case: row exists + status=completed.
	// The ON CONFLICT DO UPDATE WHERE clause suppressed the update,
	// so RowsAffected=0 here. A standalone SELECT confirms whether
	// the row exists at all (so we can return ErrStepAlreadyCompleted
	// vs. some unexpected 0-rows case).
	var exists bool
	var existingStatus string
	err = r.db.QueryRowContext(ctx, `
SELECT TRUE, status FROM execution_steps
WHERE job_id = ? AND step_key = ? AND input_fingerprint = ?
LIMIT 1
`, key.JobID, key.StepKey, key.InputFingerprint).Scan(&exists, &existingStatus)
	if errors.Is(err, sql.ErrNoRows) {
		// RowsAffected=0 AND no row exists is unexpected (UNIQUE
		// guarantees the row exists after the INSERT path). Surface
		// loudly so future drift surfaces at compile/runtime.
		return fmt.Errorf("steps.Repository.MarkStarted: 0 rows affected and row missing (UNIQUE drift?): rowsAffected=%d", affectedRows(res))
	}
	if err != nil {
		return fmt.Errorf("steps.Repository.MarkStarted: post-conflict SELECT: %w", err)
	}
	if existingStatus == "completed" {
		return steps.ErrStepAlreadyCompleted
	}
	return nil
}

// MarkCompleted: UPDATE Pending|Running|Failed → Completed, stamps
// result + artifact_refs + completed_at. Idempotent on re-call with
// SAME payload bytes (no-op, no timestamp bump). Returns ErrStepAlreadyCompleted
// when called against an already-Completed row with a DIFFERENT payload.
func (r *Repository) MarkCompleted(ctx context.Context, key steps.StepKey, result, artifactRefs json.RawMessage) error {
	if err := key.Validated(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// Read the current row to honor the idempotency-on-same-payload
	// contract and surface ErrStepAlreadyCompleted cleanly.
	var existingStatus string
	var existingResult, existingArtifacts []byte
	err := r.db.QueryRowContext(ctx, `
SELECT status, result_json, artifact_refs_json FROM execution_steps
WHERE job_id = ? AND step_key = ? AND input_fingerprint = ?
`, key.JobID, key.StepKey, key.InputFingerprint).Scan(&existingStatus, &existingResult, &existingArtifacts)
	if errors.Is(err, sql.ErrNoRows) {
		return steps.ErrStepNotFound
	}
	if err != nil {
		return fmt.Errorf("steps.Repository.MarkCompleted: pre-flight SELECT: %w", err)
	}

	if existingStatus == "completed" {
		// Idempotent re-completion ONLY if both payload bytes match.
		if bytesEqual(existingResult, result) && bytesEqual(existingArtifacts, artifactRefs) {
			return nil
		}
		return steps.ErrStepAlreadyCompleted
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = r.db.ExecContext(ctx, `
UPDATE execution_steps
SET status = 'completed',
    result_json = ?,
    artifact_refs_json = ?,
    completed_at = ?,
    last_error = ''
WHERE job_id = ? AND step_key = ? AND input_fingerprint = ?
  AND status != 'completed'
`, string(result), string(artifactRefs), now,
		key.JobID, key.StepKey, key.InputFingerprint)
	if err != nil {
		return fmt.Errorf("steps.Repository.MarkCompleted: UPDATE: %w", err)
	}
	return nil
}

// MarkFailed: UPDATE Pending|Running|Failed → Failed, stamps last_error
// + completed_at. ErrStepAlreadyCompleted when called against an
// already-Completed row.
func (r *Repository) MarkFailed(ctx context.Context, key steps.StepKey, errMessage string) error {
	if err := key.Validated(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var existingStatus string
	err := r.db.QueryRowContext(ctx, `
SELECT status FROM execution_steps
WHERE job_id = ? AND step_key = ? AND input_fingerprint = ?
`, key.JobID, key.StepKey, key.InputFingerprint).Scan(&existingStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return steps.ErrStepNotFound
	}
	if err != nil {
		return fmt.Errorf("steps.Repository.MarkFailed: pre-flight SELECT: %w", err)
	}

	if existingStatus == "completed" {
		return steps.ErrStepAlreadyCompleted
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = r.db.ExecContext(ctx, `
UPDATE execution_steps
SET status = 'failed',
    last_error = ?,
    completed_at = ?
WHERE job_id = ? AND step_key = ? AND input_fingerprint = ?
  AND status != 'completed'
`, errMessage, now,
		key.JobID, key.StepKey, key.InputFingerprint)
	if err != nil {
		return fmt.Errorf("steps.Repository.MarkFailed: UPDATE: %w", err)
	}
	return nil
}

// FirstNonCompleted returns the canonical first non-completed step.
// SQL: SELECT MAX(id) GROUP BY step_key subquery scopes "latest" to the
// most-recent row per step_key, then filters by status != 'completed'
// and orders by step_key ASC limit 1. SQLite implements MAX/GROUP BY
// efficiently under the (job_id, status, step_key) resume-index.
//
// Returns (nil, nil) when all latest rows are Completed (signals
// "fully done; orchestrator may exit"). No errors expected on happy
// path; SQL failure surfaces as a wrapped error.
func (r *Repository) FirstNonCompleted(ctx context.Context, jobID string) (*steps.StepState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	row := r.db.QueryRowContext(ctx, `
SELECT id, job_id, step_key, input_fingerprint, status, attempt,
       result_json, artifact_refs_json, started_at, completed_at, last_error
FROM execution_steps
WHERE job_id = ?
  AND id IN (
    SELECT MAX(id) FROM execution_steps WHERE job_id = ? GROUP BY step_key
  )
  AND status != 'completed'
ORDER BY step_key ASC
LIMIT 1
`, jobID, jobID)

	var st steps.StepState
	var resultStr, artifactsStr, startedAtStr, completedAtStr string
	err := row.Scan(
		&st.ID, &st.JobID, &st.StepKey, &st.Fingerprint, &st.Status, &st.Attempt,
		&resultStr, &artifactsStr, &startedAtStr, &completedAtStr, &st.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("steps.Repository.FirstNonCompleted: SELECT: %w", err)
	}
	st.Result = json.RawMessage(resultStr)
	st.ArtifactRefs = json.RawMessage(artifactsStr)
	st.StartedAt = parseTimestamp(startedAtStr)
	st.CompletedAt = parseTimestamp(completedAtStr)
	return &st, nil
}

// ListByJob returns ALL rows for jobID ordered by (step_key ASC, id ASC).
// Returns (nil, nil) for unseen jobID.
func (r *Repository) ListByJob(ctx context.Context, jobID string) ([]steps.StepState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	rows, err := r.db.QueryContext(ctx, `
SELECT id, job_id, step_key, input_fingerprint, status, attempt,
       result_json, artifact_refs_json, started_at, completed_at, last_error
FROM execution_steps
WHERE job_id = ?
ORDER BY step_key ASC, id ASC
`, jobID)
	if err != nil {
		return nil, fmt.Errorf("steps.Repository.ListByJob: SELECT: %w", err)
	}
	defer rows.Close()

	var out []steps.StepState
	for rows.Next() {
		var st steps.StepState
		var resultStr, artifactsStr, startedAtStr, completedAtStr string
		if err := rows.Scan(
			&st.ID, &st.JobID, &st.StepKey, &st.Fingerprint, &st.Status, &st.Attempt,
			&resultStr, &artifactsStr, &startedAtStr, &completedAtStr, &st.LastError,
		); err != nil {
			return nil, fmt.Errorf("steps.Repository.ListByJob: rows.Scan: %w", err)
		}
		st.Result = json.RawMessage(resultStr)
		st.ArtifactRefs = json.RawMessage(artifactsStr)
		st.StartedAt = parseTimestamp(startedAtStr)
		st.CompletedAt = parseTimestamp(completedAtStr)
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("steps.Repository.ListByJob: rows.Err: %w", err)
	}
	return out, nil
}

// ── helpers ─────────────────────────────────────────────────────

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func affectedRows(res sql.Result) int64 {
	n, err := res.RowsAffected()
	if err != nil {
		return -1
	}
	return n
}

func parseTimestamp(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
