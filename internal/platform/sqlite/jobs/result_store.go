package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// OverlayDriveLink is the operator-facing receipt written after the durable
// Drive outbox event has completed.
type OverlayDriveLink struct {
	ItemID      string `json:"item_id"`
	Language    string `json:"language"`
	PlanID      string `json:"plan_id,omitempty"`
	DriveFileID string `json:"drive_file_id"`
	DriveLink   string `json:"drive_link"`
	FolderID    string `json:"drive_folder_id,omitempty"`
}

// RecordOverlayDriveLink merges one completed overlay publication into the
// terminal job result. Outbox handlers can run before the parent job commits;
// returning an error in that case lets the outbox retry instead of writing a
// link that a later job completion would overwrite. Compare-and-swap updates
// preserve links when several overlay events complete concurrently.
func (r *SQLiteStore) RecordOverlayDriveLink(ctx context.Context, jobID string, link OverlayDriveLink) error {
	if r == nil || r.db == nil || strings.TrimSpace(jobID) == "" || strings.TrimSpace(link.ItemID) == "" || strings.TrimSpace(link.DriveLink) == "" {
		return fmt.Errorf("record overlay Drive link: job id, item id and Drive link are required")
	}
	for attempt := 0; attempt < 12; attempt++ {
		var status string
		if err := r.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&status); err != nil {
			return fmt.Errorf("record overlay Drive link: read job status: %w", err)
		}
		if !job.Status(status).IsTerminal() {
			return fmt.Errorf("record overlay Drive link: job %s is not terminal (status %s)", jobID, status)
		}
		var rowID int64
		var oldPayload string
		if err := r.db.QueryRowContext(ctx, `SELECT id, result_payload FROM job_results WHERE job_id = ? ORDER BY attempt DESC, id DESC LIMIT 1`, jobID).Scan(&rowID, &oldPayload); err != nil {
			return fmt.Errorf("record overlay Drive link: read job result: %w", err)
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal([]byte(oldPayload), &result); err != nil {
			return fmt.Errorf("record overlay Drive link: decode job result: %w", err)
		}
		if result == nil {
			return fmt.Errorf("record overlay Drive link: job result is not a JSON object")
		}
		// script.generate stores its rich GenerateResult under the envelope's
		// `result` member; keep the links beside overlay_render there so the
		// status API exposes job.result.result.overlay_links. Other job shapes
		// receive the projection at their root.
		projection := result
		nestedResult := false
		if nested := result["result"]; len(nested) != 0 {
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(nested, &decoded); err != nil {
				return fmt.Errorf("record overlay Drive link: decode nested result: %w", err)
			}
			if decoded != nil {
				projection = decoded
				nestedResult = true
			}
		}
		var links []OverlayDriveLink
		if raw := projection["overlay_links"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &links); err != nil {
				return fmt.Errorf("record overlay Drive link: decode existing links: %w", err)
			}
		}
		updated := false
		for i := range links {
			if links[i].ItemID == link.ItemID && links[i].Language == link.Language {
				links[i], updated = link, true
				break
			}
		}
		if !updated {
			links = append(links, link)
		}
		sort.Slice(links, func(i, j int) bool {
			if links[i].Language == links[j].Language {
				return links[i].ItemID < links[j].ItemID
			}
			return links[i].Language < links[j].Language
		})
		encodedLinks, err := json.Marshal(links)
		if err != nil {
			return fmt.Errorf("record overlay Drive link: encode links: %w", err)
		}
		projection["overlay_links"] = encodedLinks
		if nestedResult {
			nested, err := json.Marshal(projection)
			if err != nil {
				return fmt.Errorf("record overlay Drive link: encode nested result: %w", err)
			}
			result["result"] = nested
		}
		updatedPayload, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("record overlay Drive link: encode result: %w", err)
		}
		res, err := r.db.ExecContext(ctx, `UPDATE job_results SET result_payload = ?, result_hash = ? WHERE id = ? AND result_payload = ?`, string(updatedPayload), digest.SHA256String(string(updatedPayload)), rowID, oldPayload)
		if err != nil {
			return fmt.Errorf("record overlay Drive link: update result: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("record overlay Drive link: inspect update: %w", err)
		}
		if n == 1 {
			return nil
		}
	}
	return fmt.Errorf("record overlay Drive link: result for job %s kept changing", jobID)
}

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
	resultHash := digest.SHA256String(payload)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO job_results (job_id, attempt, result_hash, codec_id, result_payload, created_at)
		VALUES (?, ?, ?, 'json', ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(job_id, attempt, result_hash) DO NOTHING`,
		jobID, attempt, resultHash, payload)
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
	payloadHash := digest.SHA256String(payload)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO job_payloads (job_id, codec_id, payload, payload_hash, created_at)
		VALUES (?, 'json', ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(job_id) DO UPDATE SET codec_id=excluded.codec_id,
		payload=excluded.payload, payload_hash=excluded.payload_hash`,
		jobID, payload, payloadHash)
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
