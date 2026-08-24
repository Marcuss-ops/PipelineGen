// Package jobs — parent_aggregator_eligibility_test.go
// (PR-SPLIT-VO-PARENT-AGG-TESTS, July 2026).
//
// ELIGIBILITY test surface (mirror of parent_eligibility.go
// production split). Per godlike/06 SSOT (one canonical owner per
// fact), this file is the SOLE canonical owner of the eligibility-
// gate + cache + zero-children-short-circuit test surface.
//
// The eligibility gate is the canonical decision: which parent
// jobs should be processed by the aggregator's Tick? The
// IsParentAwaitingAggregation function in parent_eligibility.go owns
// the gate logic; this test file pins the 3 terminal-state branches:
//
//   - parent_state=waiting_children  → aggregator processes
//     (FinalizeAggregateParent is called for awaiting-aggregation parents)
//
//   - parent_state=succeeded         → aggregator SKIPS
//     (re-finalising an already-terminal parent would corrupt the
//     canonical aggregate per audit-P0 #1 closure contract)
//
//   - parent_state=cancelled         → aggregator SKIPS
//     (mirrors the audit-P0 #1 'failed'-state contract)
//
// godlike/07 minimum-blast-radius: pure code-motion — the test
// was previously nested at the bottom of finalizer_invariants_test.go
// alongside 3 unrelated tests of the voiceover finalizer contracts.
// The production split moved IsParentAwaitingAggregation to
// parent_eligibility.go; this file mirror the same split so the test
// is reachable via same-package visibility (no extra wiring).
//
// Migration note: the prior test was named
// TestParentAggregator_TriggeredOnlyAfterWaitingChildren (introduced by
// commit e71d10ad, 2026-07-04). We retain the original name AND
// add a mirror TestCancelParent_AggregatorSkips that documents
// the canonical Step 5 entry-point for the cancel-state branch
// (godlike/06 SSOT documentation discipline).
package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────
// IsParentAwaitingAggregation gate (parent_eligibility.go):
// 3 sub-cases that pin the canonical aggregator-trigger contract.
// ─────────────────────────────────────────────────────────────────

