// Package finalizer — job_completion_writer.go (PR-GODOBJ-5-FINALIZER split)
//
// Hosts the terminal-SUCCEEDED writer surface for the JobFinalizer.
// Two declarations move from the pre-split monolithic job_finalizer.go
// into this file:
//
//   - markSucceeded — step 9 of the orchestrator's transactional
//     pipeline. WRITES:
//
//     (a) the wrapped result_json envelope
//     ({data, completion_fingerprint}) that handleIdempotentCompletion
//     reads back for fingerprint comparison;
//     (b) the atomic UPDATE jobs SET status='SUCCEEDED' (with
//     worker_id / lease_id fence — same pattern as the
//     existing SQLiteStore.Complete in repository_lifecycle.go);
//     (c) the job_completed job_events row;
//     (d) the optional_artifact_report job_events sidecar row
//     (P1.2, July 2026) — when len(optionalReport) > 0.
//
//   - randomHex — pure SHA-256-derived id-helper used to mint job_events
//     event-id rows. n bytes truncated → 2n hex chars. n MUST be ≤ 32.
//
// godlike/06 SSOT: this file is the canonical owner of "what does
// the SUCCEEDED flip + audit sidecar look like for job X?".
// Callers MUST route through markSucceeded — never set status =
// SUCCEEDED outside the JobFinalizer (godlike/06 SSOT).
package finalize

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── Mark succeeded ──────────────────────────────────────────────────

