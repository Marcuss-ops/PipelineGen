// Package jobs — parent_aggregator.go: background poller that reads
// script.generate parents with parent_state=waiting_children, queries
// their children's terminal statuses, and finalizes the parent via
// FinalizeAggregateParent when the aggregate dictates.
package jobs

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// ScriptAggregatorDeps wires the parent aggregator's single external
// dependency (the jobs service) through a narrow interface so tests
// can inject stubs without constructing the full broker. Mirrors
// voiceover/jobs/parent_aggregator.go::AggregatorDeps exactly.
type ScriptAggregatorDeps struct {
	JobsSvc ScriptAggregatorJobsService
	RunRepo scriptgen.RunRepository
	Logger  *zap.Logger
	// PollInterval is the background-tick interval. Production: 30s.
	// Zero or negative defaults to 30s (mirrors voiceover).
	PollInterval time.Duration
	// RunCompleter completes a docs-deferred run once its deferred legs are
	// durable (TICKET-CORE-READY-DURABLE-DAG step 4). Satisfied by
	// *scriptgen.Runner via CompletePostCoreRun. Nil-safe: a nil completer
	// leaves docs-deferred parents waiting instead of completing them —
	// never a terminal flip without the run behind it.
	RunCompleter DocsDeferredRunCompleter
	// DocsEnqueuer re-drives a lost docs child (TICKET-CORE-READY-DURABLE-DAG
	// step 3 recovery). Satisfied by the same enqueuer the pipeline uses, so
	// a re-drive addresses the deterministic active key and can never fan out
	// a second publisher. Nil-safe: without it a childless parent waits for
	// an operator instead of being re-driven.
	DocsEnqueuer scriptgen.DocsPublishEnqueuer
}

// DocsDeferredRunCompleter is the narrow port the aggregator uses to complete
// a docs-deferred run after its deferred legs are durable. The single
// implementation is *scriptgen.Runner.CompletePostCoreRun, so the aggregator
// and the docs child share one completion semantic.
type DocsDeferredRunCompleter interface {
	CompletePostCoreRun(ctx context.Context, runID string) error
}

// ScriptAggregatorJobsService is the narrow surface the ParentAggregator
// needs from the jobs broker (Pattern 0 — AGENTS.md).
type ScriptAggregatorJobsService interface {
	Get(ctx context.Context, id string) (*job.Job, error)
	// List finds jobs by filter. The docs-deferred branch lists the deferred
	// Docs children by (type, correlation) so terminal children stay visible
	// after FindActiveByKey stops returning them.
	List(ctx context.Context, filter job.Filter) ([]job.Job, error)
	// ListAwaitingAggregation returns script.generate parents awaiting
	// aggregation (parent_state = waiting_children, broker status IN
	// RUNNING/FINALIZING/SUCCEEDED). Commit 3: parentType param scopes
	// the query to script.generate only.
	ListAwaitingAggregation(ctx context.Context, parentType string, limit int) ([]job.Job, error)
	// TerminalFlip is the canonical no-lease CAS that re-finalises the
	// parent (status, parent_state) atomically. expectedVersion is the
	// domain StateMachine.Version() — when > 0, the SQL layer adds
	// `AND revision = ?` as a second CAS fence.
	FinalizeAggregateParent(ctx context.Context, id string, targetStatus job.Status, result map[string]any, errMsg string, expectedVersion int) error
}

// Compile-time assertion: *appjobs.Service satisfies ScriptAggregatorJobsService.
//
// Note: the interface was narrowed in Commit 3 (removed List/Complete — the
// aggregator doesn't use them). *appjobs.Service still satisfies it because
// Go interface satisfaction is structural.
var _ ScriptAggregatorJobsService = (*appjobs.Service)(nil)

// ScriptParentResult is the typed parent job result. Field names
// and JSON tags match toFanoutResultMap's map shape exactly so
// json.Unmarshal(job.Result) into this struct is a drop-in replacement
// for map[string]any access. Mirrors voiceover/jobs/result_dto.go
// ::VoiceoverParentResult but adapted for script-batch semantics.
type ScriptParentResult struct {
	OK            bool                         `json:"ok"`
	ParentJobID   string                       `json:"parent_job_id"`
	TotalItems    int                          `json:"total_items"`
	ChildJobIDs   []string                     `json:"child_job_ids"`
	PerLanguage   []string                     `json:"per_language,omitempty"`
	StageProgress map[string]job.StageProgress `json:"stage_progress,omitempty"`
	ParentState   string                       `json:"parent_state"`
	RequestID     string                       `json:"request_id,omitempty"`
	FailedEnqueue int                          `json:"failed_enqueue_count,omitempty"`
	// AggregatorVersion is the StateMachine version at the last aggregator
	// tick (zero when the fan-out handler wrote the result).
	AggregatorVersion int `json:"_aggregator_version,omitempty"`
}