// TestParentEligibility_TriggeredOnlyAfterWaitingChildren pins the
// IsParentAwaitingAggregation gate of internal/application/voiceover/jobs/
// parent_aggregator.go::aggregateOne (gate re-exported from
// parent_eligibility.go post-split). The background aggregator MUST
// process only parents in waiting_children (or partial_success) state;
// parents in terminal states (succeeded, failed, cancelled) MUST be
// skipped — re-finalising an already-terminal parent would corrupt
// the canonical aggregate (the audit-P0 #1 closure contract).
//
// 3 sub-cases (table-driven):
//
//	A. parent_state=waiting_children  → aggregator processes (asserts FinalizeAggregateParent called)
//	B. parent_state=succeeded         → aggregator SKIPS (asserts FinalizeAggregateParent NOT called)
//	C. parent_state=cancelled         → aggregator SKIPS (asserts FinalizeAggregateParent NOT called)
//
// Originally authored at commit e71d10ad (2026-07-04) inside
// finalizer_invariants_test.go. Moved here by Step 5 of the
// PR-SPLIT-VO-PARENT-AGG-TESTS test-surface decomposition to
// mirror the production split of parent_aggregator.go →
// parent_eligibility.go (the gate's canonical owner post-split).
func TestParentEligibility_TriggeredOnlyAfterWaitingChildren(t *testing.T) {
	// Shared child job — all 3 sub-cases use the same SUCCEEDED child
	// shape. The aggregator's per-child logic is identical across the
	// sub-cases; what varies is the parent's parent_state.
	childSucceeded := func(id string) *job.Job {
		return &job.Job{
			ID:     id,
			Type:   job.TypeVoiceoverGenerateItem,
			Status: job.StatusSucceeded,
			Result: []byte(`{"ok":true,"status":"completed"}`),
		}
	}

	t.Run("A. waiting_children → aggregator processes", func(t *testing.T) {
		// makeParentResult (defined in parent_aggregator_testhelpers_test.go)
		// sets parent_state="waiting_children" by default.
		stub := &stubAggregatorJobsService{
			parentJob: &job.Job{
				ID:     "parent-waiting",
				Type:   job.TypeVoiceoverGenerate,
				Status: job.StatusSucceeded,
				Result: makeParentResult([]string{"child-w1"}),
			},
			childJobs: map[string]*job.Job{
				"child-w1": childSucceeded("child-w1"),
			},
		}
		agg := NewParentAggregator(AggregatorDeps{
			JobsSvc:      stub,
			Logger:       zap.NewNop(),
			PollInterval: 30 * time.Second,
		})
		agg.Tick(context.Background())

		// Canonical assertion: aggregator's FinalizeAggregateParent
		// (the typed post-fan-out finalise path) MUST have been invoked.
		assert.NotEmpty(t, stub.flipped,
			"Case A: aggregator MUST invoke FinalizeAggregateParent for parents in waiting_children state (per IsParentAwaitingAggregation gate)")
	})

	t.Run("B. succeeded → aggregator skips", func(t *testing.T) {
		// Build parent result with parent_state="succeeded" (terminal).
		terminalResult := map[string]any{
			"ok":            true,
			"parent_job_id": "parent-succeeded",
			"parent_state":  "succeeded",
			"child_job_ids": []string{"child-s1"},
		}
		terminalRaw, _ := json.Marshal(terminalResult)
		stub := &stubAggregatorJobsService{
			parentJob: &job.Job{
				ID:     "parent-succeeded",
				Type:   job.TypeVoiceoverGenerate,
				Status: job.StatusSucceeded,
				Result: terminalRaw,
			},
			childJobs: map[string]*job.Job{
				"child-s1": childSucceeded("child-s1"),
			},
		}
		agg := NewParentAggregator(AggregatorDeps{
			JobsSvc:      stub,
			Logger:       zap.NewNop(),
			PollInterval: 30 * time.Second,
		})
		agg.Tick(context.Background())

		assert.Empty(t, stub.flipped,
			"Case B: aggregator MUST NOT re-finalise parents in terminal 'succeeded' state (would corrupt aggregate per audit-P0 #1 contract)")
	})

	t.Run("C. cancelled → aggregator skips", func(t *testing.T) {
		// Build parent result with parent_state="cancelled" (terminal).
		// godlike/06 SSOT documentation pin: this sub-case test name
		// was originally TestAcceptance_CancelParent_AggregatorSkips
		// (per the audit-P0 #1 closure test header in the prior
		// source location). The new eligibility mirror preserves the
		// "cancelled → aggregator skips" invariant under the dedicated
		// ownership of this file.
		cancelledResult := map[string]any{
			"ok":            false,
			"parent_job_id": "parent-cancelled",
			"parent_state":  "cancelled",
			"child_job_ids": []string{"child-c1"},
		}
		cancelledRaw, _ := json.Marshal(cancelledResult)
		stub := &stubAggregatorJobsService{
			parentJob: &job.Job{
				ID:     "parent-cancelled",
				Type:   job.TypeVoiceoverGenerate,
				Status: job.StatusCancelled,
				Result: cancelledRaw,
			},
			childJobs: map[string]*job.Job{
				"child-c1": childSucceeded("child-c1"),
			},
		}
		agg := NewParentAggregator(AggregatorDeps{
			JobsSvc:      stub,
			Logger:       zap.NewNop(),
			PollInterval: 30 * time.Second,
		})
		agg.Tick(context.Background())

		assert.Empty(t, stub.flipped,
			"Case C: aggregator MUST NOT re-finalise parents in terminal 'cancelled' state (mirrors the audit-P0 #1 'failed'-state contract)")
	})
}
