// Package qdrantprojection — checkpoint + DLQ store (PR 8).
//
// Persists the per-job state qdrantprojection_checkpoints and
// qdrantprojection_dlq rows defined by migration 105. Single API
// surface (CheckpointStore) so calling code never touches *sql.DB
// directly to update job state. All operations are idempotent: a
// repeat Commit() overwrites the prior cursor; a repeat DLQ() inserts
// a NEW row (auditing each failure rather than upsert'ing).
//
// Fail-closed contract:
//   - Commit() writes to qdrantprojection_checkpoints ONCE per batch
//     boundary. Mid-batch failures do NOT advance the cursor — the
//     next call after the failure re-reads from the SAME cursor.
//     Resume logic works because crash-after-batch = at-least-once
//     delivery: the crashed batch may have been acknowledged by
//     Qdrant (idempotent), in-flight at crashtime (re-issued), or
//     not-yet-issued (re-issued from scratch). across all three
//     cases Qdrant's idempotent upsert-by-id semantics absorb the
//     duplicate.
//   - The DLQ carries HARD failures — validation/format/decode —
//     that the caller must NOT silently retry. A re-run after a
//     DLQ resolution is the canonical operator workflow.

package qdrantprojection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ── Store ─────────────────────────────────────────────────────────────

// CheckpointStore is the canonical persist layer for the
// qdrantprojection_checkpoints and qdrantprojection_dlq tables.
//
// Construction via NewCheckpointStore; the constructor wires the *sql.DB
// handle (single connection pool, no per-method handle passing).
type CheckpointStore struct {
	db *sql.DB
}

// NewCheckpointStore returns the store bound to db. Panics on db==nil.
func NewCheckpointStore(db *sql.DB) *CheckpointStore {
	if db == nil {
		panic("qdrantprojection.NewCheckpointStore: db must not be nil")
	}
	return &CheckpointStore{db: db}
}

// ── Status enum (mirrors the SQL CHECK constraint) ──────────────────

// Status is the lifecycle status of a reindex job. Mirrors the SQL
// CHECK constraint `('running','succeeded','failed','abandoned')` from
// migration 105. Adding values requires a SQL ALTER + a Go-side
// extension — out of scope for PR 8.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusAbandoned Status = "abandoned"
)

// ── State container ────────────────────────────────────────────────────

// Checkpoint is the canonical in-memory state for a reindex job.
// Serialises to the qdrantprojection_checkpoints row.
type Checkpoint struct {
	JobID            string
	TargetCollection string
	LastIndexedID    string
	IndexedCount     int64
	ErrorCount       int64
	SkippedCount     int64
	StartedAt        time.Time
	FinishedAt       time.Time // zero = not finished
	LastBatchAt      time.Time // zero = no batch yet
	Status           Status
	LastError        string
}

// ── Write operations ──────────────────────────────────────────────────

