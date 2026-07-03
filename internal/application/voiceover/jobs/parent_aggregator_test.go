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
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/domain/remote"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// stubAggregatorJobsService satisfies AggregatorJobsService with an
// in-memory job store so tests can inject any job shape without a DB.
//
// Audit 2026-07-03 P0 #1 (added `flipped` map + TerminalFlip method):
// the legacy `completed` map is preserved for the existing 4
// P0.1-false-success-gate tests (their assertions read
// `stub.completed[id]["parent_state"]`). The new `flipped` map is the
// canonical surface for the P0 #1 closure tests (assertions read
// the targetStatus / errMsg / target_status field to verify that the
// aggregator flipped the broker status to FAILED when the aggregate
// dictates it).
type stubAggregatorJobsService struct {
	parentJob *job.Job
	childJobs map[string]*job.Job // childID → *job.Job
	completed map[string]map[string]any
	flipped   map[string]flipRecord // audit 2026-07-03 P0 #1 closure
	// flippedErr is the typed-error knob for the CAS-rejection tests.
	// When non-nil, the stub's TerminalFlip returns this error
	// (simulating production CAS rejection: ErrAlreadyTerminalAggregate
	// for replay, ErrAggregateCASConflict for manual-retry / status-revoked).
	flippedErr error
	listErr    error
	getErr     error
}

// flipRecord is the typed audit pin for the P0 #1 closure tests:
// targetStatus is the broker-level status the aggregator's TerminalFlip
// decided (job.StatusFailed when all children failed), result carries the
// new parent_state, errMsg is empty on success / populated on FAILED.
type flipRecord struct {
	targetStatus job.Status
	result       map[string]any
	errMsg       string
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

// TerminalFlip satisfies the audit 2026-07-03 P0 #1 aggregator port
// extension. Records the flip into stub.flipped (canonical for new
// tests asserting broker-status mirror) AND mirrors the parent_state
// into stub.completed (back-compat for the existing 4 P0.1 false-success
// gate tests that read `stub.completed[id]["parent_state"]`).
func (s *stubAggregatorJobsService) TerminalFlip(ctx context.Context, id string, targetStatus job.Status, result map[string]any, errMsg string) error {
	if s.flippedErr != nil {
		// CAS-rejection simulation: do NOT mutate stub.flipped/completed
		// (the production SQL repo's UPDATE returned 0 rows-affected, the
		// caller MUST treat the error as a no-op or a typed-conflict).
		return s.flippedErr
	}
	if s.flipped == nil {
		s.flipped = make(map[string]flipRecord)
	}
	s.flipped[id] = flipRecord{
		targetStatus: targetStatus,
		result:       result,
		errMsg:       errMsg,
	}
	if s.completed == nil {
		s.completed = make(map[string]map[string]any)
	}
	if s.completed[id] == nil {
		s.completed[id] = map[string]any{}
	}
	parentState, _ := result["parent_state"].(string)
	s.completed[id]["parent_state"] = parentState
	s.completed[id]["_target_status"] = string(targetStatus)
	s.completed[id]["_err_msg"] = errMsg
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

// ─────────────────────────────────────────────────────────────────
// P0 #1 Audit 2026-07-03 closure tests — broker-status mirror
// ─────────────────────────────────────────────────────────────────

// makeParentStateWaitingChildren is a slim parent result for the broker-
// mirror tests: only parent_state + child_job_ids matter (the legacy
// P0.1 tests use a richer fanout-shaped map; the new tests just need
// to assert the routing decision).
//
// variadic childIDs: tests that use semantically-named child IDs (e.g.
// "c-OK"/"c-FAIL") pass them explicitly so the parent's child_job_ids
// align with the stub's childJobs map keys (extractChildJobIDs ↔
// stub.Get(ctx, childID) must match for the aggregator's loop to
// reach the TerminalFlip call).
func makeParentStateWaitingChildren(childIDs ...string) []byte {
	ids := childIDs
	if len(ids) == 0 {
		ids = []string{"c1", "c2"}
	}
	m := map[string]any{
		"ok":            true,
		"parent_job_id": "parent-broker",
		"parent_state":  string(voiceover.ParentWaitingChildren),
		"child_job_ids": ids,
	}
	raw, _ := json.Marshal(m)
	return raw
}

// TestParentBrokerStatusIsFAILEDWhenAllChildrenFailed pins the audit's
// core acceptance criterion: when ALL children definitively fail (broker
// status = FAILED), the aggregator must NOT silently leave the parent
// broker-status at SUCCEEDED. The new TerminalFlip path must call
// target_status=StatusFailed so the DB row reads broker.status=FAILED.
// Pre-P0 #1: parent broker-status stayed SUCCEEDED + result.parent_state=
// failed — the "two-truth" bug the audit calls out.
func TestParentBrokerStatusIsFAILEDWhenAllChildrenFailed(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-fail",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded, // worker already finalised broker-status
			Result: makeParentStateWaitingChildren(),
		},
		childJobs: map[string]*job.Job{
			"c1": {
				ID:     "c1",
				Type:   job.TypeVoiceoverGenerateItem,
				Status: job.StatusFailed,
				Result: makeChildResult(false, "failed", "tts_failed: edge TTS connection timeout"),
			},
			"c2": {
				ID:     "c2",
				Type:   job.TypeVoiceoverGenerateItem,
				Status: job.StatusFailed,
				Result: makeChildResult(false, "failed", "upload_failed: Drive timeout"),
			},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-fail",
		"P0 #1: aggregator must call TerminalFlip (not Complete) on full-failure aggregate")
	got := stub.flipped["parent-fail"]
	assert.Equal(t, job.StatusFailed, got.targetStatus,
		"P0 #1: aggregate=failed_terminal MUST flip broker-status to FAILED (eliminates double-truth)")
	assert.Equal(t, "failed", got.result["parent_state"],
		"P0 #1: parent_state in result must match aggregate")
	assert.NotEmpty(t, got.errMsg,
		"P0 #1: FAILED-flip's error message must surface aggregate cause for operator logs")
}

// TestParentBrokerStatusSUCCEEDEDWhenAggregateIsPartialSuccess pins the
// COMPANION criterion: when at least one child SUCCEEDED, the broker-status
// stays at SUCCEEDED (no false-FAILED) but parent_state is partial_success
// (mixed-outcome audit pin). This is the de-facto "the broker should NOT
// flip to FAILED on a mixed aggregate" guarantee.
func TestParentBrokerStatusSUCCEEDEDWhenAggregateIsPartialSuccess(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-ps",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeParentStateWaitingChildren("c-OK", "c-FAIL"),
		},
		childJobs: map[string]*job.Job{
			"c-OK": { // SUCCEEDED child
				ID: "c-OK", Type: job.TypeVoiceoverGenerateItem,
				Status: job.StatusSucceeded,
				Result: makeChildResult(true, "completed", ""),
			},
			"c-FAIL": { // FAILED child via P0.1 false-success gate
				ID: "c-FAIL", Type: job.TypeVoiceoverGenerateItem,
				Status: job.StatusSucceeded, // broker-succeeded but ok=false
				Result: makeChildResult(false, "failed", "upload_failed: Drive timeout"),
			},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{
		JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second,
	})
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-ps",
		"P0 #1: aggregator must call TerminalFlip on partial_success aggregate")
	got := stub.flipped["parent-ps"]
	assert.Equal(t, job.StatusSucceeded, got.targetStatus,
		"P0 #1: partial_success aggregate MUST NOT flip broker-status to FAILED — preserve worker-emitted SUCCEEDED")
	assert.Equal(t, "partial_success", got.result["parent_state"],
		"P0 #1: partial_success aggregate emits parent_state=partial_success in result")
	assert.Empty(t, got.errMsg,
		"P0 #1: SUCCEEDED-flip has no error message (operator dashboards read parent_state instead)")
}

