// Package jobs — repository_jobs_crud.go: jobs-table CRUD + scan helpers.
//
// Pure code-motion extraction from repository.go per PR-SPLIT-JOBS-REPO-RESIDUAL
// (wave LONG-FILES-DECOMPOSITION-V2-2026-07-06#PR-SPLIT-JOBS-REPO-RESIDUAL,
// July 2026). godlike/06 SSOT: this file is the canonical SOLE owner
// of the read + write surface on the `jobs` table (excluding the
// guarded CAS transitions in transition.go + the lifecycle terminal
// transitions in lifecycle_{complete,progress,finalize,aggregation}.go).
//
// Methods in this file use the in-package `unmarshalJobFields` helper
// (defined in scan.go) to convert the raw strftime/JSON column scan
// output into typed `*job.Job` fields. The cross-file delegation
// preserves the canonical godlike/06 SSOT seam (one canonical owner
// for the scan-shape; the strftime-wrap-in-time.Parse conversion is
// owned by `timeutil.FormatRFC3339` + `repository_scanner.go`).
//
// godlike/07 minimum-blast-radius: zero new exported symbols, zero
// signature changes. Method relocation only; lookup paths preserved
// (same `jobs` package).
package jobs

import (
	"context"
	"database/sql"
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

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
	worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision, ` + parentStateTypedColumn + `,
	parent_job_id, root_job_id, client_id, idempotency_key`

// scanner is the minimum surface of *sql.Row and *sql.Rows that scanJobColumns
// needs. Defined here so we can share the same code between single-row and
// multi-row reads.
type scanner interface {
	Scan(dest ...any) error
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
		&j.ParentJobID, &j.RootJobID,
		&j.ClientID, &j.IdempotencyKey,
	); err != nil {
		return err
	}
	unmarshalJobFields(j, payloadJSON, resultJSON, leaseExpiry, createdAt, updatedAt, startedAt, completedAt, cancelledAt)
	return nil
}

// CreateInTx inserts a new job row inside the caller's transaction.
// Used by FASE 2 `GenerationSubmissionService.Submit` to commit the
// operation + job + outbox_event atomically in a single TX.
//
// godlike/06 SSOT: this method is the SOLE canonical path for
// inserting a job row inside a caller-owned *sql.Tx. The pre-FASE-2
// `Service.Enqueue` opens its own TX internally; FASE 2's
// canonical atomic-TX shape requires the TX to be external so
// the operations + outbox_events writes participate in the same
// COMMIT.
//
// godlike/07 minimum-blast-radius: CreateInTx does NOT call
// queueChanged (the canonical immediate-wake signal). The queue
// wake is deferred to the caller's post-COMMIT step (the
// FASE 2 service calls queueChanged after `tx.Commit()` returns
// nil; for the FASE 2 atomic path, the workers pick up the new
// job at the next poll tick — operators rely on the canonical
// 1-5s poll interval as the latency floor for FASE 2 Submits).
//
// Thread safety: the caller's *sql.Tx is exclusive to the
// connection that began it; no other goroutine touches the
// same row in the same TX. SQLite single-writer semantics
// apply at the connection level.
func (r *SQLiteStore) CreateInTx(ctx context.Context, tx *sql.Tx, j *job.Job) error {
	if tx == nil {
		return fmt.Errorf("jobs.CreateInTx: tx is nil (caller MUST supply the open *sql.Tx)")
	}

	query := `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
			worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision, parent_job_id, root_job_id,
			client_id, idempotency_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	payloadJSON := string(j.Payload)
	if payloadJSON == "" || payloadJSON == "null" {
		payloadJSON = "{}"
	}
	resultJSON := string(j.Result)
	if resultJSON == "" || resultJSON == "null" {
		resultJSON = "{}"
	}

	revision := j.Revision
	if revision <= 0 {
		revision = 1
	}

	_, err := tx.ExecContext(ctx, query,
		j.ID, j.Type, j.Status, j.Priority, j.Project, j.VideoName, j.ActiveKey,
		j.CorrelationID,
		payloadJSON, resultJSON, j.Progress, j.Error,
		j.RetryCount, j.MaxRetries, j.WorkerID, j.LeaseID,
		timeutil.FormatPtrRFC3339(j.LeaseExpiry),
		timeutil.FormatRFC3339(j.CreatedAt), timeutil.FormatRFC3339(j.UpdatedAt),
		timeutil.FormatPtrRFC3339(j.StartedAt), timeutil.FormatPtrRFC3339(j.CompletedAt), nil, revision, j.ParentJobID, j.RootJobID,
		j.ClientID, j.IdempotencyKey)
	if err != nil {
		return fmt.Errorf("jobs.CreateInTx: %w", err)
	}
	// Intentionally NOT calling r.queueChanged() — see doc comment
	// above (godlike/07 minimum-blast-radius: queue wake is the
	// caller's post-COMMIT concern).
	return nil
}

