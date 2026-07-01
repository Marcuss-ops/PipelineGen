// Package jobs — parent_aggregator_test.go (P0.1 false-success gate, July 2026).
//
// Tests for the P0.1 gate in ParentAggregator.aggregateOne: when a child
// job's broker status says SUCCEEDED but its result JSON has ok=false,
// the aggregator must treat the child as FAILED and compute the parent
// state accordingly.
//
// Test strategy: drive the public Tick API with a stub AggregatorJobsService
// that returns pre-configured parent and child jobs. Tick calls List → Get
// → Complete on the stub; assertions read the stub's completed map.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stubAggregatorJobsService satisfies AggregatorJobsService with an
// in-memory job store so tests can inject any job shape without a DB.
type stubAggregatorJobsService struct {
	parentJob  *job.Job
	childJobs  map[string]*job.Job // childID → *job.Job
	completed  map[string]map[string]any
	listErr    error
	getErr     error
}

func (s *stubAggregatorJobsService) List(ctx context.Context, filter job.Filter) ([]job.Job, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.parentJob == nil {
		return nil, nil
	}
	return []job.Job{*s.parentJob}, nil
}

func (s *stubAggregatorJobsService) Get(ctx context.Context, id string) (*job.Job, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	j, ok := s.childJobs[id]
	if !ok {
		return nil, fmt.Errorf("stub: child not found: %s", id)
	}
	return j, nil
}

func (s *stubAggregatorJobsService) Complete(ctx context.Context, id string, result map[string]any) error {
	if s.completed == nil {
		s.completed = make(map[string]map[string]any)
	}
	s.completed[id] = result
	return nil
}

// Compile-time assertion: stub satisfies the port.
var _ AggregatorJobsService = (*stubAggregatorJobsService)(nil)

// makeParentResult builds the parent job's result JSON that
// FanoutVoiceoversUseCase writes (via toFanoutResultMap).
func makeParentResult(childIDs []string) []byte {
	m := map[string]any{
		"ok":            true,
		"parent_job_id": "parent-1",
		"request_id":    "vo_test",
		"total_outputs": len(childIDs),
		"enqueued_count": len(childIDs),
		"child_job_ids": childIDs,
		"parent_state":  string(voiceover.ParentWaitingChildren),
	}
	raw, _ := json.Marshal(m)
	return raw
}

// makeChildResult builds a child job's result JSON that
// GenerateItemJobHandler writes (via toItemResultMap).
// ok=false simulates a per-item pipeline failure (StatusFailed).
func makeChildResult(ok bool, status string, errStr string) []byte {
	m := map[string]any{
		"ok":          ok,
		"status":      status,
		"language":    "en",
		"job_id":      "child-1",
		"parent_job_id": "parent-1",
		"request_id":  "vo_test",
	}
	if errStr != "" {
		m["error"] = errStr
	}
	raw, _ := json.Marshal(m)
	return raw
}

// ────────────────────────────────────────────────────────────────────
// P0.1 Audit Test: child broker-succeeded but result.ok=false
// ────────────────────────────────────────────────────────────────────

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

// ────────────────────────────────────────────────────────────────────
// P0.1 complementary: child broker-succeeded + result.ok=true
// ────────────────────────────────────────────────────────────────────

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

// ────────────────────────────────────────────────────────────────────
// P0.1 mixed: one child ok=false, one child ok=true
// ────────────────────────────────────────────────────────────────────

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

// ────────────────────────────────────────────────────────────────────
// P0.1 edge case: child result JSON without ok field
// ────────────────────────────────────────────────────────────────────

// TestParentHandlesChildResultWithoutOKField pins the defense-in-depth
// path: a child result JSON that does NOT have an "ok" field (legacy
// shape, pre-toItemResultMap) must NOT crash the aggregator. The
// status falls back to the broker value.
func TestParentHandlesChildResultWithoutOKField(t *testing.T) {
	// Build result JSON without the "ok" key — simulates a pre-P0.1
	// result shape or a malformed result.
	legacyResult := map[string]any{
		"status":    "completed",
		"language":  "en",
		"job_id":    "child-legacy",
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
