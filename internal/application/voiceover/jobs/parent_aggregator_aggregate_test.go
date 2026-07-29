// Package jobs — parent_aggregator_aggregate_test.go
// (PR-SPLIT-VO-PARENT-AGG-TESTS, July 2026).
//
// AGGREGATE test surface (mirror of parent_aggregator_aggregate.go
// production split). Per godlike/06 SSOT (one canonical owner per
// fact), this file is the SOLE canonical owner of the 5 Test funcs
// that exercise the per-parent aggregateOne pipeline via the public
// Tick API:
//
//   - 4 P0.1 false-success-gate tests (P0.1, July 2026 audit):
//
//   - TestParentDoesNotSucceedWhenChildResultIsFailed
//
//   - TestParentSucceedsWhenChildResultIsOK
//
//   - TestParentPartialSuccessWhenOneChildResultFailed
//
//   - TestParentHandlesChildResultWithoutOKField
//
//   - 1 ACCEPTANCE test from §15.4 (EmptyChildIDsFiltered):
//
//   - TestAcceptance_EmptyChildIDsFiltered
//
// All test funcs target the AGGREGATE production code only
// (parent_aggregator_aggregate.go::aggregateOne). They share the
// stubAggregatorJobsService + factory helpers + flipRecord from
// parent_aggregator_testhelpers_test.go via same-package visibility.
// godlike/07 minimum-blast-radius: pure code-motion, no logic change.
package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────
// P0.1 Audit Test: child broker-succeeded but result.ok=false
// ─────────────────────────────────────────────────────────────────

// TestParentDoesNotSucceedWhenChildResultIsFailed pins the P0.1
// false-success gate at the parent-aggregator boundary. A child
// whose broker status is SUCCEEDED but whose result JSON has
// ok=false (per-item pipeline failure) MUST be treated as FAILED
// by the aggregator. The parent_state MUST NOT be "succeeded".
func TestParentDoesNotSucceedWhenChildResultIsFailed(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-1",
			Type:   job.TypeVoiceoverGenerate,
			Result: makeParentResult([]string{"child-1"}),
		},
		childJobs: map[string]*job.Job{
			"child-1": {
				ID:     "child-1",
				Type:   job.TypeVoiceoverGenerateItem,
				Status: job.StatusSucceeded, // broker says SUCCEEDED
				Result: makeChildResult(false, "failed", "tts_failed: Edge TTS connection timeout"),
			},
		},
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	// The aggregator must have called Complete on the parent.
	require.Contains(t, stub.completed, "parent-1",
		"P0.1: aggregator must call Complete on the parent after all children terminal")

	parentState, _ := stub.completed["parent-1"]["parent_state"].(string)
	assert.NotEqual(t, "succeeded", parentState,
		"P0.1: parent_state MUST NOT be 'succeeded' when child result.ok=false (false-success gate)")
	assert.Equal(t, "failed", parentState,
		"P0.1: single child with ok=false → parent_state must be 'failed' (all children failed)")
}

// ─────────────────────────────────────────────────────────────────
// P0.1 complementary: child broker-succeeded + result.ok=true
// ─────────────────────────────────────────────────────────────────

// TestParentSucceedsWhenChildResultIsOK pins the complementary
// contract: a child that is genuinely successful (broker SUCCEEDED
// + result.ok=true) must still produce parent_state="succeeded".
// The P0.1 gate must NOT false-positive on legitimate successes.
func TestParentSucceedsWhenChildResultIsOK(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-2",
			Type:   job.TypeVoiceoverGenerate,
			Result: makeParentResult([]string{"child-2"}),
		},
		childJobs: map[string]*job.Job{
			"child-2": {
				ID:     "child-2",
				Type:   job.TypeVoiceoverGenerateItem,
				Status: job.StatusSucceeded,
				Result: makeChildResult(true, "completed", ""),
			},
		},
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	require.Contains(t, stub.completed, "parent-2",
		"P0.1 gate: aggregator must call Complete on genuinely succeeded parent")

	parentState, _ := stub.completed["parent-2"]["parent_state"].(string)
	assert.Equal(t, "succeeded", parentState,
		"P0.1 gate: parent_state must be 'succeeded' when child broker-succeeded AND result.ok=true (no false-positive)")
}