// Open creates a fresh-running checkpoint row. Idempotent semantics:
// if a row with the supplied JobID already exists, Open updates its
// status to running (so resume after crash mirrors the "fresh"
// surface). Caller supplies StartedAt explicitly (not time.Now()) so
// resume flows preserve the original timestamp — operators reading
// `started_at` see "since job start" not "since last resume attempt".
func (s *CheckpointStore) Open(ctx context.Context, c Checkpoint) error {
	if c.JobID == "" {
		return errors.New("qdrantprojection.CheckpointStore.Open: Checkpoint.JobID must not be empty")
	}
	if c.TargetCollection == "" {
		return errors.New("qdrantprojection.CheckpointStore.Open: Checkpoint.TargetCollection must not be empty")
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO qdrantprojection_checkpoints
    (job_id, target_collection, last_indexed_id, indexed_count, error_count,
     skipped_count, started_at, finished_at, last_batch_at, status, last_error, updated_at)
VALUES (?, ?, '', 0, 0, 0, ?, '', '', 'running', '', datetime('now'))
ON CONFLICT(job_id) DO UPDATE SET
    status = 'running',
    updated_at = datetime('now')
`,
		c.JobID,
		c.TargetCollection,
		formatRFC3339(c.StartedAt),
	)
	if err != nil {
		return fmt.Errorf("qdrantprojection.CheckpointStore.Open: %w", err)
	}
	return nil
}

// Commit advances the cursor + counters after a Qdrant-upserter batch
// is acknowledged. Idempotent: calling Commit with the same cursor
// twice is a no-op. Operator-visible invariant: LastIndexedID MUST
// equal the highest id returned by BatchReader.Next() for the batch
// just acknowledged.
//
// ── Atomicity trade-off (PR 8 IMPORTANT note from code review) ────────
// Commit() persists a checkpoint AFTER the Qdrant upsert succeeds on
// the wire (this package only owns the SQLite side; Qdrant has no
// SQLite-bound transaction). When this method returns nil the
// upsert+checkpoint pair is durable. When this method returns non-nil:
//  1. The Qdrant upsert for THIS batch has ALREADY succeeded.
//  2. The checkpoint.cursor is left at the PREVIOUS batch's
//     last_indexed_id.
//  3. The next resume call therefore re-upserts the SAME batch.
//
// Qdrant upsert is idempotent by point ID, so a re-upsert is safe on
// the index side; the side-effect to monitor is that
// `indexed_count`/`error_count` on the checkpoint row will recount
// the prior batch's outcomes on resume. Operators relying on the
// counters as run totals must re-derive them post-resume from the
// `qdrantprojection_dlq` join as well as the existing row, or treat
// checkpoint counters as a convenience approximation rather than the
// authoritative accounting.
//
// The chosen trade-off (commit-after-upsert, no two-phase commit) was
// selected because Qdrant's lack of a transactional token API forces
// the choice between:
//
//	(a) commit-after-up — fast, idempotent, counters may drift, OR
//	(b) commit-before-up — durable cursor, but upsert failures leave
//	    silent gaps that look like "we didn't run this batch" rather
//	    than "we ran it and Qdrant rejected it".
//
// (a) is preferred because the explicit DLQ + Prometheus outcome
// counters give operators a strict superset of (b)'s visibility.
// PR 9 follow-ups may wrap (a) in a SQLite SAVEPOINT so the cursor
// advance + counter bump commit atomically; until then, this doc is
// the canonical statement of the trade-off.
// ──────────────────────────────────────────────────────────────────────
func (s *CheckpointStore) Commit(ctx context.Context, c Checkpoint) error {
	if c.JobID == "" {
		return errors.New("qdrantprojection.CheckpointStore.Commit: Checkpoint.JobID must not be empty")
	}
	res, err := s.db.ExecContext(ctx, `
UPDATE qdrantprojection_checkpoints
SET last_indexed_id = ?,
    indexed_count   = ?,
    error_count     = ?,
    skipped_count   = ?,
    last_batch_at   = ?,
    last_error      = ?,
    status          = 'running',
    updated_at      = datetime('now')
WHERE job_id = ?
`,
		c.LastIndexedID,
		c.IndexedCount,
		c.ErrorCount,
		c.SkippedCount,
		formatRFC3339(c.LastBatchAt),
		c.LastError,
		c.JobID,
	)
	if err != nil {
		return fmt.Errorf("qdrantprojection.CheckpointStore.Commit: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("qdrantprojection.CheckpointStore.Commit: no checkpoint row with job_id=%q (call Open first)", c.JobID)
	}
	return nil
}

// Finish transitions the job to a terminal status. Caller supplies
// FinishedAt (not Now) so deterministic test fixtures work.
func (s *CheckpointStore) Finish(ctx context.Context, jobID string, status Status, finishedAt time.Time) error {
	if jobID == "" {
		return errors.New("qdrantprojection.CheckpointStore.Finish: jobID must not be empty")
	}
	switch status {
	case StatusSucceeded, StatusFailed, StatusAbandoned:
		// terminal — proceed
	default:
		return fmt.Errorf("qdrantprojection.CheckpointStore.Finish: only terminal statuses allowed, got %q", status)
	}
	_, err := s.db.ExecContext(ctx, `
UPDATE qdrantprojection_checkpoints
SET status = ?, finished_at = ?, updated_at = datetime('now')
WHERE job_id = ?
`,
		string(status),
		formatRFC3339(finishedAt),
		jobID,
	)
	if err != nil {
		return fmt.Errorf("qdrantprojection.CheckpointStore.Finish: %w", err)
	}
	return nil
}

// ── Read operation ────────────────────────────────────────────────────

// Get returns the canonical Checkpoint row for jobID. Returns (nil,
// nil) if no row exists (the job was never Open'd).
func (s *CheckpointStore) Get(ctx context.Context, jobID string) (*Checkpoint, error) {
	if jobID == "" {
		return nil, errors.New("qdrantprojection.CheckpointStore.Get: jobID must not be empty")
	}
	var (
		c           Checkpoint
		startedAt   string
		finishedAt  sql.NullString
		lastBatchAt sql.NullString
		status      string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT job_id, target_collection, last_indexed_id, indexed_count, error_count,
       skipped_count, started_at, finished_at, last_batch_at, status, last_error
FROM qdrantprojection_checkpoints
WHERE job_id = ?
`, jobID).Scan(
		&c.JobID, &c.TargetCollection, &c.LastIndexedID, &c.IndexedCount,
		&c.ErrorCount, &c.SkippedCount, &startedAt, &finishedAt, &lastBatchAt,
		&status, &c.LastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("qdrantprojection.CheckpointStore.Get: %w", err)
	}
	c.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
	if finishedAt.Valid && finishedAt.String != "" {
		c.FinishedAt, _ = time.Parse(time.RFC3339Nano, finishedAt.String)
	}
	if lastBatchAt.Valid && lastBatchAt.String != "" {
		c.LastBatchAt, _ = time.Parse(time.RFC3339Nano, lastBatchAt.String)
	}
	c.Status = Status(status)
	return &c, nil
}