// markSucceeded writes the result manifest (wrapped with completion
// fingerprint for artifact-aware idempotency), inserts a job event for
// `job_completed`, persists the optional-artifact audit sidecar (P1.2)
// as a separate `optional_artifact_report` job_events row, and
// updates the job status to SUCCEEDED — all inside the transaction.
//
// godlike/07 typed-error contract: the optional-artifact sidecar row
// lands atomically with the job_completed flip so a partial commit
// cannot corrupt the operator's view of which optional artifacts
// shipped (P1.2 invariant: success == sidecar persisted).
func (f *Finalizer) markSucceeded(
	ctx context.Context,
	tx *sql.Tx,
	req *finalization.FinalizationRequest,
	optionalReport []finalization.OptionalArtifactRecord,
) error {
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	// Compute completion fingerprint: result + sorted artifact hashes.
	fingerprint := computeCompletionFingerprint(req.Result.Data, req.Artifacts)

	// Wrap result JSON to include the fingerprint so idempotent
	// re-completion can compare full artifact state, not just result data.
	type resultWithFingerprint struct {
		Data                  json.RawMessage `json:"data"`
		CompletionFingerprint string          `json:"completion_fingerprint"`
	}
	wrapped, err := json.Marshal(resultWithFingerprint{
		Data:                  req.Result.Data,
		CompletionFingerprint: fingerprint,
	})
	if err != nil {
		return fmt.Errorf("finalizer: marshal wrapped result: %w", err)
	}
	resultJSON := string(wrapped)
	resultHash := digest.SHA256Bytes(req.Result.Data)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO job_results (job_id, attempt, result_hash, codec_id, result_payload, created_at)
		VALUES (?, ?, ?, 'json', ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(job_id, attempt, result_hash) DO NOTHING`,
		req.Result.JobID, req.Result.Attempt, resultHash, string(req.Result.Data)); err != nil {
		// Minimal/legacy fixtures can predate the normalized result table.
		// Production migrated jobs DBs always take the canonical branch;
		// retaining this schema-gated fallback keeps the terminal transition
		// compatible while result_json is still present on older databases.
		if !strings.Contains(err.Error(), "no such table: job_results") {
			return fmt.Errorf("finalizer: persist job result: %w", err)
		}
	}

	// Atomic UPDATE with lease fence (same pattern as the existing
	// SQLiteStore.Complete in repository_lifecycle.go). Split-plane jobs DBs
	// intentionally removed the legacy result_json column; job_results above
	// is the canonical result store there. Keep the column only for older
	// schemas that still expose it.
	updateJobs := `UPDATE jobs SET status = 'SUCCEEDED', completed_at = ?,
		 progress = 100, worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING')
		 AND worker_id = ? AND lease_id = ?`
	args := []any{nowStr, nowStr, req.Result.JobID, req.Lease.WorkerID, req.Lease.LeaseID}
	if hasJobsColumn(ctx, tx, "result_json") {
		updateJobs = `UPDATE jobs SET status = 'SUCCEEDED', completed_at = ?, result_json = ?,
		 progress = 100, worker_id = '', lease_id = '', lease_expiry = NULL,
		 revision = revision + 1, updated_at = ?
		 WHERE id = ? AND status IN ('RUNNING', 'FINALIZING')
		 AND worker_id = ? AND lease_id = ?`
		args = []any{nowStr, resultJSON, nowStr, req.Result.JobID, req.Lease.WorkerID, req.Lease.LeaseID}
	}
	res, err := tx.ExecContext(ctx, updateJobs, args...)
	if err != nil {
		return fmt.Errorf("finalizer: update jobs: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return finalization.NewFinalizationError(
			"TRANSITION_CONFLICT",
			"job row was modified by another transaction after lease validation",
			req.Result.JobID, req.Lease.Attempt, nil,
		)
	}

	// Insert job event — propagate the error (previously silently ignored).
	evtID := fmt.Sprintf("evt_%d_%s", now.UnixNano(), randomHex(6))
	_, err = tx.ExecContext(ctx,
		`INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		evtID, req.Result.JobID, "job_completed",
		"job completed with artifacts via JobFinalizer", "{}", nowStr,
	)
	if err != nil {
		return fmt.Errorf("finalizer: insert job event: %w", err)
	}

	// P1.2 (July 2026): Persist the optional-artifact audit report
	// as a distinct job_events row inside the same SQLite transaction.
	// Skip when len(optionalReport)==0 to avoid bloating job_events
	// with empty sidecar rows on jobs that produced no optional
	// artifacts. The Err field on each record is json:"-" so we
	// serialise the typed error's Error() into the typed-data
	// ErrorMessage carrier for observability (the audit row reads
	// cleanly through standard JSON marshaling).
	if len(optionalReport) > 0 {
		payload, marshalErr := json.Marshal(struct {
			SchemaVersion string                                `json:"schema_version"`
			Records       []finalization.OptionalArtifactRecord `json:"records"`
		}{
			SchemaVersion: "v1",
			Records:       optionalReport,
		})
		if marshalErr != nil {
			return fmt.Errorf("finalizer: marshal optional_artifact_report: %w", marshalErr)
		}
		reportEvtID := fmt.Sprintf("evt_%d_%s_opt", now.UnixNano(), randomHex(6))
		_, err = tx.ExecContext(ctx,
			`INSERT INTO job_events (id, job_id, type, message, data_json, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			reportEvtID, req.Result.JobID, "optional_artifact_report",
			fmt.Sprintf("optional artifact audit report (%d records)", len(optionalReport)),
			string(payload), nowStr,
		)
		if err != nil {
			return fmt.Errorf("finalizer: insert optional_artifact_report job event: %w", err)
		}
	}

	return nil
}

func hasJobsColumn(ctx context.Context, tx *sql.Tx, wanted string) bool {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(jobs)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	var cid int
	var name, columnType string
	var notNull, primaryKey int
	var defaultValue sql.NullString
	for rows.Next() {
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false
		}
		if name == wanted {
			return true
		}
	}
	return false
}

// ── Helpers ─────────────────────────────────────────────────────────

// randomHex returns a random hex string of n bytes (2n characters).
// The output is derived from SHA-256 truncated to n bytes. n must be ≤ 32.
func randomHex(n int) string {
	h := digest.SHA256Bytes([]byte(fmt.Sprintf("job_finalizer_%d_%d", time.Now().UnixNano(), n)))
	return hex.EncodeToString([]byte(h)[:n])
}
