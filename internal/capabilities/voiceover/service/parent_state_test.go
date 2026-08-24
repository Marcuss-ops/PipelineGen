package voiceover

// PR-VO-AUDIT-P05 micro-commit #4 (June 2026) — pure classifier tests.
//
// AggregateChildOutcomes is a pure function (no broker dependency, no
// side-effects). All branches are pinned here so the micro-commit #5
// aggregator can compose the same function over a durable snapshot
// without re-verifying the classification semantics.

import (
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/stretchr/testify/assert"
)

// Audit-pinned: empty input → ParentPartialSuccess.
//
// "No children complete is partial" — the canonical placeholder for
// "the aggregator has nothing countable yet". Encodes both (a) the
// durable parent_job_id query returned no rows, and (b) the snapshot
// is genuinely empty (parent never enqueued).
func TestAggregateChildOutcomes_NoChildrenCompleteIsPartial(t *testing.T) {
	t.Run("nil_slice", func(t *testing.T) {
		got := AggregateChildOutcomes(nil)
		assert.Equal(t, ParentPartialSuccess, got,
			"P0.5: nil children slice must produce ParentPartialSuccess (no children complete is partial)")
	})
	t.Run("empty_slice", func(t *testing.T) {
		got := AggregateChildOutcomes([]LanguageOutcome{})
		assert.Equal(t, ParentPartialSuccess, got,
			"P0.5: empty children slice must produce ParentPartialSuccess (no children complete is partial)")
	})
}

// All children terminal + all succeeded → ParentSucceeded.
func TestAggregateChildOutcomes_AllTerminalSucceededReturnsSucceeded(t *testing.T) {
	children := []LanguageOutcome{
		{Language: "en", ChildJobID: "child-en", Status: job.StatusSucceeded},
		{Language: "it", ChildJobID: "child-it", Status: job.StatusSucceeded},
		{Language: "fr", ChildJobID: "child-fr", Status: job.StatusSucceeded},
	}
	got := AggregateChildOutcomes(children)
	assert.Equal(t, ParentSucceeded, got,
		"P0.5: all children StatusSucceeded must produce ParentSucceeded")
}

// All children terminal + all failed/cancelled → ParentFailed.
func TestAggregateChildOutcomes_AllTerminalFailedReturnsFailed(t *testing.T) {
	children := []LanguageOutcome{
		{Language: "en", ChildJobID: "child-en", Status: job.StatusFailed, Error: "tts outage"},
		{Language: "it", ChildJobID: "child-it", Status: job.StatusCancelled},
	}
	got := AggregateChildOutcomes(children)
	assert.Equal(t, ParentFailed, got,
		"P0.5: all children StatusFailed/StatusCancelled must produce ParentFailed")
}

// Mixed terminal (some succeeded, some failed) → ParentPartialSuccess.
func TestAggregateChildOutcomes_MixedTerminalReturnsPartialSuccess(t *testing.T) {
	children := []LanguageOutcome{
		{Language: "en", ChildJobID: "child-en", Status: job.StatusSucceeded},
		{Language: "it", ChildJobID: "child-it", Status: job.StatusFailed},
	}
	got := AggregateChildOutcomes(children)
	assert.Equal(t, ParentPartialSuccess, got,
		"P0.5: mixed StatusSucceeded/StatusFailed must produce ParentPartialSuccess")
}

// Snapshot is incomplete — some terminal, some still StatusQueued →
// ParentPartialSuccess. This is the "in-progress snapshot" branch;
// the aggregator waits for ALL children to reach terminal before
// committing to a homogeneous state.
func TestAggregateChildOutcomes_PendingInSnapshotReturnsPartialSuccess(t *testing.T) {
	children := []LanguageOutcome{
		{Language: "en", ChildJobID: "child-en", Status: job.StatusSucceeded},
		{Language: "it", ChildJobID: "child-it", Status: job.StatusQueued},
	}
	got := AggregateChildOutcomes(children)
	assert.Equal(t, ParentPartialSuccess, got,
		"P0.5: snapshot with terminal < total must produce ParentPartialSuccess")
}

// Single-child happy path (smallest realistic snapshot) pins the
// edge case where len(children)==1 and StatusSucceeded → ParentSucceeded.
func TestAggregateChildOutcomes_SingleChildSucceeded(t *testing.T) {
	got := AggregateChildOutcomes([]LanguageOutcome{
		{Language: "en", ChildJobID: "child-en", Status: job.StatusSucceeded},
	})
	assert.Equal(t, ParentSucceeded, got,
		"P0.5: single-child all-terminal-succeeded must produce ParentSucceeded")
}

// ── AggregateByParent + FanoutResultSnapshotAccessor wiring tests ────────
//
// Closes the prior review pass's MEDIUM #5: FanoutResultSnapshotAccessor
// was declared in parent_state.go as a forward-pointer for micro‑commit #5
// but had zero callers. The wire is now exercised end-to-end via
// AggregateByParent (the parentID-taking wrapper), locking the
// SnapshotAccessor port against signature drift at compile time.

// Audit-pinned: post-enqueue snapshot (all children StatusQueued) under
// AggregateByParent lands on ParentPartialSuccess ("terminal < total"
// branch). Pinning this branch surfaces the post-fanout real posture so
// the #5 aggregator can reason about it deterministically.
func TestAggregateByParent_PostEnqueue_AllPending_IsPartialSuccess(t *testing.T) {
	acc := &FanoutResultSnapshotAccessor{
		ChildrenByParent: map[string][]string{
			"p1": {"c1", "c2", "c3"},
		},
	}
	got := AggregateByParent("p1", acc)
	assert.Equal(t, ParentPartialSuccess, got,
		"P0.5 micro-commit #4 wiring pin: post-enqueue snapshot has all-StatusQueued children → terminal < total → partial_success")
}

// nil-accessor fail-fast: AggregateByParent MUST panic instead of
// silently returning a placeholder ParentPartialSuccess (masked wiring
// bugs in production). Per AGENTS.md WireUp pattern.
func TestAggregateByParent_NilAccessor_Panics(t *testing.T) {
	assert.Panics(t, func() {
		AggregateByParent("p1", nil)
	}, "P0.5: AggregateByParent with nil SnapshotAccessor MUST panic (fail-fast wiring bug, not silent placeholder)")
}

// Empty ChildrenByParent for the requested parentID → snapshot is nil
// slice → AggregateByParent returns ParentPartialSuccess ("no children
// complete is partial" — same branch as the pure classifier's empty input).
func TestAggregateByParent_EmptyChildrenForParent_IsPartialSuccess(t *testing.T) {
	acc := &FanoutResultSnapshotAccessor{
		ChildrenByParent: map[string][]string{}, // empty — no parent has any children
	}
	got := AggregateByParent("p1", acc)
	assert.Equal(t, ParentPartialSuccess, got,
		"P0.5: AggregateByParent with no-children-for-parentID snapshot → ParentPartialSuccess")
}

// Compile-time assertion: *FanoutResultSnapshotAccessor must satisfy
// the SnapshotAccessor port. Catches signature drift at build time.
var _ SnapshotAccessor = (*FanoutResultSnapshotAccessor)(nil)