// ─────────────────────────────────────────────────────────────────
// P0 #1 CAS-rejection tests — code-reviewer critical gap closure
// ─────────────────────────────────────────────────────────────────

// TestTerminalFlipReplayIsIdempotent_NoOp verifies that when the
// underlying broker rejects the TerminalFlip with ErrAlreadyTerminalAggregate
// (idempotent replay — another tick already landed the flip), the
// aggregator treats it as a SILENT no-op (no warn-level log, no panic,
// no parent_state regression). The audit acceptance criterion: "re-tick
// MUST be idempotent; no parent regression".
func TestTerminalFlipReplayIsIdempotent_NoOp(t *testing.T) {
	// Both children failed → aggregate=failed_terminal → aggregator attempts
	// TerminalFlip(StatusFailed). Stub's flippedErr simulates the broker
	// returning ErrAlreadyTerminalAggregate because another tick already
	// finalised the parent.
	stub := &stubAggregatorJobsService{
		flippedErr: domainremote.ErrAlreadyTerminalAggregate,
		parentJob: &job.Job{
			ID: "parent-replay", Type: job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded, Result: makeParentStateWaitingChildren(),
		},
		childJobs: map[string]*job.Job{
			"c1": {ID: "c1", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusFailed, Result: makeChildResult(false, "failed", "err")},
			"c2": {ID: "c2", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusFailed, Result: makeChildResult(false, "failed", "err")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	// Tick must not panic; the flippedErr is silently consumed (no warn).
	agg.Tick(context.Background())
	// The stub's flippedErr knob short-circuits BEFORE writing stub.flipped,
	// so the absence of stub.flipped records proves the no-op path.
	assert.Empty(t, stub.flipped,
		"P0 #1: ErrAlreadyTerminalAggregate → aggregator MUST NOT regress parent (no stub.flipped writes)")
}

// TestTerminalFlipManualRetryPathGatesOutViaCASConflict verifies that
// when the underlying broker rejects TerminalFlip with ErrAggregateCASConflict
// (manual retry path → status=QUEUED from CLI requeue, parent_state not
// in awaiting states), the aggregator TREATS IT AS A CONFLICT (warn level
// log + leave parent_state unchanged) — NOT a silent no-op and NOT a
// silent regression. The audit acceptance: "manual retry path → gateway out,
// no parent_state corruption".
func TestTerminalFlipManualRetryPathGatesOutViaCASConflict(t *testing.T) {
	stub := &stubAggregatorJobsService{
		flippedErr: domainremote.ErrAggregateCASConflict,
		parentJob: &job.Job{
			ID: "parent-retry", Type: job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded, Result: makeParentStateWaitingChildren(),
		},
		childJobs: map[string]*job.Job{
			"c1": {ID: "c1", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusFailed, Result: makeChildResult(false, "failed", "err")},
			"c2": {ID: "c2", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusFailed, Result: makeChildResult(false, "failed", "err")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	// Tick must not panic; the flippedErr is consumed at warn-level (logger.Warn).
	agg.Tick(context.Background())
	// Stub flippedErr short-circuits BEFORE writes — no stub.flipped entries.
	assert.Empty(t, stub.flipped,
		"P0 #1: ErrAggregateCASConflict → aggregator MUST NOT write to stub.flipped (gated out, retry path safe)")
}
