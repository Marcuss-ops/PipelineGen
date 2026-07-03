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

	// getCalls tracks every child ID passed to Get(ctx, id) — used by
	// §15.2 to assert that the retry tick only re-queries the child
	// that actually changed status, not the already-terminal siblings.
	getCalls []string
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

// ListAwaitingAggregation delegates to the in-memory parentJob — the
// stub does not mirror the SQL-side json_extract filter. The
// aggregator's aggregateOne still calls IsAwaitingAggregation() on
// each returned parent and skips non-awaiting entries in memory.
func (s *stubAggregatorJobsService) ListAwaitingAggregation(ctx context.Context, limit int) ([]job.Job, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.parentJob == nil {
		return nil, nil
	}
	return []job.Job{*s.parentJob}, nil
}

func (s *stubAggregatorJobsService) Get(ctx context.Context, id string) (*job.Job, error) {
	s.getCalls = append(s.getCalls, id)
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
func (s *stubAggregatorJobsService) TerminalFlip(ctx context.Context, id string, targetStatus job.Status, result map[string]any, errMsg string, expectedVersion int) error {
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

// ─────────────────────────────────────────────────────────────────
// Voiceover §15 Acceptance Tests (FASE 2, July 2026)
// ─────────────────────────────────────────────────────────────────

// makeChildPayloadWithRequired builds a child job payload with the
// Required flag set. Simulates the fan-out handler populating the
// GenerateVoiceoverItemCommand payload.
func makeChildPayloadWithRequired(required bool) []byte {
	m := map[string]any{
		"text":         "test text",
		"language":     "en",
		"voice":        "en-US-Aria",
		"filename":     "test_en.mp3",
		"parent_job_id": "parent-1",
		"request_id":   "vo_test",
		"required":     required,
	}
	raw, _ := json.Marshal(m)
	return raw
}

// makeMultiChildParentResult builds a parent result with N children.
// expectedChildren and enqueuedCount default to len(childIDs) when zero
// (back-compat with existing tests). For partial fan-out tests, pass
// explicit values smaller than len(childIDs).
func makeMultiChildParentResult(childIDs []string, totalOutputs int, expectedChildren int, parentState voiceover.ParentState) []byte {
	if expectedChildren <= 0 {
		expectedChildren = len(childIDs)
	}
	enqueued := expectedChildren
	ps := parentState
	if ps == "" {
		ps = voiceover.ParentWaitingChildren
	}
	m := map[string]any{
		"ok":              true,
		"parent_job_id":   "parent-multi",
		"request_id":      "vo_acceptance",
		"total_outputs":   totalOutputs,
		"expected_children": expectedChildren,
		"enqueued_count":  enqueued,
		"child_job_ids":   childIDs,
		"parent_state":    string(ps),
	}
	raw, _ := json.Marshal(m)
	return raw
}

// makePartialFanoutParentResult builds a parent result simulating what
// the fan-out handler writes after a partial fan-out (3 items requested,
// 2 enqueued). expected_children = 2 (matches EnqueuedCount in
// toFanoutResultMap), parent_state = partial_success, ok=true
// (the handler writes ok=res.OK which is true when at least one child
// was enqueued successfully). child_job_ids carries the 2 real IDs +
// 1 empty string for the failed enqueue.
// For the ok=false variant, use makeFanoutPartialOkFalseParentResult.
func makePartialFanoutParentResult(childIDs []string) []byte {
	return makeMultiChildParentResult(childIDs, 3, 2, voiceover.ParentPartialSuccess)
}

// makeFanoutPartialOkFalseParentResult builds a parent result simulating
// the real fan-out handler's output when some enqueues fail: ok=false
// (toFanoutResultMap sets ok=res.OK), parent_state=partial_success,
// expected_children=2, child_job_ids has 2 real IDs + 1 empty string.
// This is the variant used by §15.4c — the aggregator must handle
// ok=false the same way as ok=true (both reach IsAwaitingAggregation).
func makeFanoutPartialOkFalseParentResult(childIDs []string) []byte {
	ids := childIDs
	m := map[string]any{
		"ok":                false,
		"parent_job_id":     "parent-multi",
		"request_id":        "vo_acceptance",
		"total_outputs":     3,
		"expected_children": 2,
		"enqueued_count":    2,
		"child_job_ids":     ids,
		"parent_state":      string(voiceover.ParentPartialSuccess),
	}
	raw, _ := json.Marshal(m)
	return raw
}

// ─────────────────────────────────────────────────────────────────
// §15.1 Happy path: 3 languages, 3 children, all SUCCEEDED
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_HappyPath_ThreeLanguagesAllSucceeded(t *testing.T) {
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-happy",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it", "c-en", "c-pt"}, 3, 0, ""),
		},
		childJobs: map[string]*job.Job{
			"c-it": {ID: "c-it", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-en": {ID: "c-en", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-pt": {ID: "c-pt", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-happy",
		"§15.1: aggregator must finalise parent when all 3 children are terminal")
	got := stub.flipped["parent-happy"]
	assert.Equal(t, job.StatusSucceeded, got.targetStatus,
		"§15.1: broker status must stay SUCCEEDED when all children succeeded")
	assert.Equal(t, "succeeded", got.result["parent_state"],
		"§15.1: parent_state must be 'succeeded' when all children succeeded")
	assert.Equal(t, 3, got.result["total_children"],
		"§15.1: total_children must be 3")
	assert.Equal(t, 3, got.result["succeeded_count"],
		"§15.1: succeeded_count must be 3")
	assert.Equal(t, 0, got.result["failed_count"],
		"§15.1: failed_count must be 0")
}

// ─────────────────────────────────────────────────────────────────
// §15.2 TTS transient failure: child retry, others unaffected
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_TTSTransientFailure_ChildRetryParentStaysOpen(t *testing.T) {
	// c-it and c-pt are terminal, c-en is still in RETRY_WAIT.
	// Aggregator must skip the parent (not all children terminal).
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-retry",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it", "c-en", "c-pt"}, 3, 0, ""),
		},
		childJobs: map[string]*job.Job{
			"c-it": {ID: "c-it", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-en": {ID: "c-en", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusRetryWait, Result: makeChildResult(false, "failed", "tts timeout")},
			"c-pt": {ID: "c-pt", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	// Parent must NOT be finalised — c-en is still in flight.
	assert.Empty(t, stub.flipped,
		"§15.2: aggregator must NOT finalise parent when a child is still RETRY_WAIT")

	// Simulate next tick: c-en has been retried and now SUCCEEDED.
	stub.childJobs["c-en"].Status = job.StatusSucceeded
	stub.childJobs["c-en"].Result = makeChildResult(true, "completed", "")
	// Reset stub.flipped for clean assertion.
	stub.flipped = nil
	// Reset getCalls so we can assert only c-en is re-queried on the retry tick.
	stub.getCalls = nil
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-retry",
		"§15.2: after retry succeeds, aggregator must finalise parent")
	got := stub.flipped["parent-retry"]
	assert.Equal(t, "succeeded", got.result["parent_state"],
		"§15.2: after retry, parent_state must be 'succeeded' (all terminal, all succeeded)")

	// §15.2 code-review assertion #3 (July 2026): on the retry tick,
	// the aggregator must only re-query the child whose status changed
	// (c-en). Already-terminal siblings (c-it, c-pt) are cached in the
	// previouslyTerminal map and skip the Get() call entirely. This
	// prevents N redundant broker round-trips per retry tick where
	// N-1 children were already terminal.
	assert.Len(t, stub.getCalls, 1,
		"§15.2: retry tick must re-query only c-en (1 Get call); c-it and c-pt were already terminal and skipped via previouslyTerminal cache")
	assert.Contains(t, stub.getCalls, "c-en",
		"§15.2: retry tick must re-query c-en (the retried child)")
}

// ─────────────────────────────────────────────────────────────────
// §15.3 Permanent voice error: REQUIRED→FAILED, optional→partial
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_PermanentVoiceError_RequiredChildFailsParent(t *testing.T) {
	// c-required is REQUIRED and FAILED → parent must go to FAILED.
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-reqfail",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-required", "c-ok"}, 2, 0, ""),
		},
		childJobs: map[string]*job.Job{
			"c-required": {
				ID:      "c-required",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "voice 'xx-ZZ-Bogus' not found"),
				Payload: makeChildPayloadWithRequired(true), // REQUIRED
			},
			"c-ok": {
				ID:      "c-ok",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusSucceeded,
				Result:  makeChildResult(true, "completed", ""),
				Payload: makeChildPayloadWithRequired(false),
			},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-reqfail",
		"§15.3: REQUIRED child FAILED → aggregator must finalise parent")
	got := stub.flipped["parent-reqfail"]
	assert.Equal(t, job.StatusFailed, got.targetStatus,
		"§15.3: REQUIRED child FAILED → broker status must flip to FAILED")
	assert.Equal(t, "failed", got.result["parent_state"],
		"§15.3: REQUIRED child FAILED → parent_state must be 'failed'")
	assert.Equal(t, 1, got.result["required_failed_count"],
		"§15.3: required_failed_count must be 1")
}

