// Package jobs — parent_aggregator.go: background poller that reads
// script.generate parents with parent_state=waiting_children, queries
// their children's terminal statuses, and finalizes the parent via
// FinalizeAggregateParent when the aggregate dictates.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
}

// ScriptAggregatorJobsService is the narrow surface the ParentAggregator
// needs from the jobs broker (Pattern 0 — AGENTS.md).
type ScriptAggregatorJobsService interface {
	Get(ctx context.Context, id string) (*job.Job, error)
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
func (a *ScriptParentAggregator) aggregateOne(ctx context.Context, j job.Job) error {
	// Decode the typed parent result.
	var parentResult ScriptParentResult
	if len(j.Result) > 0 {
		if err := decodeScriptParentResult(j.Result, &parentResult); err != nil {
			a.deps.Logger.Debug("ScriptParentAggregator: cannot unmarshal parent result, skipping",
				zap.String("parent_job_id", j.ID), zap.Error(err))
			return nil
		}
	}

	// Only process parents awaiting aggregation.
	if !parentResult.IsAwaitingAggregation() {
		return nil
	}

	// Filter empty child IDs (failed-enqueue placeholders).
	childIDs := parentResult.ChildJobIDs
	filtered := make([]string, 0, len(childIDs))
	for _, id := range childIDs {
		if id == "" {
			continue
		}
		filtered = append(filtered, id)
	}
	childIDs = filtered

	// Edge case: zero children enqueued → FAILED terminal immediately.
	//
	// FASE 4 (July 2026) spec close-out: zero children created =
	// FAILED terminal (not partial_success). The pre-FASE-4 mapping
	// (ScriptParentPartialSuccess) conflated dispatch-failure (zero
	// enqueued) with partial-success (mixed terminal) — two
	// semantically distinct states. The pre-FASE-4 mapping was a
	// false-positive terminal leak that masked dispatch failures
	// in the operator dashboard. The canonical terminal for "no
	// children enqueued" is ScriptParentFailedTerminal (per the
	// 5-state machine; the constant is already declared in this
	// file at line 138-139).
	if len(childIDs) == 0 {
		a.finalizeParent(ctx, j.ID, ScriptAggregateResult{
			ParentState:    ScriptParentFailedTerminal,
			TotalItems:     0,
			ParentRevision: j.Revision,
		})
		return nil
	}

	// Query each child and count terminal outcomes. Keep one progress slot
	// per requested item so failed-enqueue placeholders cannot shift the
	// language associated with a later child.
	succeeded := 0
	failed := parentResult.FailedEnqueue
	allTerminal := true
	stageStatuses := make([]job.StageLanguageStatus, len(parentResult.ChildJobIDs))
	childStageIndex := make(map[string]int, len(childIDs))
	for index, childID := range parentResult.ChildJobIDs {
		language := ""
		if index < len(parentResult.PerLanguage) {
			language = parentResult.PerLanguage[index]
		}
		status := job.StageQueued
		if childID == "" {
			status = job.StageFailed
		}
		stageStatuses[index] = job.StageLanguageStatus{
			Stage: job.StageScript, Language: language, Status: status, JobID: childID,
		}
		if childID != "" {
			childStageIndex[childID] = index
		}
	}
	childDocLinks := make(map[string]string) // item_id → doc_link (2026-07-07 fix)
	childDocIDs := make(map[string]string)   // item_id → doc_id (2026-07-07 fix)
	childStageProgress := make(map[string]job.StageProgress)
	for _, childID := range childIDs {
		var status job.Status
		var childOK bool
		var childResult ScriptChildResult

		childJob, err := a.deps.JobsSvc.Get(ctx, childID)
		if err != nil {
			a.deps.Logger.Warn("ScriptParentAggregator: Get child failed",
				zap.String("parent_job_id", j.ID),
				zap.String("child_job_id", childID),
				zap.Error(err))
			allTerminal = false
			continue
		}
		if childJob == nil {
			a.deps.Logger.Warn("ScriptParentAggregator: child job missing",
				zap.String("parent_job_id", j.ID),
				zap.String("child_job_id", childID))
			allTerminal = false
			continue
		}
		status = childJob.Status

		// P0.1-gate: broker SUCCEEDED + result.ok=false → FAILED.
		if status == job.StatusSucceeded {
			if isOKFalseOverride := scriptItemP0_1Gate(childJob.Result); isOKFalseOverride {
				a.deps.Logger.Warn("ScriptParentAggregator: child broker-succeeded but result.ok=false",
					zap.String("parent_job_id", j.ID),
					zap.String("child_job_id", childID))
				status = job.StatusFailed
			}
		}
		if status == job.StatusFailed || status == job.StatusCancelled {
			if len(childJob.Result) > 0 {
				if err := json.Unmarshal(childJob.Result, &childResult); err == nil {
					if childResult.OK != nil {
						childOK = *childResult.OK
					}
				}
			}
		} else if status == job.StatusSucceeded {
			if len(childJob.Result) > 0 {
				if err := json.Unmarshal(childJob.Result, &childResult); err == nil {
					if childResult.OK != nil {
						childOK = *childResult.OK
					}
				}
			}
		}

		// Non-terminal children → parent stays open (re-tick).
		if !status.IsTerminal() {
			allTerminal = false
			continue
		}

		// All script-batch children are OPTIONAL. Count terminal outcome.
		if stageIndex, ok := childStageIndex[childID]; ok {
			stageStatuses[stageIndex].Status = job.StageFailed
			if status == job.StatusSucceeded && childOK {
				stageStatuses[stageIndex].Status = job.StageCompleted
			}
		}
		if len(childResult.StageProgress) > 0 {
			childStageProgress = job.MergeStageProgress(childStageProgress, childResult.StageProgress)
		}
		if (status == job.StatusSucceeded) && childOK {
			succeeded++
			// Collect doc_link/doc_id from succeeded children so the
			// parent result surfaces Google Doc links to operators
			// (2026-07-07 fix — previously dropped by toScriptItemResultMap).
			if childResult.DocLink != "" {
				childDocLinks[childResult.ItemID] = childResult.DocLink
			}
			if childResult.DocID != "" {
				childDocIDs[childResult.ItemID] = childResult.DocID
			}
		} else {
			failed++
		}
	}

	// Skip until all children are terminal.
	if !allTerminal {
		return nil
	}

	// Compute total items and derive the aggregate parent state.
	totalItems := parentResult.TotalItems
	if totalItems <= 0 {
		totalItems = len(childIDs) + parentResult.FailedEnqueue
	}

	var state ScriptParentState
	switch {
	case failed == totalItems && totalItems > 0:
		state = ScriptParentFailedTerminal
	case failed > 0:
		state = ScriptParentPartialSuccess
	default:
		state = ScriptParentSucceeded
	}

	aggResult := ScriptAggregateResult{
		ParentState:    state,
		TotalItems:     totalItems,
		SucceededCount: succeeded,
		FailedCount:    failed,
		ParentRevision: j.Revision,
		ChildIDs:       childIDs,
		PerLanguage:    parentResult.PerLanguage,
		StageProgress:  job.MergeStageProgress(job.AggregateStageProgressByStage(stageStatuses), childStageProgress),
		ChildDocLinks:  childDocLinks,
		ChildDocIDs:    childDocIDs,
	}
	a.finalizeParent(ctx, j.ID, aggResult)
	return nil
}

// decodeScriptParentResult accepts both the legacy root result shape and the
// canonical completion envelope shape ({"data":{...}}). The worker stores
// completed handler results through the typed envelope, while older rows and
// hermetic tests still use the root map directly.
func decodeScriptParentResult(raw []byte, out *ScriptParentResult) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	if out.ParentState != "" || out.ParentJobID != "" || len(out.ChildJobIDs) > 0 {
		return nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data) == 0 {
		return err
	}
	return json.Unmarshal(envelope.Data, out)
}

