// Package voiceover — parent_state.go (PR-VO-AUDIT-P05 micro-commit #4 of
// 8, June 2026).
//
// Closes audit P0.5 part 1: parent's application-level state
// reflects REAL child completion posture (not enqueue success).
// Pre-P05 the parent's HandleJob returned (resultMap, nil) after
// fan-out completed enqueueing child jobs, so the dispatcher marked
// the parent SUCCEEDED + terminal even though no child had yet
// reached a terminal state. If a child then FAILED in dispatch,
// the parent's recorded status was falsely SUCCEEDED — the audit's
// third "false success" surface.
//
// P0.5 MVP (micro-commit #4, this scope):
//
//  1. Type ParentState + 4 typed constants emitted into the parent
//     job's result map under key "parent_state". The dispatcher still
//     marks the parent SUCCEEDED when (resultMap, nil) is returned,
//     but the result-map CARRIES the application-level state so
//     (a) operators / APIs reading job.Result see the real posture,
//     (b) the micro-commit #5 aggregator can read parent_state back
//     to compute the terminal re-finalisation (the durable child-
//     list correlation arrives in #5).
//
//  2. Type LanguageOutcome holds the per-language audit trail so
//     the #5 aggregator can dump the durably-fetched child list
//     into job.Result without losing fidelity.
//
//  3. AggregateChildOutcomes(children []LanguageOutcome) ParentState:
//     pure classifier — callable from both the immediate post-
//     enqueue emit (caller feeds a synthetic pending-only slice)
//     AND the future #5 aggregator (caller feeds a durable snapshot
//     read from the parent_job_id index).
//
// Behavioural contract pinned by tests (jobs/generate_handler_test.go):
//
//   - TestGenerateJobHandler_ParentEntersWaitingChildren:
//     HandleJob succeeds → result["parent_state"] == "waiting_children".
//   - TestAggregateChildOutcomes_NoChildrenCompleteIsPartial:
//     AggregateChildOutcomes(nil) or empty slice → "partial_success".
//   - TestGenerateJobHandler_DoesNotMarkSucceededBeforeChildrenTerminal:
//     Post-enqueue parent_state is NEVER "succeeded" (the flip to
//     "succeeded" is deferred to micro-commit #5's aggregator path).
package voiceover

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ParentState is the typed parent-job application-level lifecycle
// enum. JSON consumers see the underlying string (lower-snake)
// under key "parent_state" in job.Result. The shape is intentionally
// narrow (4 values only) — the future outbox-driven aggregator
// (#5) emits ONE of these at terminal time and the close-to-enqueue
// emit uses the same value space.
type ParentState string

const (
	// ParentWaitingChildren: parent just enqueued children OR children
	// are still in flight. At the DISPATCHER level, the parent may
	// already be marked SUCCEEDED — but the application-level state in
	// job.Result["parent_state"] correctly reports "waiting_children".
	// The micro-commit #5 aggregator re-finalises on terminal.
	ParentWaitingChildren ParentState = "waiting_children"

	// ParentSucceeded: durable snapshot reflects ALL children terminal
	// + ALL succeeded. Computed by Aggregate (or future #5 reader).
	ParentSucceeded ParentState = "succeeded"

	// ParentPartialSuccess: durability-incomplete OR mixed terminal
	// outcome. Encodes BOTH (a) snapshot is incomplete (some children
	// still pending — terminal < total) AND (b) post-completion
	// mixed succeeded+failed. The "no children complete is partial"
	// test name encodes branch (a) — empty input → ParentPartialSuccess.
	ParentPartialSuccess ParentState = "partial_success"

	// ParentFailed: durable snapshot reflects ALL children terminal
	// + ALL failed or cancelled.
	ParentFailed ParentState = "failed"
)

// LanguageOutcome carries the per-language audit trail surfaced in
// job.Result["per_language"] and re-hydrated by future callers.
// In micro-commit #4 the field set is full but no caller populates
// it durably yet — the #5 BACKFILL step wires the parent_job_id
// index on jobs (or equivalent correlation store).
//
// Language is typed (voiceover.Language) per PR-VO-TYPED-PRIMITIVES —
// JSON wire shape is byte-equivalent with the pre-refactor string
// field (typed string serialises as the underlying string).
type LanguageOutcome struct {
	Language   Language   `json:"language"`
	ChildJobID string     `json:"child_job_id"`
	Status     job.Status `json:"status"`
	Error      string     `json:"error,omitempty"`
}

// SnapshotAccessor is the narrow port to the durable parent→child
// correlation store. Implementations return the per-language
// LanguageOutcome slice for a given parentID. The aggregator
// driver in micro-commit #5 will implement this against a
// parent_job_id index on jobs; micro-commit #4 callers feed a
// synthetic in-memory accessor constructed from FanoutResult.
type SnapshotAccessor interface {
	Snapshot(parentID string) []LanguageOutcome
}

