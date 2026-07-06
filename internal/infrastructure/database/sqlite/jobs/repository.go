// Package jobs provides the canonical job repository with atomic CAS operations
// for the 8-state job lifecycle.
//
// States: queued → leased → running → finalizing → succeeded / retry_wait / failed / cancelled
//
// Implements the canonical job.Store contract from internal/domain/job directly,
// without conversion through legacy model types (PR4: job.Job SSOT).
//
// queue_notifier.go holds the in-process wake-up broadcast primitive
// (canonical per PR-Polling / ADR-0002 §D6.5, June 2026).
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// SQLiteStore — canonical job.Store implementation.
//
// Concurrency model (post-PR-Polling design, ADR-0003 §Implementation-
// status #6 supersession by PR-Queue-Split-claimMu cleanup, June 2026):
// the previous `claimMu` application-level mutex on ClaimNext is REMOVED.
// SQLite's WAL write-serialisation + the `AND revision = ?` CAS gate in
// repository_claims.go::Start() are sufficient for ClaimNext atomicity.
// Two workers racing the same row will both SELECT the same `id` at the
// LIMIT 1 read; the loser's UPDATE matches a stale `revision` and
// returns rows-affected=0 → ErrTransitionConflict. No application-level
// mutex is needed; SQLite is the synchronisation point.
type SQLiteStore struct {
	db                *sql.DB
	log               *zap.Logger
	notifier          *notifier
	producesArtifacts map[string]bool // job types that MUST use CompleteWithArtifacts
}

// jobColumns is the canonical list of column names read by Get, List and
// FindActiveByKey and written by Create. Kept in one place so adding a
// new tracked column is a one-line change.
// jobColumns is the canonical SELECT projection for the jobs table.
// MUST-FIX #3 (PR-P1.2-SQL-DUAL-WRITE godlike/06 SSOT): the
// parent_state_typed column is appended via string concatenation
// with the package-private parentStateTypedColumn constant so any
// future rename to the typed column name (or a typo at one site)
// surfaces as a SQL error at query time, not a silent mismatch
// between 3 independent string literals.
//
// godlike/06 SSOT (one canonical owner per fact): the typed
// column name lives ONLY in
// voiceover.JobParentStateColumn (canonical cross-package, public)
// + sqlite/jobs.parentStateTypedColumn (SQL mirror, package-private).
// The cross-package drift test was DROPPED per godlike/07 minimum-
// blast-radius — see repository_lifecycle_dualwrite_test.go header
// for the explicit rationale.
const jobColumns = `id, type, status, priority, project, video_name, active_key,
	correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
	worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision, ` + parentStateTypedColumn

func NewSQLiteStore(db *sql.DB, log *zap.Logger) *SQLiteStore {
	return &SQLiteStore{db: db, log: log, notifier: newNotifier()}
}

// SetProducesArtifacts configures which job types produce artifacts and
// must use CompleteWithArtifacts instead of the legacy Complete path.
// Passing nil clears the gate (allows all types through Complete).
func (r *SQLiteStore) SetProducesArtifacts(types map[string]bool) {
	r.producesArtifacts = types
}

// ── In-process queue-notifier port (PR-Polling / ADR-0002 §D6.5) ────────────

// Subscribe returns a shared channel that wakes on every QueueChanged
// (Enqueue / Retry / RequeueExpiredLeases) notification. The returned
// channel is the LIVE channel at the call moment; the next Broadcast
// closes it AND replaces it with a fresh open channel (a subsequent
// Subscribe call returns the new channel, not the closed one).
//
// Implementation note: lifecycle is fully owned by *SQLiteStore (the
// notifier is constructed in NewSQLiteStore). Workers / runners
// subscribe per-loop via this method.
func (r *SQLiteStore) Subscribe() <-chan struct{} {
	return r.notifier.Subscribe()
}

