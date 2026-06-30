// Package jobs — generate_handler.go (PR-VOICEOVER-PARENT-CHILD-FANOUT, P0.3, June 2026).
//
// GenerateJobHandler is the per-job-type handler subpackage for the
// voiceover capability. It hosts the dedicated handler for the new
// single voiceover.generate job type (Blocco 4 EXPAND, June 2026):
// one job per request, dispatched through the typed-port
// FanoutVoiceoversUseCase (Ports-only dependency per AGENTS.md
// Pattern 0).
//
// P0.3 architectural shift (June 2026): the parent GenerateJobHandler
// no longer fans out N languages via goroutines inside the use case
// (executor.Run sem channel pool). Instead it fans out N child jobs
// (one per language+voice pair, job.TypeVoiceoverGenerateItem) via
// the canonical job broker. The worker pool's per-job-type
// Concurrency field regulates sibling concurrency.
//
// godlike/07 EXPAND phase — this commit extends the system without
// removing the legacy voiceover.batch + voiceover.promo registrations
// wired in internal/application/voiceover/service.go at lines
// 132-133. CUTOVER (B-3) flips call sites to enqueue voiceover.generate
// instead; CONTRACT (B-4) removes back-compat aliases.
//
// Subpackage placement rationale: voiceover/jobs/ imports voiceover
// (the canonical use case) but voiceover does NOT import voiceover/jobs/
// (composition root handles the dependency injection). No circular
// dependency risk.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"go.uber.org/zap"
)

// FanoutUseCase aliases voiceover.Jobs.FanoutVoiceoversUseCase
// (stage-typed-port: parent handlers reference the fan-out use case
// from the canonical sub-package; no infrastructure imports leak
// here).
type FanoutUseCase = FanoutVoiceoversUseCase

// GenerateJobHandler is the dedicated handler for the
// voiceover.generate parent job type. Holds the typed-port
// FanoutUseCase and dispatches ONLY to its
// Execute(ctx, parentJobID, *cmd) method. NO goroutines are spawned
// here (PR-VOICEOVER-PARENT-CHILD-FANOUT P0.3 invariant).
//
// Why a dedicated handler (vs extending the legacy
// voiceover.Service.HandleJob switch): the new FanoutVoiceoversUseCase
// is a typed-port use case — its handler must NOT carry the legacy
// Service's per-type switch semantics. Sibling handlers
// (voiceover.batch, voiceover.promo) remain on the legacy path until
// the CUTOVER step removes them.
type GenerateJobHandler struct {
	useCase *FanoutVoiceoversUseCase
	logger  *zap.Logger
}