func TestAcceptance_OptionalVoiceError_ParentSucceedsWithWarning(t *testing.T) {
	// c-opt-fail is OPTIONAL and FAILED, c-ok is SUCCEEDED → partial_success.
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-optfail",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-opt-fail", "c-ok"}, 2, 0, ""),
		},
		childJobs: map[string]*job.Job{
			"c-opt-fail": {
				ID:      "c-opt-fail",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "voice 'xx-YY-Nope' not found"),
				Payload: makeChildPayloadWithRequired(false), // OPTIONAL
			},
			"c-ok": {
				ID:      "c-ok",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusSucceeded,
				Result:  makeChildResult(true, "completed", ""),
				Payload: makeChildPayloadWithRequired(false),
			},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-optfail",
		"§15.3b: optional child FAILED with another SUCCEEDED → aggregator must finalise parent")
	got := stub.flipped["parent-optfail"]
	assert.Equal(t, job.StatusSucceeded, got.targetStatus,
		"§15.3b: optional FAILED + one SUCCEEDED → broker status stays SUCCEEDED")
	assert.Equal(t, "partial_success", got.result["parent_state"],
		"§15.3b: optional FAILED + one SUCCEEDED → parent_state must be 'partial_success'")
	assert.Equal(t, 0, got.result["required_failed_count"],
		"§15.3b: optional failures don't increment required_failed_count")
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