// Create inserts a new job row.
//
// PR-Polling / ADR-0002 §D6.5 (June 2026): wake every sleeping Worker
// so the new job is picked up immediately instead of waiting for the
// next backoff tick. Routed through queueChanged (singular broadcast
// trigger — the 3 paths that add to the queue are Create here, Retry
// in lifecycle_aggregation.go, and RequeueExpiredLeases in
// repository_claims.go).
func (r *SQLiteStore) Create(ctx context.Context, j *job.Job) error {
	query := `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
			worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at, cancelled_at, revision, parent_job_id, root_job_id,
			client_id, idempotency_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		timeutil.FormatPtrRFC3339(j.StartedAt), timeutil.FormatPtrRFC3339(j.CompletedAt), nil, revision, j.ParentJobID, j.RootJobID,
		j.ClientID, j.IdempotencyKey)
	if err != nil {
		return fmt.Errorf("failed to create job: %w", err)
	}

	r.queueChanged()

	return nil
}

// PeekQueued returns up to limit currently queued jobs without acquiring a
// lease or changing any job state. Results use the same priority/creation
// ordering as ClaimNext so preparation can inspect the likely future jobs.
// A non-positive limit is treated as an empty request.
func (r *SQLiteStore) PeekQueued(ctx context.Context, limit int) ([]job.Job, error) {
	if limit <= 0 {
		return []job.Job{}, nil
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+jobColumns+` FROM jobs
		 WHERE status = ?
		 ORDER BY priority DESC, created_at ASC
		 LIMIT ?`, job.StatusQueued, limit)
	if err != nil {
		return nil, fmt.Errorf("PeekQueued: query: %w", err)
	}
	defer rows.Close()

	out := make([]job.Job, 0)
	for rows.Next() {
		var j job.Job
		if err := scanJobColumns(rows, &j); err != nil {
			return nil, fmt.Errorf("PeekQueued: scan: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PeekQueued: rows: %w", err)
	}
	return out, nil
}

// Get returns the job row matching `id`, or (nil, nil) on sql.ErrNoRows.
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

// List returns job rows matching an optional filter (Status / Type /
// WorkerID + pagination via Limit/Offset). Empty filter returns the
// full list (paginated). Pagination is forward-only (DESC by
// created_at) — operators inspect the most recent N rows first.
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
	if filter.CorrelationID != nil && *filter.CorrelationID != "" {
		query += ` AND correlation_id = ?`
		args = append(args, *filter.CorrelationID)
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
	//
	// FASE 1 (July 2026): added WAITING_CHILDREN to the broker-status
	// tail of the query. The canonical post-fan-out parent lifecycle
	// now elevates waiting-children to a first-class broker status
	// (kernel/job.StatusWaitingChildren) so the aggregator finds
	// parents that have NOT yet been flipped to SUCCEEDED by the
	// worker. The pre-FASE-1 brokers RUNNING/FINALIZING/SUCCEEDED
	// are retained for back-compat with rows whose parent_state
	// = waiting_children was carried solely in the JSON result column.
	query := `SELECT ` + jobColumns + ` FROM jobs
WHERE type = ?
  AND status IN ('WAITING_CHILDREN','RUNNING','FINALIZING','SUCCEEDED')
  AND (parent_state_typed = 'waiting_children'
       OR (parent_state_typed = '' AND (
            json_extract(result_json,'$.parent_state') = 'waiting_children'
            OR json_extract(result_json,'$.data.parent_state') = 'waiting_children'
       )))
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

// FindActiveByKey returns the most recent non-terminal job matching
// the active_key (used for idempotency probes).
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

// FindByClientAndIdempotencyKey returns the most recent job matching the
// (client_id, idempotency_key) pair regardless of status. Used by
// Service.Enqueue to satisfy M2M idempotency after a UNIQUE-constraint
// collision on the idx_jobs_client_idempotency index (migration 251,
// PG-M2M Aug 2026). The pair is the canonical dedup key for the M2M
// surface: a remote submitter that retries a POST after a network drop
// gets the SAME job_id back instead of a duplicate.
//
// Returns (nil, nil) when either argument is empty (the caller MUST
// guard) so the pre-check path in Service.Enqueue is a no-op for
// admin/internal enqueues that do not set the M2M fields.
func (r *SQLiteStore) FindByClientAndIdempotencyKey(ctx context.Context, clientID, idempotencyKey string) (*job.Job, error) {
	if clientID == "" || idempotencyKey == "" {
		return nil, nil
	}
	query := `SELECT ` + jobColumns + ` FROM jobs WHERE client_id = ? AND idempotency_key = ? ORDER BY created_at DESC, id DESC LIMIT 1`
	j := &job.Job{}
	if err := scanJobColumns(r.db.QueryRowContext(ctx, query, clientID, idempotencyKey), j); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find job by client+idempotency_key: %w", err)
	}
	return j, nil
}
