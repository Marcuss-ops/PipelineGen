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
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	jobvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	"go.uber.org/zap"
)

// GenerateJobHandler is the dedicated handler for the
// voiceover.generate parent job type. Holds the typed-port
// FanoutVoiceoversUseCase and dispatches ONLY to its
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
//
// Audit P0 #2 (July 2026): Register now returns error so the
// composition root can fail-closed at boot if the dispatcher rejects
// the binding. Pre-P0 #2 the silent-Warn path meant a future CallSite
// regression (e.g. jobs.Service receives a different registry mid-
// migration) would silently dead-letter every parent job — the same
// failure mode that triggered audit-P0 1 month prior. Returning the
// error lets NewComposition abort with a typed message that
// operators can grep on.
// Register propagates wiring errors — composition root MUST fail-closed on non-nil return.
//
// P1 #1 (July 2026): wraps appjobs.ErrMissingDeps via %w so the
// composition root + tests can assert via errors.Is(err, appjobs.ErrMissingDeps)
// regardless of which handler-specific prefix the future maintainer
// adds or removes. The handler-specific diagnostic prefix is preserved
// for operator logs.
func (h *GenerateJobHandler) Register(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("GenerateJobHandler.Register: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(jobvoiceover.TypeGenerate, appjobs.HandlerFunc(h.HandleJob)); err != nil {
		return fmt.Errorf("GenerateJobHandler.Register: bind %q to dispatcher: %w",
			jobvoiceover.TypeGenerate, err)
	}
	if h.logger != nil {
		h.logger.Info("registered voiceover.generate handler",
			zap.String("job_type", jobvoiceover.TypeGenerate))
	}
	return nil
}

// HandleJob processes a voiceover.generate parent job from the queue.
//
// Dispatch contract (P0.3 → P0.5, June 2026):
//   - json.Unmarshal failure → (nil, err): dispatcher marks job FAILED.
//   - cmd.Validate failure → (resultMap, err): dispatcher marks job FAILED.
//     The resultMap carries parent_state="failed" so operators / the
//     #5 aggregator can read it from job.Result.
//   - Fanout partial enqueue failure → (resultMap, err): dispatcher
//     marks job FAILED (godlike/07 — partial fan-out = parent failure,
//     NO silent success). The resultMap carries parent_state=
//     "partial_success" — fan-out completed for some children but
//     not all; operators see which siblings enqueued + which failed.
//   - Fanout full enqueue success → (resultMap, nil): dispatcher
//     marks job SUCCEEDED at the terminal-state level. BUT the
//     resultMap carries parent_state="waiting_children" — the
//     application-level state reflects "children in flight" rather
//     than "all children succeeded" (PR-VO-AUDIT-P05 micro-commit
//     #4). The micro-commit #5 aggregator will read parent_state
//     from job.Result and compute the durable terminal state.
//
// Progress: tools.Progress is called once at start (5%) and once at
// end (100%). Per-child progress wiring lives in the new child
// handler (GenerateItemJobHandler) — the parent no longer iterates
// languages in-process. The parent_state emit replaces the prior
// "Dispatcher SUCCEEDED = parent succeeded" invariant with
// "Dispatcher SUCCEEDED = fan-out done; re-finalise on children".
func (h *GenerateJobHandler) HandleJob(
	ctx context.Context,
	j *appjobs.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	h.logger.Info("handling voiceover.generate job",
		zap.String("job_id", j.ID))

	// job-tools.Progress nil-safe wrapper — canonical for all 3 handlers
	// (voiceover.generate, voiceover.generate_item, script.generate). The
	// Creator-runtime wrap path passes tools=nil; the SafeProgressFn
	// utility captures the canonical nil-tolerance gate so consumer
	// sites can call pf(...) directly without per-call nil checks.
	pf := appjobs.SafeProgressFn(tools)
	ef := appjobs.SafeEventFn(tools)

	pf(5, "starting voiceover.generate fan-out")

	var cmd voiceover.GenerateVoiceoversCommand
	if err := json.Unmarshal(j.Payload, &cmd); err != nil {
		return nil, fmt.Errorf("voiceover.generate: unmarshal payload: %w", err)
	}

	// P0.6 request_id threading: if the API caller supplied a
	// CorrelationID, thread it into the command so the fan-out and
	// every child use the SAME request_id. Without this, the fan-out
	// generates a new BuildRequestID() and the chain diverges:
	// API request_id (A) → correlation (A) → fanout generates B →
	// child ignores B → GenerateBatch generates C.
	if cmd.RequestID == "" {
		if j.CorrelationID != "" {
			cmd.RequestID = j.CorrelationID
		} else {
			cmd.RequestID = j.ID // fallback: parent job ID is stable
		}
	}

	res, err := h.useCase.Execute(ctx, j.ID, &cmd)
	stageProgress := map[string]any{
		"stage":          string(job.StageVoiceover),
		"language":       "*",
		"status":         string(job.StageRunning),
		"job_id":         j.ID,
		"stage_progress": nil,
	}
	if res != nil {
		stageProgress["stage_progress"] = res.StageProgress
	}
	ef("stage_progress", "Voiceover fan-out progress aggregated", stageProgress)
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
		pf(100, "voiceover.generate fan-out failed")
		// Surface the partial-success result map so operators can
		// correlate which siblings enqueued + which failed. toFanoutResultMap
		// is nil-safe (it returns a well-formed partial-failure map
		// when res is nil), so the dispatcher writes the right shape
		// even on validation-failure paths. The dispatcher still
		// marks the parent job FAILED because err != nil.
		return toFanoutResultMap(res, j.ID), fmt.Errorf("voiceover.generate: fanout: %w", err)
	}

	pf(100, "voiceover.generate fan-out complete")

	// FASE 1 (July 2026): the parent is NOT truly terminal after fan-out.
	// Returning (resultMap, nil) tells the broker to mark SUCCEEDED — but
	// this is TEMPORARY. The parent aggregator's FinalizeAggregateParent will re-finalise
	// the parent status based on real child outcomes: preserving SUCCEEDED
	// on all-succeeded, or flipping to FAILED when all children definitively
	// failed (P0 #1 closure). The result map carries parent_state=
	// waiting_children to signal "not yet terminal at the application level".
	return toFanoutResultMap(res, j.ID), nil
}

