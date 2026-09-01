package jobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// persistJobResult is the sole write path for durable job results. The hot
// jobs row intentionally does not carry the result payload; callers invoke
// this with the same transaction as the lifecycle transition.
func persistJobResult(ctx context.Context, tx *sql.Tx, jobID string, attempt int, payload string) error {
	if tx == nil {
		return fmt.Errorf("persist job result: nil transaction")
	}
	if payload == "" || payload == "null" {
		payload = "{}"
	}
	sum := sha256.Sum256([]byte(payload))
	_, err := tx.ExecContext(ctx, `
		INSERT INTO job_results (job_id, attempt, result_hash, codec_id, result_payload, created_at)
		VALUES (?, ?, ?, 'json', ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(job_id, attempt, result_hash) DO NOTHING`,
		jobID, attempt, hex.EncodeToString(sum[:]), payload)
	if err != nil {
		// Legacy/minimal fixtures may predate migration 119. This fallback is
		// intentionally schema-gated; migrated production databases never
		// write the result payload to the hot jobs row.
		if strings.Contains(err.Error(), "no such table: job_results") {
			if _, legacyErr := tx.ExecContext(ctx, `UPDATE jobs SET result_json = ? WHERE id = ?`, payload, jobID); legacyErr == nil {
				return nil
			}
		}
		return fmt.Errorf("persist job result %q: %w", jobID, err)
	}
	return nil
}

func (r *SQLiteStore) hydrateLatestResult(ctx context.Context, j *job.Job) error {
	if r == nil || r.db == nil || j == nil {
		return nil
	}
	var payload string
	err := r.db.QueryRowContext(ctx, `
		SELECT result_payload FROM job_results
		WHERE job_id = ? ORDER BY attempt DESC, id DESC LIMIT 1`, j.ID).Scan(&payload)
	if err == sql.ErrNoRows {
		// Compatibility read for pre-contraction databases. New jobs planes
		// have no result_json column, so the probe is schema-gated.
		if legacy, legacyErr := r.legacyJobJSON(ctx, j.ID, "result_json"); legacyErr == nil && legacy != "" {
			j.Result = []byte(legacy)
		}
		return nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "no such table: job_results") {
			legacy, legacyErr := r.legacyJobJSON(ctx, j.ID, "result_json")
			if legacyErr == nil && legacy != "" {
				j.Result = []byte(legacy)
			}
			return nil
		}
		return err
	}
	j.Result = []byte(payload)
	return nil
}

func persistJobPayload(ctx context.Context, tx *sql.Tx, jobID string, payload string) error {
	if tx == nil {
		return fmt.Errorf("persist job payload: nil transaction")
	}
	if payload == "" || payload == "null" {
		payload = "{}"
	}
	sum := sha256.Sum256([]byte(payload))
	_, err := tx.ExecContext(ctx, `
		INSERT INTO job_payloads (job_id, codec_id, payload, payload_hash, created_at)
		VALUES (?, 'json', ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(job_id) DO UPDATE SET codec_id=excluded.codec_id,
		payload=excluded.payload, payload_hash=excluded.payload_hash`,
		jobID, payload, hex.EncodeToString(sum[:]))
	if err != nil {
		if strings.Contains(err.Error(), "no such table: job_payloads") {
			if _, legacyErr := tx.ExecContext(ctx, `UPDATE jobs SET payload_json = ? WHERE id = ?`, payload, jobID); legacyErr == nil {
				return nil
			}
		}
		return fmt.Errorf("persist job payload %q: %w", jobID, err)
	}
	return nil
}

func (r *SQLiteStore) hydrateLatestPayload(ctx context.Context, j *job.Job) error {
	if r == nil || r.db == nil || j == nil {
		return nil
	}
	var payload string
	err := r.db.QueryRowContext(ctx, `SELECT payload FROM job_payloads WHERE job_id = ?`, j.ID).Scan(&payload)
	if err == sql.ErrNoRows {
		if legacy, legacyErr := r.legacyJobJSON(ctx, j.ID, "payload_json"); legacyErr == nil && legacy != "" {
			j.Payload = []byte(legacy)
		}
		return nil
	}
	if err != nil {
		if strings.Contains(err.Error(), "no such table: job_payloads") {
			legacy, legacyErr := r.legacyJobJSON(ctx, j.ID, "payload_json")
			if legacyErr == nil && legacy != "" {
				j.Payload = []byte(legacy)
			}
			return nil
		}
		return err
	}
	j.Payload = []byte(payload)
	return nil
}

// legacyJobJSON reads one retired inline JSON field only when it still exists
// on an older database. Column names are selected from a closed allowlist.
func (r *SQLiteStore) legacyJobJSON(ctx context.Context, jobID, column string) (string, error) {
	if column != "payload_json" && column != "result_json" {
		return "", fmt.Errorf("unsupported legacy jobs column %q", column)
	}
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT `+column+` FROM jobs WHERE id = ?`, jobID).Scan(&value)
	return value, err
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func hasJobsColumn(ctx context.Context, q rowQueryer, column string) bool {
	var present int
	err := q.QueryRowContext(ctx,
		`SELECT 1 FROM pragma_table_info('jobs') WHERE name = ? LIMIT 1`, column,
	).Scan(&present)
	return err == nil && present == 1
}

func (r *SQLiteStore) hydrateJob(ctx context.Context, j *job.Job) error {
	if err := r.hydrateLatestPayload(ctx, j); err != nil {
		return err
	}
	return r.hydrateLatestResult(ctx, j)
}
