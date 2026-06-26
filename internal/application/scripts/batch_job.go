// Package scripts — batch_job.go hosts the application-layer job
// handler for `script.generate_batch`. PR-A (June 2026) moves it
// out of internal/api/script/handler_jobs.go so the canonical
// orchestrator (GenerateBatchUseCase) sits between the queue and
// BatchService.
//
// Before PR-A:
//
//	api/script/handler_jobs.go::ScriptFlowHandler.HandleBatchScriptGenerateJob
//	  → BatchService.Execute   (worker path, sync only)
//	api/script/handler_flow.go::ScriptFlowHandler.GenerateBatch
//	  → BatchService.Execute   (HTTP sync path)
//	  → jobsSvc.Enqueue         (HTTP async path)
//
// Two call sites, no shared orchestration; default-coercion, drive-
// folder resolution, and idempotency-key probing duplicated across
// both. PR-A collapses both into the canonical GenerateBatchUseCase.
// Run path: the worker side becomes a pure decoder-and-delegate
// (mirrors CurationJobServiceImpl.HandleCurateJob and CatalogJobServiceImpl.
// HandleCatalogScriptGenerateJob), and the HTTP sync path moves to
// the same use case.
//
// What this handler does:
//
//  1. Unmarshal j.Payload into a typed GenerateBatchRequest.
//  2. Force req.Async=false (the queue IS the async transport — a
//     worker must never re-enqueue from inside the worker loop).
//  3. Pipe tools.Progress through to uc.Run's optional ProgressFn
//     so the worker keeps the section-level progress signals the
//     previous handler emitted (regression-free behaviour).
//  4. Call uc.Run(ctx, GenerateBatchInput{Request: &req, ProgressFn: ...}).
//  5. Map the typed output back to the wire map the job dispatcher
//     expects (BatchGenerateResponse.ToMap() shape).
//
// What it does NOT do:
//
//   - Default coercion (use case owns it).
//   - Drive folder resolution (use case owns it).
//   - Idempotency-key probing (use case owns it; the worker re-run
//     is not a fresh client request, so the key is empty here).
//   - HTTP status codes (HTTP transport owns it — gone from worker
//     context entirely).
package scripts

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"go.uber.org/zap"
)

// BatchJobHandler is the application-layer job-system handler for
// `script.generate_batch` jobs. Registered via the jobs broker in
// wire_script.go:
//   root.Jobs.Service.RegisterHandler(job.TypeBatchScriptGenerate,
//       batchJobHandler.Handle)
//
// Type-aliasing against job.TypeBatchScriptGenerate (instead of a
// string literal) closes one of the SSOT violations the Wave 19
// audit documented (registry.go and payload.go both still redeclare
// the constant, but those are out of scope for PR-A; the wiring site
// now references the canonical domain constant).
type BatchJobHandler struct {
	uc  *GenerateBatchUseCase
	log *zap.Logger
}

// NewBatchJobHandler wires the handler to the use case.
func NewBatchJobHandler(uc *GenerateBatchUseCase, log *zap.Logger) *BatchJobHandler {
	return &BatchJobHandler{uc: uc, log: log}
}

// Handle is the queue-worker entry point. Strictly decoder +
// delegate — no business logic. Mirrors handler_flow_ops.go's
// RegenerateSection pattern (decode → use case → wire map).
func (h *BatchJobHandler) Handle(ctx context.Context, j *jobdomain.Job, tools *appjobs.JobTools) (map[string]any, error) {
	if h == nil || h.uc == nil {
		return nil, fmt.Errorf("batch job handler: use case not initialized")
	}

	var req GenerateBatchRequest
	if err := json.Unmarshal(j.Payload, &req); err != nil {
		return nil, fmt.Errorf("batch job handler: unmarshal payload: %w", err)
	}

	// Wave 19 invariant: the queue IS the async transport. A worker
	// must run sync regardless of what the persisted payload says,
	// otherwise we'd re-enqueue the same doc forever in worker loops.
	req.Async = false

	// Optional progress functor. The HTTP-sync path leaves this nil
	// (existing behaviour, regression-free); the worker path pipes
	// tools.Progress through so section-level updates land on the
	// job-status UI. The use case threads it down to
	// BatchService.Execute when present.
	var progressFn func(int, string)
	if tools != nil && tools.Progress != nil {
		progressFn = tools.Progress
	}

	if h.log != nil {
		h.log.Info("handling script.generate_batch job",
			zap.String("job_id", j.ID),
			zap.String("doc_title", req.DocTitle),
			zap.Int("items", len(req.Items)+len(req.BatchTopics)),
		)
	}

	out, err := h.uc.Run(ctx, GenerateBatchInput{
		Request:        &req,
		IdempotencyKey: "", // already-keyed at enqueue time; the worker re-run is not a fresh client request
		ProgressFn:     progressFn,
	})
	if err != nil {
		if h.log != nil {
			h.log.Error("batch job handler: use case failed",
				zap.String("job_id", j.ID), zap.Error(err))
		}
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("batch job handler: use case returned nil output (no error): job_id=%s", j.ID)
	}
	if out.Response == nil {
		// Defensive: req.Async=false was just forced, so out.Response
		// must be populated. If the use case ever evolves to enqueue
		// from inside Run (e.g. a sub-batch), surface the anomaly as
		// an error rather than returning a ghost job ref that the
		// worker never polls.
		return nil, fmt.Errorf("batch job handler: worker path expected sync output but got async ref: job_id=%s", j.ID)
	}

	if h.log != nil {
		h.log.Info("batch job handler: completed",
			zap.String("job_id", j.ID),
			zap.String("doc_title", out.Response.DocTitle),
			zap.Int("script_count", len(out.Response.Scripts)),
		)
	}
	return out.Response.ToMap(), nil
}