// ── DLQ (per-failure persistence) ─────────────────────────────────────

// DLQReason enumerates the bounded failure categories mirrored in
// the SQL CHECK constraint from migration 105.
type DLQReason string

const (
	DLQReasonEmbeddingObsolete  DLQReason = "embedding_obsolete"
	DLQReasonContentHashMissing DLQReason = "content_hash_missing"
	DLQReasonDimensionMismatch  DLQReason = "dimension_mismatch"
	DLQReasonPayloadInvalid     DLQReason = "payload_invalid"
	DLQReasonOther              DLQReason = "other"
)

// DLQEntry is a single failure record. Inserted via DLQ() and visible
// to operators via the admin CLI.
type DLQEntry struct {
	JobID      string
	AssetID    string
	Reason     DLQReason
	LastError  string
	ObservedAt time.Time
}

// DLQ appends a failure row. Idempotent semantics are deliberately
// NOT upsert: every observed failure is recorded so post-mortem
// analysis can SEE retry counts (multiple observed_at rows for the
// same (job_id, asset_id, reason) triple means the v2 reindex tried
// the document more than once).
func (s *CheckpointStore) DLQ(ctx context.Context, e DLQEntry) error {
	if e.JobID == "" {
		return errors.New("qdrantprojection.CheckpointStore.DLQ: DLQEntry.JobID must not be empty")
	}
	reason := string(e.Reason)
	if reason == "" {
		reason = string(DLQReasonOther)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO qdrantprojection_dlq
    (job_id, asset_id, reason_category, last_error, observed_at, resolved_at, resolved_by)
VALUES (?, ?, ?, ?, ?, '', '')
`,
		e.JobID,
		e.AssetID,
		reason,
		e.LastError,
		formatRFC3339(e.ObservedAt),
	)
	if err != nil {
		return fmt.Errorf("qdrantprojection.CheckpointStore.DLQ: %w", err)
	}
	return nil
}

// ListDLQ returns the most recent unresolved DLQ rows for a job.
// limit caps the result count; pass 0 for the canonical 100-row default.
func (s *CheckpointStore) ListDLQ(ctx context.Context, jobID string, limit int) ([]DLQEntry, error) {
	if jobID == "" {
		return nil, errors.New("qdrantprojection.CheckpointStore.ListDLQ: jobID must not be empty")
	}
	if limit <= 0 {
		limit = 100 // canonical admin-CLI default; matches CLI flag's default.
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT job_id, asset_id, reason_category, last_error, observed_at
FROM qdrantprojection_dlq
WHERE job_id = ? AND resolved_at = ''
ORDER BY observed_at DESC
LIMIT ?
`, jobID, limit)
	if err != nil {
		return nil, fmt.Errorf("qdrantprojection.CheckpointStore.ListDLQ: %w", err)
	}
	defer rows.Close()

	var out []DLQEntry
	for rows.Next() {
		var (
			e        DLQEntry
			observed string
		)
		if err := rows.Scan(&e.JobID, &e.AssetID, &e.Reason, &e.LastError, &observed); err != nil {
			return nil, fmt.Errorf("qdrantprojection.CheckpointStore.ListDLQ: scan: %w", err)
		}
		e.ObservedAt, _ = time.Parse(time.RFC3339Nano, observed)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("qdrantprojection.CheckpointStore.ListDLQ: rows iter: %w", err)
	}
	return out, nil
}

// ── Helpers ───────────────────────────────────────────────────────────

// formatRFC3339 returns the canonical SQL-friendly RFC3339Nano string
// when t is non-zero, otherwise the empty string (so SQL columns
// with `DEFAULT ”` accept the value without an EXPLICIT NULL flag).
// Operator convention: zero time == "not set" == empty column.
func formatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