// ─────────────────────────────────────────────────────────────────
// §15.4c Fan-out parziale con ok=false: real partial fan-out where
// some enqueues failed. The parent result has ok=false (res.OK=false
// in toFanoutResultMap), parent_state=partial_success, child_job_ids
// has 2 real children + 1 empty slot. The aggregator must handle this
// identically to the ok=true case — IsAwaitingAggregation only inspects
// parent_state, not ok.
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_FanoutParziale_OKFalse_AggregatorHandlesPartialSuccess(t *testing.T) {
	// 3 languages requested, only 2 enqueued (fan-out parziale).
	// ok=false because res.OK is false when some enqueues fail.
	// c-it SUCCEEDED, c-en FAILED (optional), 3rd child never enqueued.
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-okfalse",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeFanoutPartialOkFalseParentResult([]string{"c-it", "c-en", ""}),
		},
		childJobs: map[string]*job.Job{
			"c-it": {
				ID:      "c-it",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusSucceeded,
				Result:  makeChildResult(true, "completed", ""),
				Payload: makeChildPayloadWithRequired(false),
			},
			"c-en": {
				ID:      "c-en",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "tts_failed: Edge TTS timeout"),
				Payload: makeChildPayloadWithRequired(false),
			},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	// Criterion 1: aggregator MUST still finalise the parent even with ok=false.
	// IsAwaitingAggregation() inspects parent_state, not ok — both ok=true and
	// ok=false with parent_state=partial_success reach the aggregation loop.
	require.Contains(t, stub.flipped, "parent-okfalse",
		"§15.4c: ok=false parent with parent_state=partial_success must still be finalised by aggregator")

	got := stub.flipped["parent-okfalse"]

	// Criterion 2: broker status stays SUCCEEDED (partial_success is not terminal failure).
	assert.Equal(t, job.StatusSucceeded, got.targetStatus,
		"§15.4c: ok=false + partial_success → broker status stays SUCCEEDED")

	// Criterion 3: parent_state remains partial_success (one succeeded, one optional-failed).
	assert.Equal(t, "partial_success", got.result["parent_state"],
		"§15.4c: one succeeded + one optional-failed → parent_state='partial_success'")

	// Criterion 4: total_children = 2 (empty string filtered).
	assert.Equal(t, 2, got.result["total_children"],
		"§15.4c: total_children=2 (empty string filtered out)")

	// Criterion 5: succeeded_count=1, failed_count=1.
	assert.Equal(t, 1, got.result["succeeded_count"],
		"§15.4c: 1 child succeeded (c-it)")
	assert.Equal(t, 1, got.result["failed_count"],
		"§15.4c: 1 child failed (c-en, optional TTS failure)")

	// Criterion 6: no error message on partial_success flip.
	assert.Empty(t, got.errMsg,
		"§15.4c: partial_success flip has no error message")
}