// NewGenerateJobHandler constructs the handler. useCase is mandatory
// (panic on nil — fail-fast per AGENTS.md WireUp pattern). Logger is
// optional (nil-safe via zap.NewNop()).
func NewGenerateJobHandler(useCase *FanoutVoiceoversUseCase, logger *zap.Logger) *GenerateJobHandler {
	if useCase == nil {
		panic("voiceover.Jobs.NewGenerateJobHandler: useCase is required (FanoutVoiceoversUseCase)")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GenerateJobHandler{
		useCase: useCase,
		logger:  logger,
	}
}

// Register binds the handler to the canonical jobs.Service dispatch
// for the voiceover.generate parent job type.
//
// Composition root owns WHEN this is called (post-bundle construction,
// pre-Freeze). Dep injection of Service + UseCase is centralised in
// internal/app/build_bundles_voiceover.go (B-2 BACKFILL) and the
// late-bindings block of internal/app/composition.go.
//
// Godlike/07: registering alongside the legacy voiceover.batch +
// voiceover.promo handlers is the EXPAND shape — no removal. Both
// paths coexist until CUTOVER flips the call sites.
func (h *GenerateJobHandler) Register(jobsSvc *appjobs.Service) {
	if jobsSvc == nil {
		h.logger.Warn("GenerateJobHandler.Register: jobsSvc is nil; handler not bound to dispatcher")
		return
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeVoiceoverGenerate, h.HandleJob); err != nil {
		h.logger.Error("GenerateJobHandler.Register: RegisterHandler failed",
			zap.String("job_type", appjobs.TypeVoiceoverGenerate),
			zap.Error(err))
		return
	}
	h.logger.Info("registered voiceover.generate handler",
		zap.String("job_type", appjobs.TypeVoiceoverGenerate))
}

// HandleJob processes a voiceover.generate parent job from the queue.
//
// P0.3 dispatch contract:
//   - json.Unmarshal failure → (nil, err): dispatcher marks job FAILED.
//   - cmd.Validate failure → (resultMap, err): dispatcher marks job FAILED.
//   - Fanout partial enqueue failure → (resultMap, err): dispatcher
//     marks job FAILED (godlike/07 — partial fan-out = parent failure,
//     NO silent success). The resultMap still carries the per-language
//     status so operators see which siblings enqueued + which failed.
//   - Fanout full enqueue success → (resultMap, nil): dispatcher
//     marks job SUCCEEDED. Commit 2's aggregator then RE-finalises
//     the parent based on outbox events from the children.
//
// Progress: tools.Progress is called once at start (5%) and once at
// end (100%). Per-child progress wiring lives in the new child
// handler (GenerateItemJobHandler) — the parent no longer iterates
// languages in-process.
func (h *GenerateJobHandler) HandleJob(
	ctx context.Context,
	j *appjobs.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	h.logger.Info("handling voiceover.generate job",
		zap.String("job_id", j.ID))

	if h.hasProgress(tools) {
		tools.Progress(5, "starting voiceover.generate fan-out")
	}

	var cmd voiceover.GenerateVoiceoversCommand
	if err := json.Unmarshal(j.Payload, &cmd); err != nil {
		return nil, fmt.Errorf("voiceover.generate: unmarshal payload: %w", err)
	}

	res, err := h.useCase.Execute(ctx, j.ID, &cmd)
	if err != nil {
		// PR-VO-AUDIT-P06 (June 2026): GUARD against FanoutVoiceoversUseCase
		// returning (nil, err) — happens on cmd==nil, cmd.Validate()
		// failure, or a panic-recovered use case. Pre-P06 the next two
		// lines dereferenced res.EnqueuedCount unconditionally, which
		// panicked the worker whenever the use case returned a nil
		// result with a non-nil err. The handler must tolerate both
		// shapes (validation failure returns nil; partial-fanout returns
		// a partial result with err) AND keep the result-map contract
		// intact (toFanoutResultMap is nil-safe so the same call
		// returns a well-formed partial-failure map for both shapes).
		enq, failEnq := 0, 0
		if res != nil {
			enq = res.EnqueuedCount
			failEnq = res.FailedEnqueueCount
		}
		h.logger.Error("voiceover.generate fan-out failure",
			zap.String("job_id", j.ID),
			zap.Int("enqueued", enq),
			zap.Int("failed_enqueue", failEnq),
			zap.Error(err))
		if h.hasProgress(tools) {
			tools.Progress(100, "voiceover.generate fan-out failed")
		}
		// Surface the partial-success result map so operators can
		// correlate which siblings enqueued + which failed. toFanoutResultMap
		// is nil-safe (it returns a well-formed partial-failure map
		// when res is nil), so the dispatcher writes the right shape
		// even on validation-failure paths. The dispatcher still
		// marks the parent job FAILED because err != nil.
		return toFanoutResultMap(res, j.ID), fmt.Errorf("voiceover.generate: fanout: %w", err)
	}

	if h.hasProgress(tools) {
		tools.Progress(100, "voiceover.generate fan-out complete")
	}

	// Full enqueue success: dispatcher marks parent SUCCEEDED. The
	// Commit 2 aggregator will later re-finalise the parent status
	// based on outbox events emitted by each child (SUCCEEDED +
	// (success_count, failed_count, total) for completed / partial; or
	// FAILED if all children failed).
	return toFanoutResultMap(res, j.ID), nil
}

// hasProgress is the nil-safe guard for the JobTools Progress callback.
func (h *GenerateJobHandler) hasProgress(tools *appjobs.JobTools) bool {
	return tools != nil && tools.Progress != nil
}

// toFanoutResultMap serialises a FanoutResult into the
// map[string]any shape the jobs.Dispatcher writes into job.Result
// JSON. Field names mirror the FanoutResult struct's JSON tags so
// the Commit 2 aggregator can unmarshal a parent job's result back
// into a typed FanoutResult.
func toFanoutResultMap(res *FanoutResult, parentJobID string) map[string]any {
	if res == nil {
		return map[string]any{
			"ok":           false,
			"parent_job_id": parentJobID,
			"enqueued_count": 0,
		}
	}
	pid := res.ParentJobID
	if pid == "" {
		pid = parentJobID
	}
	m := map[string]any{
		"ok":                   res.OK,
		"parent_job_id":        pid,
		"request_id":           res.RequestID,
		"total_outputs":        res.TotalOutputs,
		"enqueued_count":       res.EnqueuedCount,
		"failed_enqueue_count": res.FailedEnqueueCount,
		"child_job_ids":        res.ChildJobIDs,
		"per_language":         res.PerLanguage,
	}
	return m
}