// ─────────────────────────────────────────────────────────────────
// P0.1 mixed: one child ok=false, one child ok=true
// ─────────────────────────────────────────────────────────────────

// TestParentPartialSuccessWhenOneChildResultFailed pins the
// mixed-outcome branch: two children, one genuinely succeeded
// (ok=true), one with ok=false. The parent_state must be
// "partial_success".
func TestParentPartialSuccessWhenOneChildResultFailed(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-3",
			Type:   job.TypeVoiceoverGenerate,
			Result: makeParentResult([]string{"child-ok", "child-fail"}),
		},
		childJobs: map[string]*job.Job{
			"child-ok": {
				ID:     "child-ok",
				Type:   job.TypeVoiceoverGenerateItem,
				Status: job.StatusSucceeded,
				Result: makeChildResult(true, "completed", ""),
			},
			"child-fail": {
				ID:     "child-fail",
				Type:   job.TypeVoiceoverGenerateItem,
				Status: job.StatusSucceeded, // broker says SUCCEEDED but...
				Result: makeChildResult(false, "failed", "upload_failed: Drive timeout"),
			},
		},
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	require.Contains(t, stub.completed, "parent-3",
		"P0.1: aggregator must call Complete on mixed-outcome parent")

	parentState, _ := stub.completed["parent-3"]["parent_state"].(string)
	assert.Equal(t, "partial_success", parentState,
		"P0.1: one child succeeded + one child ok=false → parent_state must be 'partial_success' (mixed outcome)")
}

// ─────────────────────────────────────────────────────────────────
// P0.1 edge case: child result JSON without ok field
// ─────────────────────────────────────────────────────────────────

// TestParentHandlesChildResultWithoutOKField pins the defense-in-depth
// path: a child result JSON that does NOT have an "ok" field (legacy
// shape, pre-toItemResultMap) must NOT crash the aggregator. The
// status falls back to the broker value.
func TestParentHandlesChildResultWithoutOKField(t *testing.T) {
	// Build result JSON without the "ok" key — simulates a pre-P0.1
	// result shape or a malformed result.
	legacyResult := map[string]any{
		"status":   "completed",
		"language": "en",
		"job_id":   "child-legacy",
	}
	legacyRaw, _ := json.Marshal(legacyResult)

	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-4",
			Type:   job.TypeVoiceoverGenerate,
			Result: makeParentResult([]string{"child-legacy"}),
		},
		childJobs: map[string]*job.Job{
			"child-legacy": {
				ID:     "child-legacy",
				Type:   job.TypeVoiceoverGenerateItem,
				Status: job.StatusSucceeded,
				Result: legacyRaw,
			},
		},
	}

	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc:      stub,
		Logger:       zap.NewNop(),
		PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	require.Contains(t, stub.completed, "parent-4",
		"P0.1 edge: aggregator must call Complete even on legacy result shape")

	parentState, _ := stub.completed["parent-4"]["parent_state"].(string)
	assert.Equal(t, "succeeded", parentState,
		"P0.1 edge: missing 'ok' field → fall back to broker status (succeeded)")
}

// ─────────────────────────────────────────────────────────────────
// §15.4 Empty-string child ID filtering: 3rd child enqueue failed,
// its slot in child_job_ids is an empty string. The aggregator must
// filter it out and only process the 2 real children.
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_EmptyChildIDsFiltered(t *testing.T) {
	// Fan-out requested 3 children but only 2 were enqueued.
	// ChildJobIDs = ["c-it", "c-en", ""] — empty string for the failed enqueue.
	// The aggregator must filter the empty string and only process 2 children.
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-partial",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it", "c-en", ""}, 3, 0, ""),
		},
		childJobs: map[string]*job.Job{
			"c-it": {ID: "c-it", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-en": {ID: "c-en", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-partial",
		"§15.4: aggregator must finalise parent even with partial fan-out")
	got := stub.flipped["parent-partial"]
	assert.Equal(t, "succeeded", got.result["parent_state"],
		"§15.4: 2 enqueued children both succeeded → parent_state must be 'succeeded'")
	assert.Equal(t, 2, got.result["total_children"],
		"§15.4: total_children must be 2 (empty string filtered out)")
}