// Broadcast closes the current notifier channel and replaces it with
// a fresh open channel. All in-flight subscribers unblock; new
// subscribers join the fresh channel.
//
// Trigger surface: this method is called from Create, Retry, and
// RequeueExpiredLeases — the only three canonical paths that ADD
// jobs to the queue. ClaimNext does NOT call Broadcast per ADR
// §D6.5 ("no fake availability": raw SQL operators do not get wakes).
//
// In-process scope: the broadcast is single-process only. A future
// postgres adapter will need a separate LISTEN/NOTIFY adapter (out of
// scope for PR-Polling / §D6.5 single-node).
func (r *SQLiteStore) Broadcast() {
	r.notifier.Broadcast()
}

// queueChanged is a private helper that centralises the
// "after a write added a job to the queue, wake every sleep­ing
// Worker" pattern. It is the canonical call site for Broadcast on
// the SQLiteStore write paths; triggering code MUST go through
// this helper rather than calling r.Broadcast() directly so the
// trigger set stays in one place (the linter cannot enforce this,
// but the doc-comment on Create / Retry / RequeueExpiredLeases pins
// the canonical call).
func (r *SQLiteStore) queueChanged() {
	r.Broadcast()
}

// DB returns the underlying *sql.DB for direct query access.
//
// Deprecated: prefer the typed methods on *SQLiteStore (Get, List, Create,
// Transition, ClaimNext, etc.) over raw *sql.DB access. Direct access
// bypasses the optimistic-lock guards, lease fencing, and queue-notifier
// broadcasts that make the store concurrency-safe. This method is kept for
// test fixtures and admin one-shot commands only. Scheduled for removal in
// 2026-Q4; new production callers will fail code review.
func (r *SQLiteStore) DB() *sql.DB { return r.db }

// Compile-time check: SQLiteStore satisfies the canonical job.Store contract.
var _ job.Store = (*SQLiteStore)(nil)

// Compile-time check: SQLiteStore satisfies the canonical job.JobBroker
// port (PR-B, Wave 22, June 2026). The same assertion will be added at
// the top of any future PostgreSQL adapter's repository file — the
// port + this assertion is the seam that lets internal/application/**
// depend on a portable interface instead of *SQLiteStore directly.
//
// Rationale for the embedding-not-alias choice (and the call sites a
// future PR-postgres author must touch): see ADR-0002 §D2 audit notes
// (`architecture/decisions/0002-p2-p3-roadmap.md`).
var _ job.JobBroker = (*SQLiteStore)(nil)

func (r *SQLiteStore) Create(ctx context.Context, j *job.Job) error {
	query := `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
			worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	payloadJSON := string(j.Payload)
	if payloadJSON == "" || payloadJSON == "null" {
		payloadJSON = "{}"
	}
	resultJSON := string(j.Result)
	if resultJSON == "" || resultJSON == "null" {
		resultJSON = "{}"
	}

	// Revision is per-row monotonic counter; on creation it is 1 (the
	// first revision before any transition fired).
	revision := j.Revision
	if revision <= 0 {
		revision = 1
	}

	_, err := r.db.ExecContext(ctx, query,
		j.ID, j.Type, j.Status, j.Priority, j.Project, j.VideoName, j.ActiveKey,
		j.CorrelationID,
		payloadJSON, resultJSON, j.Progress, j.Error,
		j.RetryCount, j.MaxRetries, j.WorkerID, j.LeaseID,
		timeutil.FormatPtrRFC3339(j.LeaseExpiry),
		timeutil.FormatRFC3339(j.CreatedAt), timeutil.FormatRFC3339(j.UpdatedAt),
		timeutil.FormatPtrRFC3339(j.StartedAt), timeutil.FormatPtrRFC3339(j.CompletedAt), nil, revision)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	// PR-Polling / ADR-0002 §D6.5 (June 2026): wake every sleeping
	// Worker so the new job is picked up immediately instead of
	// waiting for the next backoff tick. Routed through queueChanged
	// (singular broadcast trigger — the 3 paths that add to the
	// queue are Create here, Retry in repository_lifecycle.go, and
	// RequeueExpiredLeases in repository_claims.go).
	r.queueChanged()

	return nil
}

// scanJobColumns reads the canonical job column list into job, then
// unmarshals the JSON payload/result and parses the time columns. Used by
// Get, List, and FindActiveByKey.
func scanJobColumns(s scanner, j *job.Job) error {
	var payloadJSON, resultJSON string
	var leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt *string
	if err := s.Scan(
		&j.ID, &j.Type, &j.Status, &j.Priority, &j.Project, &j.VideoName, &j.ActiveKey,
		&j.CorrelationID,
		&payloadJSON, &resultJSON, &j.Progress, &j.Error, &j.RetryCount, &j.MaxRetries,
		&j.WorkerID, &j.LeaseID, &leaseExpiry, &createdAt, &updatedAt,
		&startedAt, &completedAt, &cancelledAt, &j.Revision,
		&j.ParentStateTyped,
	); err != nil {
		return err
	}
	unmarshalJobFields(j, payloadJSON, resultJSON, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt)
	return nil
}

// scanner is the minimum surface of *sql.Row and *sql.Rows that scanJobColumns
// needs. Defined here so we can share the same code between single-row and
// multi-row reads.
type scanner interface {
	Scan(dest ...any) error
}

func (r *SQLiteStore) Get(ctx context.Context, id string) (*job.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE id = ?`
	j := &job.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, id), j); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get job: %w", err)
	}
	return j, nil
}