// toFanoutResultMap serialises a FanoutResult into the
// map[string]any shape the jobs.Dispatcher writes into job.Result
// JSON. Field names mirror the FanoutResult struct's JSON tags so
// the micro-commit #5 aggregator can unmarshal a parent job's
// result back into a typed FanoutResult.
//
// PR-VO-AUDIT-P05 micro-commit #4 (June 2026): the result map now
// carries parent_state under the canonical key "parent_state"
// (string-encoded voiceover.ParentState). The emit shapes:
//
//   - res == nil   → "failed"       (validation-failure / nil-fanout
//     paths; aggregator branch-inactive)
//   - res.OK == false → "partial_success"
//     (per-language partial fan-out; some
//     children could not be enqueued but
//     the ones that did are in flight)
//   - res.OK == true  → "waiting_children"
//     (full enqueue success — children
//     are in flight; aggregator will
//     re-finalise on terminal)
//
// The dispatcher still marks the parent SUCCEEDED when (resultMap,
// nil) is returned. The application-level state is in result
// ["parent_state"] — the operative invariant this micro-commit
// establishes: "Dispatcher SUCCEEDED != parent succeeded; we always
// emit parent_state never == succeeded here in micro-commit #4.
func toFanoutResultMap(res *FanoutResult, parentJobID string) map[string]any {
	if res == nil {
		return map[string]any{
			"ok":             false,
			"parent_job_id":  parentJobID,
			"request_id":     parentJobID,
			"enqueued_count": 0,
			"parent_state":   string(voiceover.ParentFailed),
		}
	}
	pid := res.ParentJobID
	if pid == "" {
		pid = parentJobID
	}
	ps := voiceover.ParentWaitingChildren
	if !res.OK {
		ps = voiceover.ParentPartialSuccess
	} // FASE 1 (July 2026): result map carries expected_children so the
	// parent aggregator can reconstruct the domain StateMachine with
	// the correct expected count on every tick, even when the parent
	// job's result map is the only durable source of truth before the
	// parent_aggregator_state table migration (Step 12B-C2).
	//
	// expected_children = EnqueuedCount (children actually enqueued),
	// NOT TotalOutputs. On partial fan-out, TotalOutputs > EnqueuedCount
	// and the aggregator should only track children that were actually
	// created — the failed enqueue entries have empty-string child IDs
	// that extractChildJobIDs filters out.
	m := map[string]any{
		"ok":                   res.OK,
		"parent_job_id":        pid,
		"request_id":           res.RequestID,
		"total_outputs":        res.TotalOutputs,
		"expected_children":    res.EnqueuedCount,
		"enqueued_count":       res.EnqueuedCount,
		"failed_enqueue_count": res.FailedEnqueueCount,
		"child_job_ids":        res.ChildJobIDs,
		"per_language":         res.PerLanguage,
		"stage_progress":       res.StageProgress,
		"parent_state":         string(ps),
	}
	return m
}