// IsAwaitingAggregation reports whether the parent is in a non-terminal
// application-level state that the aggregator should process.
//
// Commit 3 P0 #4: only waiting_children is non-terminal. partial_success
// is terminal — once the aggregator finalises a parent as partial_success,
// it must NOT be re-aggregated (P0 #7 infinite aggregation loop).
func (r *ScriptParentResult) IsAwaitingAggregation() bool {
	return ScriptParentState(r.ParentState) == ScriptParentWaitingChildren
}

// ScriptChildResult is the typed per-item child result the aggregator
// reads to apply the P0 #1-style false-success gate (P0 #4 extension).
// Voiceover uses an explicit *bool OK; scripts use a hardbool because
// the handler always emits the field (no absent-vs-false disambiguation
// needed). "parent_state" remains a string rather than typed enum so
// the wire shape stays JSON-stable across future refactors.
type ScriptChildResult struct {
	OK            *bool                        `json:"ok,omitempty"`
	Status        string                       `json:"status"`
	ItemID        string                       `json:"item_id"`
	JobID         string                       `json:"job_id"`
	ParentJobID   string                       `json:"parent_job_id"`
	RequestID     string                       `json:"request_id,omitempty"`
	Error         string                       `json:"error,omitempty"`
	StageProgress map[string]job.StageProgress `json:"stage_progress,omitempty"`
	// DocLink and DocID are propagated from the child handler's
	// toScriptItemResultMap (2026-07-07 fix). When the child generated
	// a Google Doc, these fields carry the doc URL and ID so the
	// aggregator can surface them in the parent result.
	DocLink string `json:"doc_link,omitempty"`
	DocID   string `json:"doc_id,omitempty"`
}

// ScriptAggregateResult is the typed output of the aggregation loop.
// Built from the domain StateMachine (Transition + Compute) and passed
// to finalizeParent for FinalizeAggregateParent persistence. Mirrors
// voiceover's VoiceoverAggregateResult.
type ScriptAggregateResult struct {
	ParentState    ScriptParentState
	TotalItems     int
	SucceededCount int
	FailedCount    int
	ParentRevision int
	ChildIDs       []string
	PerLanguage    []string
	StageProgress  map[string]job.StageProgress
	// ChildDocLinks maps child item_id → doc_link for succeeded children
	// that produced a Google Doc (2026-07-07 fix). Populated by aggregateOne
	// and surfaced in the parent result via finalizeParent.
	ChildDocLinks map[string]string
	// ChildDocIDs maps child item_id → doc_id (the Google Doc document ID)
	// for succeeded children that produced a Google Doc (2026-07-07 fix).
	ChildDocIDs map[string]string
}

// ScriptParentState is the canonical typed enum for script-batch
// parents (mirror of voiceover.ParentState). 5 named values matching
// the domain 5-state machine semantics.
type ScriptParentState string

const (
	// ScriptParentWaitingChildren — fan-out complete, awaiting child terminals.
	ScriptParentWaitingChildren ScriptParentState = "waiting_children"
	// ScriptParentPartialSuccess — some optional children succeeded,
	// some failed (mixed).
	ScriptParentPartialSuccess ScriptParentState = "partial_success"
	// ScriptParentSucceeded — all children succeeded.
	ScriptParentSucceeded ScriptParentState = "succeeded"
	// ScriptParentFailedTerminal — every child definitively failed.
	ScriptParentFailedTerminal ScriptParentState = "failed_terminal"
)

// ScriptParentAggregator re-finalises parent jobs once all children
// have reached terminal status.
type ScriptParentAggregator struct {
	deps    ScriptAggregatorDeps
	started atomic.Bool
}

