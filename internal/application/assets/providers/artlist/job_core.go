package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

// JobCodec handles conversion between Artlist types and job payload/result maps.
type JobCodec struct{}

// PayloadFromRequest converts RunTagRequest to a map suitable for job payload.
func (c *JobCodec) PayloadFromRequest(req *RunTagRequest) map[string]any {
	if req == nil {
		return map[string]any{}
	}
	m := map[string]any{
		"term":           strings.TrimSpace(req.Term),
		"limit":          req.Limit,
		"root_folder_id": strings.TrimSpace(req.RootFolderID),
		"strategy":       strings.TrimSpace(req.Strategy),
		"dry_run":        req.DryRun,
	}
	if req.ClipDuration > 0 {
		m["clip_duration"] = req.ClipDuration
	}
	if req.Width > 0 {
		m["width"] = req.Width
	}
	if req.Height > 0 {
		m["height"] = req.Height
	}
	if req.FPS > 0 {
		m["fps"] = req.FPS
	}
	if req.Concurrency > 0 {
		m["concurrency"] = req.Concurrency
	}
	return m
}

func (c *JobCodec) RequestFromPayload(payload map[string]any) *RunTagRequest {
	req := &RunTagRequest{}
	if v, ok := payload["term"].(string); ok {
		req.Term = strings.TrimSpace(v)
	}
	if v, ok := payload["limit"].(float64); ok {
		req.Limit = int(v)
	} else if v, ok := payload["limit"].(int); ok {
		req.Limit = v
	}
	if v, ok := payload["root_folder_id"].(string); ok {
		req.RootFolderID = strings.TrimSpace(v)
	}
	if v, ok := payload["strategy"].(string); ok {
		req.Strategy = v
	}
	if v, ok := payload["dry_run"].(bool); ok {
		req.DryRun = v
	}
	if v, ok := payload["clip_duration"].(float64); ok {
		req.ClipDuration = int(v)
	} else if v, ok := payload["clip_duration"].(int); ok {
		req.ClipDuration = v
	}
	if v, ok := payload["width"].(float64); ok {
		req.Width = int(v)
	} else if v, ok := payload["width"].(int); ok {
		req.Width = v
	}
	if v, ok := payload["height"].(float64); ok {
		req.Height = int(v)
	} else if v, ok := payload["height"].(int); ok {
		req.Height = v
	}
	if v, ok := payload["fps"].(float64); ok {
		req.FPS = int(v)
	} else if v, ok := payload["fps"].(int); ok {
		req.FPS = v
	}
	if v, ok := payload["concurrency"].(float64); ok {
		req.Concurrency = int(v)
	} else if v, ok := payload["concurrency"].(int); ok {
		req.Concurrency = v
	}
	return req
}

func (c *JobCodec) ResultFromResponse(resp *RunTagResponse) map[string]any {
	result := map[string]any{
		"found":          resp.Found,
		"processed":      resp.Processed,
		"skipped":        resp.Skipped,
		"failed":         resp.Failed,
		"estimated_size": resp.EstimatedSize,
		"tag_folder_id":  resp.TagFolderID,
		"term":           resp.Term,
		"strategy":       resp.Strategy,
	}
	if resp.LastProcessedAt != nil {
		result["last_processed_at"] = *resp.LastProcessedAt
	}
	if len(resp.Items) > 0 {
		items := make([]map[string]any, 0, len(resp.Items))
		for _, item := range resp.Items {
			items = append(items, map[string]any{
				"clip_id":       item.ClipID,
				"name":          item.Name,
				"filename":      item.Filename,
				"status":        item.Status,
				"drive_link":    item.DriveLink,
				"drive_file_id": item.DriveFileID,
				"download_link": item.DownloadLink,
				"local_path":    item.LocalPath,
				"file_hash":     item.FileHash,
				"error":         item.Error,
			})
		}
		result["items"] = items
	}
	return result
}

func addItemFromMap(resp *RunTagResponse, itemMap map[string]any) {
	item := RunTagItem{}
	if v, ok := itemMap["clip_id"].(string); ok {
		item.ClipID = v
	}
	if v, ok := itemMap["name"].(string); ok {
		item.Name = v
	}
	if v, ok := itemMap["filename"].(string); ok {
		item.Filename = v
	}
	if v, ok := itemMap["status"].(string); ok {
		item.Status = v
	}
	if v, ok := itemMap["drive_link"].(string); ok {
		item.DriveLink = v
	}
	if v, ok := itemMap["drive_file_id"].(string); ok {
		item.DriveFileID = v
	}
	if v, ok := itemMap["download_link"].(string); ok {
		item.DownloadLink = v
	}
	if v, ok := itemMap["local_path"].(string); ok {
		item.LocalPath = v
	}
	if v, ok := itemMap["file_hash"].(string); ok {
		item.FileHash = v
	}
	if v, ok := itemMap["error"].(string); ok {
		item.Error = v
	}
	resp.Items = append(resp.Items, item)
}

