package scripts

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// ListStaleSubmissionRuns returns old nonterminal submission runs. The
// submission marker excludes observability recorder rows, and the workflow
// payload keeps PENDING distinct from the canonical RUNNING projection column.
func (r *SQLiteRunRepository) ListStaleSubmissionRuns(ctx context.Context, before time.Time, limit int) ([]scriptgen.StaleSubmissionRun, error) {
	if before.IsZero() {
		return nil, fmt.Errorf("scriptgeneration: stale run cutoff is required")
	}
	if limit <= 0 {
		return []scriptgen.StaleSubmissionRun{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT run_id, job_id, workflow_payload_json, updated_at
		FROM run_observability
		WHERE job_type='script.generate'
		  AND attempt_id=run_id||':script'
		  AND updated_at <= ?
		  AND CASE WHEN json_valid(workflow_payload_json)
		           THEN json_extract(workflow_payload_json, '$.status')
		           ELSE '' END IN ('PENDING','RUNNING')
		ORDER BY updated_at ASC, run_id ASC
		LIMIT ?`, formatTime(before.UTC()), limit)
	if err != nil {
		return nil, fmt.Errorf("scriptgeneration: list stale unlinked runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []scriptgen.StaleSubmissionRun
	for rows.Next() {
		var candidate scriptgen.StaleSubmissionRun
		var payload, updated string
		if err := rows.Scan(&candidate.RunID, &candidate.JobID, &payload, &updated); err != nil {
			return nil, fmt.Errorf("scriptgeneration: scan stale unlinked run: %w", err)
		}
		var checkpoint workflowCheckpoint
		if err := json.Unmarshal([]byte(payload), &checkpoint); err != nil {
			return nil, fmt.Errorf("scriptgeneration: decode stale run %s: %w", candidate.RunID, err)
		}
		candidate.IdempotencyKey = checkpoint.Request.IdempotencyKey
		candidate.CurrentStage = checkpoint.CurrentStage
		candidate.UpdatedAt, err = parseTime(updated)
		if err != nil {
			return nil, fmt.Errorf("scriptgeneration: parse stale run %s updated_at: %w", candidate.RunID, err)
		}
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scriptgeneration: iterate stale unlinked runs: %w", err)
	}
	return out, nil
}

var _ scriptgen.RunReconciliationRepository = (*SQLiteRunRepository)(nil)
