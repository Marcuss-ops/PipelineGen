// Package jobs — handler_get_full.go: the GET /api/jobs/{id}/full
// projection and its activity/trace readers.
//
// Split out of impl.go to respect max_lines_per_file_strict=600 (same
// package, no behavior change), mirroring handler_get_full_test.go.
package jobs

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
)

func (h *JobsHandler) GetFull(c *gin.Context) {
	id := c.Param("id")

	j, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		apiutil.NotFound(c, "job not found")
		return
	}
	if j == nil {
		apiutil.NotFound(c, "job not found")
		return
	}

	// On error, fall back to an empty slice so the response shape stays stable;
	// clients polling /full already expect events to be an array (never null).
	// The canonical Event type lives in domain/job.
	events := []job.Event{}
	if eventsList, err := h.service.ListEvents(c.Request.Context(), id); err != nil {
		h.log.Error("failed to list job events", zap.String("job_id", id), zap.Error(err))
	} else {
		events = eventsList
	}

	// PR-ERROR-SURFACING (2026-07-04): godlike/06 SSOT between `/api/jobs` (LIST)
	// and `/api/jobs/{id}/full` (GET) — both endpoints MUST surface the canonical
	// job.Error field at TOP-level so a polled `/full` does not silently drop
	// typed-error strings (e.g. `scriptpkg.ErrScriptGenerationFailed` wraps
	// accumulated through the worker → jobs.error column → j.Error struct
	// field). The LIST endpoint already exposes each slice element's
	// `error` JSON tag (canonical `json:"error,omitempty"` in
	// internal/kernel/job/job.go::Job.Error); the /full response, which
	// historically enumerated only id/type/status/progress/current_step/events/
	// result/retryable/job in gin.H{}, DROPPED the `error` field at the
	// top-level (operators reading `/full` saw `error=None` even when the
	// DB column had a 123-char error string). The fix adds `error` to the
	// gin.H literal so parity with LIST holds end-to-end.
	//
	// Behaviour preservation: the canonical `job: j` embedded object
	// continues to expose `job.error` (unchanged), so callers using the
	// nested path keep working. The new top-level `error` is the canonical
	// surface for /full parity with /api/jobs LIST.
	resp := h.buildJobResponse(j, events)

	// PR-SCRIPT-TIMING: expose the canonical timing breakdown for the most
	// recent run. It is derived from run_observability.report_json (the
	// persisted RunReport — stages, operations, and the derived
	// attributed/unattributed/bottleneck fields), never from job timestamps
	// or a second clock. Best-effort: an absent reader/report yields a null
	// `timing` field without changing the /full status response.
	resp["timing"] = h.readTimingBreakdown(c.Request.Context(), id)

	// script.generate persists its caller-visible script checkpoint in the
	// durable run ledger before the broker job finishes publishing every
	// requested artifact. Project that snapshot onto the canonical polling
	// endpoint, but leave the job's top-level status untouched: CORE_READY is
	// availability, never job success. The separate script field is intentionally
	// only the canonical output projection, not a second job result or a promise
	// that other requested artifacts have completed.
	if j.Type == scriptpkg.TypeGenerate {
		snapshot := h.readScriptRunSnapshot(c.Request.Context(), id)
		resp["core_ready"] = snapshot != nil && snapshot.CurrentStage == "CORE_READY"
		if snapshot != nil {
			resp["script_run"] = gin.H{
				"run_id":        snapshot.RunID,
				"status":        snapshot.Status,
				"current_stage": snapshot.CurrentStage,
			}
			if snapshot.CurrentStage == "CORE_READY" && len(snapshot.Result) > 0 {
				var result struct {
					Output json.RawMessage `json:"output"`
				}
				if err := json.Unmarshal(snapshot.Result, &result); err == nil && len(result.Output) > 0 && string(result.Output) != "null" {
					resp["script"] = result.Output
				}
				resp["current_event_stage"] = resp["current_stage"]
				resp["current_stage"] = snapshot.CurrentStage
			}
		}
	}

	apiutil.OK(c, resp)
}

// readTimingBreakdown returns the canonical timing summary for a job's most
// recent run, or nil when the history reader is absent, no report exists, or
// the report cannot be parsed. It is deliberately best-effort: timing
// diagnostics must never fail or alter the /full status surface.
func (h *JobsHandler) readTimingBreakdown(ctx context.Context, id string) any {
	if h.history == nil {
		return nil
	}
	raw, err := h.history.GetRunReport(ctx, id)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var report kernobs.RunReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil
	}
	return report.TimingSummary()
}

// readScriptRunSnapshot returns the durable script checkpoint when the
// configured history backend supports that projection. It is best-effort so
// an unavailable run ledger does not make the canonical broker-status endpoint
// unavailable; the job status itself remains authoritative for completion.
func (h *JobsHandler) readScriptRunSnapshot(ctx context.Context, id string) *ScriptRunSnapshot {
	reader, ok := h.history.(ScriptRunReader)
	if !ok {
		return nil
	}
	snapshot, err := reader.GetScriptRunSnapshot(ctx, id)
	if err != nil {
		if h.log != nil {
			h.log.Warn("failed to read script run snapshot", zap.String("job_id", id), zap.Error(err))
		}
		return nil
	}
	return snapshot
}