// ResponseFromJob converts a domain job.Job to RunTagResponse.
// (domain job.Result is json.RawMessage, so we unmarshal it first)
func (c *JobCodec) ResponseFromJob(j *job.Job) *RunTagResponse {
	resp := &RunTagResponse{
		OK:        j.Status != job.StatusFailed,
		RunID:     j.ID,
		Status:    string(j.Status),
		Error:     j.Error,
		Found:     0,
		Processed: 0,
		Skipped:   0,
		Failed:    0,
	}
	if j.StartedAt != nil {
		started := j.StartedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.StartedAt = &started
	}
	if j.CompletedAt != nil {
		ended := j.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.EndedAt = &ended
	}
	if j.Payload != nil {
		var payload map[string]any
		if err := json.Unmarshal(j.Payload, &payload); err == nil {
			if v, ok := payload["term"].(string); ok {
				resp.Term = v
			}
			if v, ok := payload["strategy"].(string); ok {
				resp.Strategy = v
			}
			if v, ok := payload["dry_run"].(bool); ok {
				resp.DryRun = v
			}
			if v, ok := payload["root_folder_id"].(string); ok {
				resp.RootFolderID = v
			}
		}
	}
	// domain job.Result is json.RawMessage — unmarshal before indexing
	if len(j.Result) > 0 {
		var result map[string]any
		if err := json.Unmarshal(j.Result, &result); err == nil {
			resp.Found = getIntFromResult(result, "found")
			resp.Processed = getIntFromResult(result, "processed")
			resp.Skipped = getIntFromResult(result, "skipped")
			resp.Failed = getIntFromResult(result, "failed")
			resp.EstimatedSize = getIntFromResult(result, "estimated_size")
			if v, ok := result["tag_folder_id"].(string); ok {
				resp.TagFolderID = v
			}
			if v, ok := result["last_processed_at"].(string); ok {
				resp.LastProcessedAt = &v
			}
			if itemsRaw, ok := result["items"].([]any); ok {
				for _, itemRaw := range itemsRaw {
					if itemMap, ok := itemRaw.(map[string]any); ok {
						addItemFromMap(resp, itemMap)
					}
				}
			} else if itemsRaw, ok := result["items"].([]map[string]any); ok {
				for _, itemMap := range itemsRaw {
					addItemFromMap(resp, itemMap)
				}
			}
		}
	}
	return resp
}

// ResponseFromLegacyJob is removed — all callers now use domain *job.Job directly.
// Kept as a compile-time reference for the diff; callers should use ResponseFromJob.

// buildRunRecordFromResponse assembles a RunRecord from the worker-context
// domain Job ID + the orchestrator's RunTagResponse. PR-ARTLIST-PERSIST-FIX
// (2026-07-04): the canonical writer of artlist_runs aggregates.
//
// godlike/06 SSOT: this is the SINGLE construction site that maps a
// (JOB ID + RunTagResponse) pair onto a RunRecord. Future field
// additions to artlist_runs belong here AND in the adapter's
// translation (internal/app/artlist_runs_adapter.go) AND in the
// concrete's SQL column list (internal/infrastructure/database/sqlite/
// assets/artlist_runs_repository.go) — three sites in lockstep
// per the schema-reconciliation review of 2026-07-04.
//
// Schema reconciliation note: the RunRecord struct was simplified
// to drop Strategy / DryRun / StartedAt / CompletedAt / LastError —
// the artlist_runs migration does not have columns for those fields
// (verified verbatim against migrations/sqlite/001_velox_core.sql:
// 46-62). Status defaults to "completed" when OK, "failed" when
// any error path triggers.
func buildRunRecordFromResponse(jobID string, resp *RunTagResponse) RunRecord {
	rec := RunRecord{
		RunID:        jobID,
		Term:         resp.Term,
		RootFolderID: resp.RootFolderID,
		TagFolderID:  resp.TagFolderID,
		RequestedN:   resp.Requested,
		FoundN:       resp.Found,
		ProcessedN:   resp.Processed,
		SkippedN:     resp.Skipped,
		FailedN:      resp.Failed,
	}
	switch {
	case resp.Error != "" || !resp.OK:
		rec.Status = "failed"
		rec.ErrorMessage = resp.Error
	default:
		rec.Status = "completed"
	}
	return rec
}