// finalizeParent builds the result map from the typed aggregate result
// and persists it via jobsSvc.FinalizeAggregateParent with version-based CAS.
// Mirrors voiceover/jobs/parent_aggregator.go::finalizeParent.
func (a *ScriptParentAggregator) finalizeParent(ctx context.Context, parentJobID string, agg ScriptAggregateResult) {
	resultMap := map[string]any{
		"parent_state":        string(agg.ParentState),
		"_aggregator_version": agg.ParentRevision,
		"total_items":         agg.TotalItems,
		"succeeded_count":     agg.SucceededCount,
		"failed_count":        agg.FailedCount,
		"child_job_ids":       agg.ChildIDs,
		"per_language":        agg.PerLanguage,
		"stage_progress":      agg.StageProgress,
	}
	if len(agg.ChildDocLinks) > 0 {
		resultMap["child_doc_links"] = agg.ChildDocLinks
	}
	if len(agg.ChildDocIDs) > 0 {
		resultMap["child_doc_ids"] = agg.ChildDocIDs
	}

	targetStatus := job.StatusSucceeded
	errMsg := ""
	if agg.ParentState == ScriptParentFailedTerminal {
		targetStatus = job.StatusFailed
		// Marker is canonical per P0 #4 aggregate-flip audit; the test
		// parent_aggregator_test.go::TestFinalizeAggregateParent_AllFailed_PopulatesErrMsg
		// asserts that errMsg contains the literal
		// "script aggregate: all child jobs definitively failed" prefix
		// for audit forensics (the test extracts prefix via substring
		// match — keeps the (P0 #4 narrow-port discipline) suffix free
		// to evolve without breaking the contract).
		errMsg = "script aggregate: all child jobs definitively failed (P0 #4 narrow-port discipline)"
	}

	if err := a.deps.JobsSvc.FinalizeAggregateParent(ctx, parentJobID, targetStatus, resultMap, errMsg, agg.ParentRevision); err != nil {
		if errors.Is(err, domainremote.ErrAlreadyTerminalAggregate) {
			a.deps.Logger.Info("ScriptParentAggregator: parent already finalised (replay no-op)",
				zap.String("parent_job_id", parentJobID),
				zap.String("parent_state", string(agg.ParentState)))
			return
		}
		if errors.Is(err, domainremote.ErrAggregateCASConflict) {
			a.deps.Logger.Warn("ScriptParentAggregator: FinalizeAggregateParent CAS conflict (concurrent tick)",
				zap.String("parent_job_id", parentJobID))
			return
		}
		a.deps.Logger.Warn("ScriptParentAggregator: FinalizeAggregateParent failed",
			zap.String("parent_job_id", parentJobID),
			zap.String("target_status", string(targetStatus)),
			zap.Error(err))
		return
	}
	a.deps.Logger.Info("ScriptParentAggregator: parent state transition",
		zap.String("parent_job_id", parentJobID),
		zap.String("parent_state", string(agg.ParentState)),
		zap.String("target_status", string(targetStatus)),
		zap.Int("revision", agg.ParentRevision))
	if a.deps.RunRepo != nil {
		if targetStatus == job.StatusFailed {
			_ = a.deps.RunRepo.FailRun(ctx, scriptgen.FailRunInput{
				RunID:        runIDForJob(ctx, a.deps.RunRepo, parentJobID),
				FailedStage:  scriptgen.StageFailed,
				ErrorCode:    "SCRIPT_BATCH_FAILED",
				ErrorMessage: errMsg,
			})
		} else if run, lookupErr := a.deps.RunRepo.GetByJobID(ctx, parentJobID); lookupErr == nil && run != nil {
			_ = a.deps.RunRepo.UpdateStage(ctx, run.ID, scriptgen.RunStatusCompleted, scriptgen.StageCompleted)
		}
	}
}

func runIDForJob(ctx context.Context, repo scriptgen.RunRepository, jobID string) string {
	if run, err := repo.GetByJobID(ctx, jobID); err == nil && run != nil {
		return run.ID
	}
	return ""
}

// scriptItemP0_1Gate overrides a child to FAILED when broker status
// is SUCCEEDED but result.ok=false (false-success gate).
func scriptItemP0_1Gate(childJSON []byte) bool {
	if len(childJSON) == 0 {
		return false
	}
	var cr ScriptChildResult
	if err := json.Unmarshal(childJSON, &cr); err != nil {
		return false
	}
	// Aggregate reads ok as *bool to mirror voiceover's explicit-field
	// discipline; present-and-false → override; absent or true → no override.
	if cr.OK == nil {
		return false
	}
	return !*cr.OK
}
