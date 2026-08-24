// Package jobs — parent_aggregator_testhelpers_test.go
// (PR-SPLIT-VO-PARENT-AGG-TESTS, July 2026).
//
// Shared test plumbing for the parent aggregator test surface
// (post-split sibling test files: parent_aggregator_aggregate_test.go,
// parent_aggregator_finalize_test.go, parent_aggregator_state_test.go,
// parent_aggregator_state_machine_test.go, plus the slim orchestrator
// parent_aggregator_test.go).
//
// godlike/06 SSOT (one canonical owner per fact): this file owns:
//   - stubAggregatorJobsService (the AggregatorJobsService mock)
//   - flipRecord (the typed audit pin surfaced by FinalizeAggregateParent)
//   - the 7 factory helpers (makeParentResult / makeChildResult /
//     makeParentStateWaitingChildren / makeChildPayloadWithRequired /
//     makeMultiChildParentResult / makePartialFanoutParentResult /
//     makeFanoutPartialOkFalseParentResult)
//   - the compile-time `var _ AggregatorJobsService = (*stubAggregatorJobsService)(nil)` pin.
//
// All exports are package-private (`jobs`). Same-package visibility
// reaches the two sibling-consumer test files
// (parent_aggregator_read_preference_test.go, finalizer_invariants_test.go)
// WITHOUT modifications to those files. The helpers are file-suffix
// `_test.go` so they only compile under `go test` — they do NOT
// pollute the production binary (godlike/07 minimum-blast-radius).
//
// Extraneous imports are intentional (kept verbatim from the original
// parent_aggregator_test.go to avoid drift); the unused-import build
// gate will surface real drift if a helper is later split.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// stubAggregatorJobsService satisfies AggregatorJobsService with an
// in-memory job store so tests can inject any job shape without a DB.
//
// Audit 2026-07-03 P0 #1 (added `flipped` map + FinalizeAggregateParent method):
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
	// When non-nil, the stub's FinalizeAggregateParent returns this error
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
// targetStatus is the broker-level status the aggregator's FinalizeAggregateParent
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
func (s *stubAggregatorJobsService) ListAwaitingAggregation(ctx context.Context, parentType string, limit int) ([]job.Job, error) {
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

// FinalizeAggregateParent satisfies the audit 2026-07-03 P0 #1 aggregator port
// extension. Records the flip into stub.flipped (canonical for new
// tests asserting broker-status mirror) AND mirrors the parent_state
// into stub.completed (back-compat for the existing 4 P0.1 false-success
// gate tests that read `stub.completed[id]["parent_state"]`).
func (s *stubAggregatorJobsService) FinalizeAggregateParent(ctx context.Context, id string, targetStatus job.Status, result map[string]any, errMsg string, expectedVersion int) error {
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
		"ok":             true,
		"parent_job_id":  "parent-1",
		"request_id":     "vo_test",
		"total_outputs":  len(childIDs),
		"enqueued_count": len(childIDs),
		"child_job_ids":  childIDs,
		"parent_state":   string(voiceover.ParentWaitingChildren),
	}
	raw, _ := json.Marshal(m)
	return raw
}

// makeChildResult builds a child job's result JSON that
// GenerateItemJobHandler writes (via toItemResultMap).
// ok=false simulates a per-item pipeline failure (StatusFailed).
func makeChildResult(ok bool, status string, errStr string) []byte {
	m := map[string]any{
		"ok":            ok,
		"status":        status,
		"language":      "en",
		"job_id":        "child-1",
		"parent_job_id": "parent-1",
		"request_id":    "vo_test",
	}
	if errStr != "" {
		m["error"] = errStr
	}
	raw, _ := json.Marshal(m)
	return raw
}

// makeParentStateWaitingChildren is a slim parent result for the broker-
// mirror tests: only parent_state + child_job_ids matter (the legacy
// P0.1 tests use a richer fanout-shaped map; the new tests just need
// to assert the routing decision).
//
// variadic childIDs: tests that use semantically-named child IDs (e.g.
// "c-OK"/"c-FAIL") pass them explicitly so the parent's child_job_ids
// align with the stub's childJobs map keys (extractChildJobIDs ↔
// stub.Get(ctx, childID) must match for the aggregator's loop to
// reach the FinalizeAggregateParent call).
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

// makeChildPayloadWithRequired builds a child job payload with the
// Required flag set. Simulates the fan-out handler populating the
// GenerateVoiceoverItemCommand payload.
func makeChildPayloadWithRequired(required bool) []byte {
	m := map[string]any{
		"text":          "test text",
		"language":      "en",
		"voice":         "en-US-Aria",
		"filename":      "test_en.mp3",
		"parent_job_id": "parent-1",
		"request_id":    "vo_test",
		"required":      required,
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
		"ok":                true,
		"parent_job_id":     "parent-multi",
		"request_id":        "vo_acceptance",
		"total_outputs":     totalOutputs,
		"expected_children": expectedChildren,
		"enqueued_count":    enqueued,
		"child_job_ids":     childIDs,
		"parent_state":      string(ps),
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