func (r *SQLiteStore) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE 1=1`
	args := []any{}

	if filter.Status != nil {
		query += ` AND status = ?`
		args = append(args, *filter.Status)
	}
	if filter.Type != nil {
		query += ` AND type = ?`
		args = append(args, *filter.Type)
	}
	if filter.WorkerID != "" {
		query += ` AND worker_id = ?`
		args = append(args, filter.WorkerID)
	}

	query += ` ORDER BY created_at DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		query += ` OFFSET ?`
		args = append(args, filter.Offset)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	defer rows.Close()

	var out []job.Job
	for rows.Next() {
		j := &job.Job{}
		if err := scanJobColumns(rows, j); err != nil {
			return nil, fmt.Errorf("failed to scan job: %w", err)
		}
		out = append(out, *j)
	}

	return out, nil
}

// ListAwaitingAggregation returns parent jobs of the given type whose
// result_json carries parent_state = 'waiting_children' AND whose broker
// status is RUNNING, FINALIZING, or SUCCEEDED. Uses the composite index
// idx_jobs_type_status (migration 127) to narrow the scan before applying
// json_extract.
//
// Commit 3 P0 #4 (July 2026): parentType parameter replaces the hardcoded
// job.TypeVoiceoverGenerate so both script.generate and voiceover.generate
// aggregators reuse the same query. Only waiting_children is queried —
// partial_success is terminal (prevents infinite re-aggregation P0 #7).
//
// The query returns up to `limit` rows ordered by created_at DESC so the
// aggregator processes the most recent parents first. When limit <= 0,
// defaults to 100.
// ListAwaitingAggregation returns voiceover.generate parents awaiting
// aggregation (parent_state=waiting_children, broker status IN
// RUNNING/FINALIZING/SUCCEEDED).
//
// PR-P1.2-SQL-DUAL-WRITE (July 2026): the WHERE clause uses the
// AUTHORITATIVE typed parent_state_typed column as the PRIMARY
// match (added by migration 129, written atomically by
// repository_lifecycle.go::FinalizeAggregateParent). The JSON
// resultMap["parent_state"] is the SECONDARY fallback used ONLY
// when the typed column is empty (pre-P1.2 rows + concurrent
// writes in flight — the SQL UPDATE writes BOTH surfaces in the
// same transaction, but a race window exists between migration
// 129 shipping and the BACKFILL CLI running).
//
// Strict precedence: a row with parent_state_typed=succeeded
// (terminal) is NEVER matched even if the JSON key still says
// waiting_children. The typed column is authoritative per the
// BACKFILL contract; the JSON is a back-compat shim. A test
// pins this strictness (TestListAwaitingAggregation_ExcludesNonMatching).
//
// godlike/06 SSOT: the typed column name is the SQL mirror constant
// `parentStateTypedColumn` (package-private); the cross-package
// canonical is voiceover.JobParentStateColumn. Both must agree (the
// godlike/06 SSOT drift test was DROPPED per godlike/07 minimum-
// blast-radius — see repository_lifecycle_dualwrite_test.go header).
//
// godlike/07 minimum-blast-radius: the typed-primary + JSON-
// fallback WHERE clause is intentional. A simpler `parent_state_typed
// = 'X' OR json_extract(...) = 'X'` (OR-either) would over-match
// during the BACKFILL window (a row with typed=succeeded + JSON=
// waiting_children would be incorrectly included). The strict
// precedence prevents that.
func (r *SQLiteStore) ListAwaitingAggregation(ctx context.Context, parentType string, limit int) ([]job.Job, error) {
	if limit <= 0 {
		limit = 100
	}
	// PR-P1.2-SQL-DUAL-WRITE: typed column is PRIMARY; JSON is
	// SECONDARY fallback ONLY when typed is empty.
	query := `SELECT ` + jobColumns + ` FROM jobs
WHERE type = ?
  AND status IN ('RUNNING','FINALIZING','SUCCEEDED')
  AND (parent_state_typed = 'waiting_children'
       OR (parent_state_typed = '' AND json_extract(result_json,'$.parent_state') = 'waiting_children'))
ORDER BY created_at DESC
LIMIT ?`
	rows, err := r.db.QueryContext(ctx, query, parentType, limit)
	if err != nil {
		return nil, fmt.Errorf("ListAwaitingAggregation: %w", err)
	}
	defer rows.Close()

	var out []job.Job
	for rows.Next() {
		j := &job.Job{}
		if err := scanJobColumns(rows, j); err != nil {
			return nil, fmt.Errorf("ListAwaitingAggregation: scan: %w", err)
		}
		out = append(out, *j)
	}
	return out, nil
}