// NewScriptParentAggregator constructs the poller. JobsSvc is mandatory
// (panic on nil — fail-fast per AGENTS.md WireUp pattern).
func NewScriptParentAggregator(deps ScriptAggregatorDeps) *ScriptParentAggregator {
	if deps.JobsSvc == nil {
		panic("scripts.Jobs.NewScriptParentAggregator: JobsSvc is required (ScriptAggregatorDeps.JobsSvc)")
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	if deps.PollInterval <= 0 {
		deps.PollInterval = 30 * time.Second
	}
	return &ScriptParentAggregator{deps: deps}
}

// Start launches the background ticker goroutine. Idempotent (atomic.Bool
// guard prevents double-spawn).
func (a *ScriptParentAggregator) Start(ctx context.Context) {
	if !a.started.CompareAndSwap(false, true) {
		a.deps.Logger.Info("script parent aggregator Start called twice; ignoring (idempotency guard)")
		return
	}
	go func() {
		ticker := time.NewTicker(a.deps.PollInterval)
		defer ticker.Stop()
		a.deps.Logger.Info("script parent aggregator started",
			zap.Duration("poll_interval", a.deps.PollInterval))
		a.Tick(ctx)
		for {
			select {
			case <-ctx.Done():
				a.deps.Logger.Info("script parent aggregator stopped")
				return
			case <-ticker.C:
				a.Tick(ctx)
			}
		}
	}()
}

// Tick performs one aggregation sweep. Errors on individual parents are
// logged and skipped — a failed parent is retried on the next tick.
func (a *ScriptParentAggregator) Tick(ctx context.Context) {
	jobs, err := a.deps.JobsSvc.ListAwaitingAggregation(ctx, appjobs.TypeScriptGenerate, 100)
	if err != nil {
		a.deps.Logger.Error("ScriptParentAggregator.Tick: ListAwaitingAggregation failed", zap.Error(err))
		return
	}
	if len(jobs) == 0 {
		return
	}
	for _, j := range jobs {
		if err := a.aggregateOne(ctx, j); err != nil {
			a.deps.Logger.Warn("ScriptParentAggregator.Tick: aggregateOne failed",
				zap.String("parent_job_id", j.ID), zap.Error(err))
		}
	}
}

// aggregateOne processes a single parent job: decodes the typed result,
// verifies it's awaiting aggregation, queries each child's terminal
// status, counts succeeded/failed, and finalises the parent.
type DocsDeferredParent struct {
	RunID                string                       `json:"run_id"`
	DocsDeferred         bool                         `json:"docs_deferred"`
	DocsChildCorrelation string                       `json:"docs_child_correlation,omitempty"`
	StageProgress        map[string]job.StageProgress `json:"stage_progress,omitempty"`
}

// docsDeferredOf reports whether a parent result is a docs-deferred waiting
// envelope, accepting both the root shape and the canonical
// ({"data":{...}}) envelope (same dual-shape discipline as
// decodeScriptParentResult).
func docsDeferredOf(raw []byte) (DocsDeferredParent, bool) {
	var docs DocsDeferredParent
	if len(raw) == 0 {
		return docs, false
	}
	if err := json.Unmarshal(raw, &docs); err == nil && docs.DocsDeferred && docs.RunID != "" {
		return docs, true
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data) == 0 {
		return DocsDeferredParent{}, false
	}
	docs = DocsDeferredParent{}
	if err := json.Unmarshal(envelope.Data, &docs); err != nil || !docs.DocsDeferred || docs.RunID == "" {
		return DocsDeferredParent{}, false
	}
	return docs, true
}

// aggregateDocsDeferred finalises a docs-deferred parent
// (TICKET-CORE-READY-DURABLE-DAG step 4: the terminal flip owned by the
// aggregator, reachable only once every required artifact is persisted).
//
// State machine, evaluated in order:
//
//  1. a non-terminal docs child exists → wait (re-tick);
//  2. a terminal SUCCEEDED docs child exists (with result.ok not false) →
//     complete the run through RunCompleter (idempotent) and flip the parent
//     SUCCEEDED/completed with the child's document references;
//  3. no children at all → re-drive one through DocsEnqueuer (deterministic
//     active key: a re-drive addresses the same child row, never a second
//     publisher); without an enqueuer the parent waits for an operator;
//  4. terminal FAILED children, none retryable → FailRun + flip FAILED;
//  5. terminal FAILED children with broker retry budget left → wait.
//
// A SUCCEEDED child whose run still is not completable is NEVER flipped: the
// completion would mint a COMPLETED without its documents. It waits (and
// warns) instead of lying.
func (a *ScriptParentAggregator) aggregateDocsDeferred(ctx context.Context, parent job.Job, docs DocsDeferredParent) {
	logger := a.deps.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	typ := domainScript.TypeDocsPublish
	var children []job.Job
	if docs.DocsChildCorrelation != "" {
		corr := docs.DocsChildCorrelation
		list, err := a.deps.JobsSvc.List(ctx, job.Filter{Type: &typ, CorrelationID: &corr})
		if err != nil {
			logger.Warn("ScriptParentAggregator: docs child list failed",
				zap.String("parent_job_id", parent.ID), zap.Error(err))
			return
		}
		children = list
	}
	var succeeded, retryableFailed *job.Job
	failedExhausted := false
	for i := range children {
		child := &children[i]
		if !child.Status.IsTerminal() {
			// Case 1: work still in flight — wait for the next tick (or the
			// broker retry cadence) instead of deciding on partial state.
			return
		}
		switch child.Status {
		case job.StatusSucceeded:
			if scriptItemP0_1Gate(child.Result) {
				failedExhausted = true
				continue
			}
			if succeeded == nil {
				succeeded = child
			}
		case job.StatusFailed, job.StatusRetryWait, job.StatusCancelled:
			if child.CanRetry() {
				retryableFailed = child
			} else {
				failedExhausted = true
			}
		default:
			// Unknown terminal status: do not decide on it.
			return
		}
	}
	// Case 2: documents published — complete the run, then flip the parent.
	if succeeded != nil {
		if a.deps.RunCompleter == nil {
			logger.Warn("ScriptParentAggregator: docs child succeeded but no run completer is wired; waiting",
				zap.String("parent_job_id", parent.ID), zap.String("run_id", docs.RunID))
			return
		}
		if err := a.deps.RunCompleter.CompletePostCoreRun(ctx, docs.RunID); err != nil {
			// ErrPostCoreNotCompletable (or any completion error) is a WAIT,
			// never a flip: flipping here would declare COMPLETED without the
			// documents the child was supposed to publish.
			logger.Warn("ScriptParentAggregator: post-core completion refused; waiting",
				zap.String("parent_job_id", parent.ID), zap.String("run_id", docs.RunID), zap.Error(err))
			return
		}
		resultMap := map[string]any{
			"ok":           true,
			"run_id":       docs.RunID,
			"parent_state": "completed",
		}
		var childResult ScriptChildResult
		if len(succeeded.Result) > 0 {
			if err := json.Unmarshal(succeeded.Result, &childResult); err == nil {
				if childResult.DocID != "" {
					resultMap["doc_id"] = childResult.DocID
				}
				if childResult.DocLink != "" {
					resultMap["doc_link"] = childResult.DocLink
				}
				if len(childResult.StageProgress) > 0 {
					resultMap["stage_progress"] = job.MergeStageProgress(docs.StageProgress, childResult.StageProgress)
				}
			}
		}
		if _, ok := resultMap["stage_progress"]; !ok && len(docs.StageProgress) > 0 {
			resultMap["stage_progress"] = docs.StageProgress
		}
		if err := a.deps.JobsSvc.FinalizeAggregateParent(ctx, parent.ID, job.StatusSucceeded, resultMap, "", parent.Revision); err != nil {
			logger.Warn("ScriptParentAggregator: docs parent flip failed",
				zap.String("parent_job_id", parent.ID), zap.Error(err))
			return
		}
		logger.Info("ScriptParentAggregator: docs-deferred parent completed",
			zap.String("parent_job_id", parent.ID), zap.String("run_id", docs.RunID))
		return
	}
	// Case 3: no child ever appeared — re-drive one. The deterministic active
	// key makes the re-drive address the same child row an in-flight enqueue
	// may have created, so this can never fan out a second publisher.
	if retryableFailed == nil && !failedExhausted {
		if a.deps.DocsEnqueuer == nil {
			logger.Warn("ScriptParentAggregator: docs-deferred parent has no child and no enqueuer; waiting for an operator",
				zap.String("parent_job_id", parent.ID), zap.String("run_id", docs.RunID))
			return
		}
		if err := a.deps.DocsEnqueuer.EnqueueDocsPublish(ctx, scriptgen.DocsPublishEnqueue{
			ParentJobID:   parent.ID,
			RunID:         docs.RunID,
			CorrelationID: docs.DocsChildCorrelation,
		}); err != nil {
			logger.Warn("ScriptParentAggregator: docs child re-drive failed",
				zap.String("parent_job_id", parent.ID), zap.Error(err))
			return
		}
		logger.Info("ScriptParentAggregator: docs child re-driven",
			zap.String("parent_job_id", parent.ID), zap.String("run_id", docs.RunID))
		return
	}
	// Case 5: a failed child with broker budget left — its retry is the
	// driver; the parent waits without spending anything.
	if retryableFailed != nil {
		return
	}
	// Case 4: every docs child definitively failed — fail the run and the parent.
	errMsg := "script aggregate: docs child definitively failed (deferred Docs leg)"
	if a.deps.RunRepo != nil {
		noteRunLedgerFailure(logger, "fail_run", docs.RunID, a.deps.RunRepo.FailRun(ctx, scriptgen.FailRunInput{
			RunID:        docs.RunID,
			FailedStage:  scriptgen.StageFailed,
			ErrorCode:    "SCRIPT_DOCS_FAILED",
			ErrorMessage: errMsg,
		}))
	}
	if err := a.deps.JobsSvc.FinalizeAggregateParent(ctx, parent.ID, job.StatusFailed, map[string]any{
		"ok":           false,
		"run_id":       docs.RunID,
		"parent_state": "failed_terminal",
	}, errMsg, parent.Revision); err != nil {
		logger.Warn("ScriptParentAggregator: docs parent fail flip failed",
			zap.String("parent_job_id", parent.ID), zap.Error(err))
	}
}