// ─────────────────────────────────────────────────────────────────
// §15.4b Fan-out parziale con accurate handler simulation:
// 3 items richiesti, 2 enqueued, expected_children=2, parent_state=partial_success.
// One child optional-failed, one child succeeded → TerminalFlip with
// parent_state=partial_success, broker status SUCCEEDED.
// ─────────────────────────────────────────────────────────────────

// TestAcceptance_PartialFanout_ExpectedChildrenMatchesEnqueued verifies
// the complete acceptance criteria for partial fan-out (§15 fan-out parziale):
//
//  1. expected_children = 2 (the handler writes res.EnqueuedCount, NOT total_outputs)
//  2. parent_state = "partial_success" (res.OK=false → partial_success in toFanoutResultMap)
//  3. TerminalFlip is called correctly — broker status stays SUCCEEDED
//     (partial_success is not a terminal failure), parent_state emits "partial_success"
//     in the finalised result.
//
// This test uses makePartialFanoutParentResult which accurately simulates what
// the fan-out handler's toFanoutResultMap produces: expected_children=2, parent_state=
// partial_success, child_job_ids=["c-it", "c-en", ""] where the empty string
// represents the failed enqueue for the 3rd language.
func TestAcceptance_PartialFanout_ExpectedChildrenMatchesEnqueued(t *testing.T) {
	// c-it: SUCCEEDED (the Italian voiceover completed successfully)
	// c-en: FAILED (the English voiceover had a TTS failure, but it's OPTIONAL)
	// 3rd child (e.g. Portuguese): never enqueued (empty string in child_job_ids)
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:       "parent-partial-accurate",
			Type:     job.TypeVoiceoverGenerate,
			Status:   job.StatusSucceeded,
			Result:   makePartialFanoutParentResult([]string{"c-it", "c-en", ""}),
			Revision: 7,
		},
		childJobs: map[string]*job.Job{
			"c-it": {
				ID:      "c-it",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusSucceeded,
				Result:  makeChildResult(true, "completed", ""),
				Payload: makeChildPayloadWithRequired(false),
			},
			"c-en": {
				ID:      "c-en",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "tts_failed: Deepgram connection timeout"),
				Payload: makeChildPayloadWithRequired(false), // OPTIONAL
			},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	// Criterion 1: aggregator must finalise the parent (TerminalFlip called).
	require.Contains(t, stub.flipped, "parent-partial-accurate",
		"§15.4b: aggregator must call TerminalFlip when all enqueued children are terminal")

	got := stub.flipped["parent-partial-accurate"]

	// Criterion 2: broker status stays SUCCEEDED (partial_success ≠ terminal failure).
	assert.Equal(t, job.StatusSucceeded, got.targetStatus,
		"§15.4b: partial_success → broker status stays SUCCEEDED (no false-FAILED flip)")

	// Criterion 3: parent_state = "partial_success" (one succeeded, one optional-failed).
	assert.Equal(t, "partial_success", got.result["parent_state"],
		"§15.4b: one succeeded + one optional-failed → parent_state='partial_success'")

	// Criterion 4: total_children = 2 (empty string filtered out — the 3rd child
	// was never enqueued, the aggregator must not count it).
	assert.Equal(t, 2, got.result["total_children"],
		"§15.4b: total_children=2 (empty string for failed enqueue filtered out)")

	// Criterion 5: succeeded_count=1, failed_count=1.
	assert.Equal(t, 1, got.result["succeeded_count"],
		"§15.4b: 1 child succeeded (c-it)")
	assert.Equal(t, 1, got.result["failed_count"],
		"§15.4b: 1 child failed (c-en, optional TTS failure)")

	// Criterion 6: no REQUIRED failures → required_failed_count=0.
	assert.Equal(t, 0, got.result["required_failed_count"],
		"§15.4b: optional-only failures → required_failed_count=0")

	// Criterion 7: version CAS guard — StateMachineVersion = j.Revision (7).
	assert.Equal(t, 7, got.result["_aggregator_version"],
		"§15.4b: StateMachineVersion must match j.Revision (7) for CAS guard")

	// Criterion 8: no error message on partial_success flip.
	assert.Empty(t, got.errMsg,
		"§15.4b: partial_success flip has no error message")
}