// GetStats + RefreshMetrics — extracted to repository_stats.go (PR-REPO-SPLIT, July 2026).

func (r *SQLiteStore) FindActiveByKey(ctx context.Context, activeKey string) (*job.Job, error) {
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE active_key = ? AND active_key != '' AND status IN ('QUEUED', 'LEASED', 'RUNNING', 'FINALIZING') ORDER BY started_at DESC LIMIT 1`
	j := &job.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, activeKey), j); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find job by active key: %w", err)
	}
	return j, nil
}

// FindByTypeAndCorrelation returns the most recent job matching the
// (type, correlation_id) pair regardless of status. Used by Service.Enqueue
// to satisfy idempotency after a UNIQUE-constraint collision on the
// idx_jobs_type_correlation index.
func (r *SQLiteStore) FindByTypeAndCorrelation(ctx context.Context, jobType string, correlationID string) (*job.Job, error) {
	if correlationID == "" {
		return nil, nil
	}
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE type = ? AND correlation_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`
	j := &job.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, jobType, correlationID), j); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find job by type+correlation: %w", err)
	}
	return j, nil
}

// ListEvents returns all events for a given job.
func (r *SQLiteStore) ListEvents(ctx context.Context, jobID string) ([]job.Event, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, job_id, type, message, data_json,
			strftime('%Y-%m-%dT%H:%M:%fZ', created_at) AS created_at
		FROM job_events
		WHERE job_id = ?
		ORDER BY strftime('%Y-%m-%dT%H:%M:%fZ', created_at) ASC`, jobID)
	if err != nil {
		return nil, fmt.Errorf("listEvents: %w", err)
	}
	defer rows.Close()

	var events []job.Event
	for rows.Next() {
		var evt job.Event
		var dataJSON string
		createdAt := &evt.CreatedAt
		if err := rows.Scan(&evt.ID, &evt.JobID, &evt.Type, &evt.Message, &dataJSON, &rfc3339TimeScanner{t: createdAt}); err != nil {
			return nil, fmt.Errorf("listEvents: scan: %w", err)
		}
		if len(dataJSON) > 0 {
			json.Unmarshal([]byte(dataJSON), &evt.Data)
		}
		events = append(events, evt)
	}
	return events, nil
}