var jobCodec = &JobCodec{}

// RegisterHandler registers HandleJob as the worker handler for media.artlist.
// The canonical job type constant lives in internal/application/jobs/registry.go
// (TypeArtlistRun = "media.artlist") and internal/domain/job/job.go. This method
// is the single call-site that bridges the Artlist service to the jobs dispatcher;
// composition root (build_bundles_artlist.go) calls it after WireArtlist.
func (a *JobAdapter) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("artlist.RegisterHandler: jobs service is nil")
	}
	return jobsSvc.RegisterHandler(appjobs.TypeArtlistRun, appjobs.HandlerFunc(a.HandleJob))
}

func (a *JobAdapter) HandleJob(ctx context.Context, j *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s := a.service
	s.log.Info("handling artlist job",
		zap.String("job_id", j.ID),
		zap.String("type", j.Type),
	)

	// Extract request from job payload directly (domain *job.Job)
	var payloadMap map[string]any
	if err := json.Unmarshal(j.Payload, &payloadMap); err != nil {
		payloadMap = map[string]any{}
	}
	req := jobCodec.RequestFromPayload(payloadMap)

	// Normalize the request (worker path)
	normalized := NormalizeRunTagRequest(*req, RunDefaults{
		DefaultRootFolderID: ResolveRootFolderID(s.cfg),
		MaxLimit:            500,
	})
	req = &normalized

	if strings.TrimSpace(req.RootFolderID) == "" {
		s.log.Warn("skipping artlist job because no root folder is configured", zap.String("job_id", j.ID), zap.String("term", req.Term))
		tools.Event("warning", "artlist job skipped: no root folder configured", map[string]any{
			"term": req.Term,
		})
		return map[string]any{
			"skipped": 1,
			"reason":  "no root folder configured",
		}, nil
	}

	resp, err := s.runOrchestrator.RunTag(ctx, req)
	if err != nil || (resp != nil && !resp.OK) {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		} else if resp != nil {
			errMsg = resp.Error
		}
		if errMsg == "" {
			errMsg = "unknown error"
		}
		tools.Event("error", "artlist run failed", map[string]any{
			"error": errMsg,
		})
		return nil, fmt.Errorf("%s", errMsg)
	}

	// Use centralized policy evaluation
	if failed, errMsg := EvaluateRunOutcome(resp); failed {
		tools.Event("error", errMsg, map[string]any{
			"failed": resp.Failed,
		})
		return nil, fmt.Errorf("%s", errMsg)
	}

	// PR-ARTLIST-PERSIST-FIX (2026-07-04): mandatory artlist_runs
	// aggregate write (godlike/07 no-fake-availability). Without this
	// step the handler can return SUCCEEDED + processed=N + ran the
	// orchestrator without ever writing a single row to artlist_runs
	// (the original fake-success bug). s.runRepo is the canonical
	// RunRepository port (PR-ARTLIST-PERSIST-FIX) — NewService
	// guarantees non-nil at composition time. If Record fails the
	// job is marked as failed so the operator sees a real error
	// rather than a fake-success aggregate row.
	if s.runRepo != nil && resp != nil {
		runRecord := buildRunRecordFromResponse(j.ID, resp)
		if err := s.runRepo.Record(ctx, runRecord); err != nil {
			tools.Event("error", "artlist_runs aggregate write failed", map[string]any{
				"run_id": j.ID,
				"error":  err.Error(),
			})
			s.log.Error("artlist_runs aggregate write failed (godlike/07 no-fake-availability)",
				zap.String("job_id", j.ID),
				zap.String("term", resp.Term),
				zap.Error(err),
			)
			return nil, fmt.Errorf("artlist_runs.Record(run_id=%q): %w", j.ID, err)
		}
	}

	tools.Event("completed", "artlist run completed", map[string]any{
		"found":     resp.Found,
		"processed": resp.Processed,
		"skipped":   resp.Skipped,
		"failed":    resp.Failed,
	})

	// Use codec to convert response to result map
	return jobCodec.ResultFromResponse(resp), nil
}