// FanoutResultSnapshotAccessor is the canonical implementation of
// SnapshotAccessor used in micro-commit #4. It builds a pending-only
// LanguageOutcome slice from the child IDs that the fan-out enqueued
// for a parent. Micro-commit #5 swaps it for a broker-reading
// accessor without changing AggregateByParent's signature or the
// audit-pinned test surface.
type FanoutResultSnapshotAccessor struct {
	// ChildrenByParent maps parentID → child job IDs (all StatusQueued
	// at post-enqueue; aggregator re-classifies on terminal).
	ChildrenByParent map[string][]string
}

// Snapshot returns the pending-only slice for the given parent,
// empty if the parent has no recorded children. The StatusQueued
// value drives AggregateChildOutcomes into the "terminal < total"
// branch (ParentPartialSuccess — correctly reflects "no children
// complete yet"). Micro-commit #5's real accessor will substitute
// each child's broker-fetched terminal status.
func (a *FanoutResultSnapshotAccessor) Snapshot(parentID string) []LanguageOutcome {
	childIDs := a.ChildrenByParent[parentID]
	out := make([]LanguageOutcome, 0, len(childIDs))
	for _, id := range childIDs {
		out = append(out, LanguageOutcome{
			ChildJobID: id,
			Status:     job.StatusQueued,
		})
	}
	return out
}

// AggregateByParent satisfies the user-spec contract: takes a
// parentID (+ injected SnapshotAccessor) and returns the typed
// ParentState. This is the wrapper callers use in production code;
// the pure AggregateChildOutcomes (below) is the corpus of the
// classifier, callable directly from unit tests on hand-built
// snapshots.
//
// MANDATORY accessor (fail-fast): a nil accessor is a wiring
// bug — it means the caller forgot to inject the parent_job_id
// snapshot layer. Panic unconditionally instead of returning a
// silent ParentPartialSuccess placeholder (the latter would mask
// the bug in production). Per AGENTS.md WireUp pattern: failures
// at the wire surface should be loud, not silent.
func AggregateByParent(parentID string, accessor SnapshotAccessor) ParentState {
	if accessor == nil {
		panic("voiceover.AggregateByParent: SnapshotAccessor is required (parentID=" + parentID + ")")
	}
	return AggregateChildOutcomes(accessor.Snapshot(parentID))
}

// AggregateChildOutcomes computes the canonical ParentState from a
// snapshot of children's per-language terminal-or-pending posture.
// Pure function — no broker dependency, no side-effects. Test
// contents feed their own snapshot directly.
//
// Semantics (P0.5 part 1 MVP — the durable child-list relationship
// lands in micro-commit #5; this function is pure and caller-fed):
//
//   - len(children) == 0 → ParentPartialSuccess ("no children
//     complete is partial" — branch pinned by
//     TestAggregateChildOutcomes_NoChildrenCompleteIsPartial).
//     This encodes the "durable aggregator hasn't picked up the
//     parent yet" placeholder, in addition to the canonical
//     "in-progress snapshot" branch below.
//
//   - Some children pending (terminal < total) → ParentPartialSuccess
//     (snapshot is incomplete; aggregator won't commit to a terminal
//     judgment until ALL children are terminal).
//
//   - All children terminal:
//
//   - failed == 0 → ParentSucceeded
//
//   - succeeded == 0 → ParentFailed
//
//   - succeeded > 0 && failed > 0 → ParentPartialSuccess
//
// Callers in micro-commit #4 (HandleJob) feed a synthetic pending-
// only slice; callers in micro-commit #5 (aggregator tick driver)
// feed a durable snapshot read from the parent_job_id query.
func AggregateChildOutcomes(children []LanguageOutcome) ParentState {
	if len(children) == 0 {
		// Pinned by TestAggregateChildOutcomes_NoChildrenCompleteIsPartial:
		// no terminal children visible → we cannot commit to a
		// homogeneous terminal state, so report partial.
		return ParentPartialSuccess
	}
	terminal, succeeded, failed := 0, 0, 0
	for _, c := range children {
		switch c.Status {
		case job.StatusSucceeded:
			terminal++
			succeeded++
		case job.StatusFailed, job.StatusCancelled:
			terminal++
			failed++
		}
	}

	if terminal < len(children) {
		// Snapshot is incomplete — children still in flight.
		return ParentPartialSuccess
	}
	if failed == 0 {
		return ParentSucceeded
	}
	if succeeded == 0 {
		return ParentFailed
	}
	return ParentPartialSuccess
}