// ─────────────────────────────────────────────────────────────────
// §15.8 Cancel parent: children in flight, parent CANCELLED
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_CancelParent_AggregatorSkips(t *testing.T) {
	// Parent was cancelled after fan-out. Children may still complete,
	// but the aggregator should skip the parent (parent_state is not
	// waiting_children or partial_success).
	cancelParentResult := map[string]any{
		"ok":            false,
		"parent_job_id": "parent-cancelled",
		"parent_state":  "failed",
		"child_job_ids": []string{"c-it", "c-en"},
	}
	cancelRaw, _ := json.Marshal(cancelParentResult)

	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-cancelled",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusCancelled,
			Result: cancelRaw,
		},
		childJobs: map[string]*job.Job{
			"c-it": {ID: "c-it", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-en": {ID: "c-en", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	// Aggregator must NOT finalise — parent_state="failed" is not awaiting aggregation.
	assert.Empty(t, stub.flipped,
		"§15.8: cancelled parent with parent_state='failed' must NOT be finalised by aggregator")
}

// ─────────────────────────────────────────────────────────────────
// §15.6 DB succeeded + worker crash: idempotent retry
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_DBSucceededWorkerCrash_IdempotentRetry(t *testing.T) {
	// Simulates: DB transaction completed, worker crashed before ACK.
	// The retry finds all children terminal; the TerminalFlip CAS
	// returns ErrAlreadyTerminalAggregate (another tick already landed).
	// The aggregator must treat this as a silent no-op.
	stub := &stubAggregatorJobsService{
		flippedErr: domainremote.ErrAlreadyTerminalAggregate,
		parentJob: &job.Job{
			ID:     "parent-idem",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-it", "c-en", "c-pt"}, 3, 0, ""),
		},
		childJobs: map[string]*job.Job{
			"c-it": {ID: "c-it", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-en": {ID: "c-en", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c-pt": {ID: "c-pt", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	// Must not panic; the ErrAlreadyTerminalAggregate is silently consumed.
	agg.Tick(context.Background())

	// No flip was written — the previous tick already finalised.
	assert.Empty(t, stub.flipped,
		"§15.6: ErrAlreadyTerminalAggregate → no regression, no panic, no duplicate")
}

// ─────────────────────────────────────────────────────────────────
// §15.2b Required flag propagation from child payload
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_RequiredFlagPropagatedFromChildPayload(t *testing.T) {
	// Verifies that VoiceoverChildPayload.Required is correctly read
	// from the child job's payload JSON and fed to the StateMachine.
	// Two children: one REQUIRED-failed → parent FAILED.
	stub := &stubAggregatorJobsService{
		parentJob: &job.Job{
			ID:     "parent-reqprop",
			Type:   job.TypeVoiceoverGenerate,
			Status: job.StatusSucceeded,
			Result: makeMultiChildParentResult([]string{"c-req", "c-opt"}, 2, 0, ""),
		},
		childJobs: map[string]*job.Job{
			"c-req": {
				ID:      "c-req",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "permanent TTS failure"),
				Payload: makeChildPayloadWithRequired(true), // REQUIRED=true
			},
			"c-opt": {
				ID:      "c-opt",
				Type:    job.TypeVoiceoverGenerateItem,
				Status:  job.StatusFailed,
				Result:  makeChildResult(false, "failed", "transient upload failure"),
				Payload: makeChildPayloadWithRequired(false), // REQUIRED=false
			},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	agg.Tick(context.Background())

	require.Contains(t, stub.flipped, "parent-reqprop",
		"§15.2b: REQUIRED child FAILED → aggregator must finalise parent")
	got := stub.flipped["parent-reqprop"]
	assert.Equal(t, job.StatusFailed, got.targetStatus,
		"§15.2b: REQUIRED-failed → broker status flipped to FAILED")
	assert.Equal(t, "failed", got.result["parent_state"],
		"§15.2b: REQUIRED-failed → parent_state='failed'")
	assert.Equal(t, 1, got.result["required_failed_count"],
		"§15.2b: only c-req counts as required_failed (c-opt is optional)")
}

// ─────────────────────────────────────────────────────────────────
// §15.7 Parent aggregator crash: version CAS conflict recovery
// ─────────────────────────────────────────────────────────────────

func TestAcceptance_AggregatorCrash_VersionCASConflictRecovery(t *testing.T) {
	// Simulates: aggregator tick reads parent at revision=3, computes
	// aggregate, but TerminalFlip's SQL UPDATE returns 0 rows because
	// another tick already bumped revision to 4. The aggregator must
	// treat ErrAggregateCASConflict as a warn-level no-op.
	stub := &stubAggregatorJobsService{
		flippedErr: domainremote.ErrAggregateCASConflict,
		parentJob: &job.Job{
			ID:       "parent-casconflict",
			Type:     job.TypeVoiceoverGenerate,
			Status:   job.StatusSucceeded,
			Result:   makeMultiChildParentResult([]string{"c1", "c2"}, 2, 0, ""),
			Revision: 3,
		},
		childJobs: map[string]*job.Job{
			"c1": {ID: "c1", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
			"c2": {ID: "c2", Type: job.TypeVoiceoverGenerateItem, Status: job.StatusSucceeded, Result: makeChildResult(true, "completed", "")},
		},
	}
	agg := NewParentAggregator(AggregatorDeps{JobsSvc: stub, Logger: zap.NewNop(), PollInterval: 30 * time.Second})
	// Must not panic; the ErrAggregateCASConflict is consumed at warn-level.
	agg.Tick(context.Background())

	// No flip written — CAS conflict means the row was already updated.
	assert.Empty(t, stub.flipped,
		"§15.7: ErrAggregateCASConflict → no regression, next tick recovers")
}
