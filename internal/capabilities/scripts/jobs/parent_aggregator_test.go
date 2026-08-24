// Package jobs — parent_aggregator_test.go (P0 #4 audit 2026-07-03
// closure, 4 acceptance tests + 4 CAS narrow-port tests).
//
// Pins the canonical P0 #4 acceptance surface for the script-batch
// aggregator (mirror of voiceover/jobs/parent_aggregator_test.go at
// commit 7f319edb). Each test drives the public Tick API with a stub
// ScriptAggregatorJobsService that returns pre-configured parent + child
// jobs. Tick calls ListAwaitingAggregation → Get → FinalizeAggregateParent on the
// stub; assertions read the stub's flipped / completed maps.
//
// Existing acceptance suite (audit P0 #4, 6 tests) — pinned pre-2026-07-03:
//
//  1. TestScriptParentBrokerStatusIsFAILEDWhenAllChildrenFailed
//  2. TestScriptParentBrokerFAILEDWhenChildResultOKIsFalse
//  3. TestFinalizeAggregateParentReplayIsIdempotent_NoOp
//  4. TestScriptParentBrokerStatusSUCCEEDEDWhenMixedOutcome
//  5. TestAcceptance_HappyPath_AllSucceeded
//  6. TestFinalizeAggregateParentCASConflict_LoggedAndNoFatal
//
// New CAS narrow-port suite (added 2026-07-03, audit P0 #4 lock):
//
//  7. TestFinalizeAggregateParent_Mixed_PreservesRevision — mixed outcome keeps
//     broker-status SUCCEEDED + parent_state=partial_success. Records
//     that the flip call passes expectedVersion == parent.Revision
//     (CAS fence). Real SQL UPDATE bumps revision atomically; the
//     stub-level assertion pins the CAS argument selection, not the
//     post-update row revision (that's an integration-test concern).
//
//  8. TestFinalizeAggregateParent_AllFailed_PopulatesErrMsg — all-failed flips
//     broker-status FAILED + populates errMsg with the canonical
//     aggregate marker "script aggregate: all child jobs
//     definitively failed" (audit-forensics readable on operator
//     dashboards). Records expectedVersion == parent.Revision.
//
//  9. TestFinalizeAggregateParent_StaleRevision_ReturnsErrAggregateCASConflict —
//     stub.FinalizeAggregateParent returns domainremote.ErrAggregateCASConflict
//     (simulating a concurrent tick's revision bump). Aggregator
//     must (a) NOT mutate stub.flipped + (b) emit a Warn-level log
//     with the "FinalizeAggregateParent CAS conflict" message (audit pin).
//     Uses zap/zaptest/observer for non-destructive log capture.
//
//  10. TestFinalizeAggregateParent_ReplayIdempotentAfterAlreadyTerminal — second
//     tick AFTER a successful first tick, with stub simulating the
//     "already-terminal" state. Aggregator must (a) NOT overwrite
//     the first tick's stub.flipped record + (b) emit ZERO Warn log
//     entries (idempotent replay is silent — INFO level acceptable,
//     WARN forbidden).
package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	domainremote "github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// stubScriptAggregatorJobsService satisfies ScriptAggregatorJobsService
// with an in-memory job store so tests can inject any job shape
// without a DB. Mirrors voiceover/jobs/parent_aggregator_test.go
// ::stubAggregatorJobsService exactly, with the audit-pinned 2026-07-03
// extension: tracks the expectedVersion (parent.Revision) passed on
// each FinalizeAggregateParent call so CAS-fence-argument assertions can run.
type stubScriptAggregatorJobsService struct {
	parentJob  *job.Job
	childJobs  map[string]*job.Job // childID → *job.Job
	completed  map[string]map[string]any
	flipped    map[string]scriptFlipRecord
	flippedErr error // CAS-rejection knob (ErrAggregateCASConflict / ErrAlreadyTerminalAggregate)
	listErr    error
}

// scriptFlipRecord is the typed audit pin for the P0 #4 closure tests
// (mirror of voiceover's flipRecord + 2026-07-03 extension: expectedVersion
// captures the parent.Revision that the aggregator passed as the SQL
// CAS-fence argument; in real SQL this is the row's revision at tick
// start, and the UPDATE WHERE revision=? is the CAS fence).
//
// 2026-07-08 PR-4 extension: result captures the resultMap arg so
// child→parent doc_id/doc_link propagation tests can assert the
// presence + content of child_doc_links / child_doc_ids keys (per
// godlike/06 SSOT — the aggregator is the SOLE writer of these keys
// in the parent result; the test pin locks the contract).
type scriptFlipRecord struct {
	targetStatus    job.Status
	errMsg          string
	expectedVersion int
	result          map[string]any
}

// Ensure the stub satisfies the port interface (compile-time).
var _ ScriptAggregatorJobsService = (*stubScriptAggregatorJobsService)(nil)

// ListAwaitingAggregation returns the parent job (matches voiceover's
// stub surface — single parent, no batch filtering).
// Commit 3: parentType param added (ignored in stub).
func (s *stubScriptAggregatorJobsService) ListAwaitingAggregation(ctx context.Context, parentType string, limit int) ([]job.Job, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if s.parentJob == nil {
		return nil, nil
	}
	return []job.Job{*s.parentJob}, nil
}

// Get returns the child or parent job by ID.
func (s *stubScriptAggregatorJobsService) Get(ctx context.Context, id string) (*job.Job, error) {
	if j, ok := s.childJobs[id]; ok {
		return j, nil
	}
	if s.parentJob != nil && s.parentJob.ID == id {
		return s.parentJob, nil
	}
	return nil, nil
}

// TerminalFlip records the flip into stub.flipped (including expectedVersion
// so CAS-fence-argument assertions can pin parent.Revision selection;
// and the result map so child→parent doc_id/doc_link propagation tests
// can assert the child_doc_links/child_doc_ids keys per PR-4).
func (s *stubScriptAggregatorJobsService) FinalizeAggregateParent(ctx context.Context, id string, targetStatus job.Status, result map[string]any, errMsg string, expectedVersion int) error {
	if s.flippedErr != nil {
		// CAS-rejection simulation: do NOT mutate stub.flipped (the
		// non-mutation contract is per godlike/07 no-fake-availability;
		// the aggregator's response is logged at warn level for
		// ErrAggregateCASConflict or absorbed silently for
		// ErrAlreadyTerminalAggregate).
		return s.flippedErr
	}
	if s.flipped == nil {
		s.flipped = make(map[string]scriptFlipRecord)
	}
	s.flipped[id] = scriptFlipRecord{
		targetStatus:    targetStatus,
		errMsg:          errMsg,
		expectedVersion: expectedVersion,
		result:          result,
	}
	return nil
}

// makeScriptParentStateWaitingChildren builds the parent's result JSON
// with parent_state=waiting_children + typed child_job_ids.
func makeScriptParentStateWaitingChildren(parentID string, childIDs []string, totalItems int) []byte {
	parentResult := ScriptParentResult{
		OK:          true,
		ParentJobID: parentID,
		ParentState: string(ScriptParentWaitingChildren),
		TotalItems:  totalItems,
		ChildJobIDs: childIDs,
	}
	raw, _ := json.Marshal(parentResult)
	return raw
}

// makeScriptChildResultOK builds the per-child result JSON with
// ok=true + status=completed (matches the canonical emission shape
// from script_generation_item_handler.go::toScriptItemResultMap).
func makeScriptChildResultOK(childID string, ok bool) []byte {
	type childResultWire struct {
		ItemID string `json:"item_id"`
		JobID  string `json:"job_id"`
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	wire := childResultWire{
		ItemID: childID,
		JobID:  childID,
		OK:     ok,
		Status: map[bool]string{true: "completed", false: "failed"}[ok],
	}
	raw, _ := json.Marshal(wire)
	return raw
}

// makeScriptChildResultWithDoc builds the per-child result JSON with
// doc_link + doc_id fields populated (PR-4 child→parent propagation).
// The aggregator reads DocLink + DocID from the unmarshalled
// ScriptChildResult and surfaces them in the parent result map
// (child_doc_links / child_doc_ids keyed by item_id). When
// docLink or docID is empty, the corresponding key is omitted from
// the JSON wire shape (omitempty contract).
func makeScriptChildResultWithDoc(itemID string, ok bool, docLink string, docID string) []byte {
	type childResultWire struct {
		ItemID  string `json:"item_id"`
		JobID   string `json:"job_id"`
		OK      bool   `json:"ok"`
		Status  string `json:"status"`
		DocLink string `json:"doc_link,omitempty"`
		DocID   string `json:"doc_id,omitempty"`
	}
	wire := childResultWire{
		ItemID:  itemID,
		JobID:   itemID,
		OK:      ok,
		Status:  map[bool]string{true: "completed", false: "failed"}[ok],
		DocLink: docLink,
		DocID:   docID,
	}
	raw, _ := json.Marshal(wire)
	return raw
}

// buildParentStubWithDocs constructs a stub parent + N children with
// the given per-child status + result_ok + (docLink, docID) quadruple.
// The parent's parent_state is "waiting_children" so the aggregator
// routes it through aggregateOne. Reuses the buildParentStub
// surface but extends it with the doc fields needed for PR-4
// child→parent propagation tests.
func buildParentStubWithDocs(parentID string, childSpecs map[string]struct {
	Status  job.Status
	OK      bool
	DocLink string
	DocID   string
}) *stubScriptAggregatorJobsService {
	childIDs := make([]string, 0, len(childSpecs))
	for id := range childSpecs {
		childIDs = append(childIDs, id)
	}
	parent := &job.Job{
		ID:     parentID,
		Type:   job.TypeScriptGenerate,
		Status: job.StatusFinalizing,
		Result: makeScriptParentStateWaitingChildren(parentID, childIDs, len(childIDs)),
	}
	children := make(map[string]*job.Job, len(childSpecs))
	for id, spec := range childSpecs {
		children[id] = &job.Job{
			ID:     id,
			Type:   job.TypeScriptGenerateItem,
			Status: spec.Status,
			Result: makeScriptChildResultWithDoc(id, spec.OK, spec.DocLink, spec.DocID),
		}
	}
	return &stubScriptAggregatorJobsService{
		parentJob: parent,
		childJobs: children,
	}
}

// buildParentStub constructs a stub parent + N children with the given
// per-child status + result_ok combination. The parent's parent_state
// is "waiting_children" so the aggregator routes it through aggregateOne.
func buildParentStub(parentID string, childStatuses map[string]job.Status, childResultOKs map[string]bool) *stubScriptAggregatorJobsService {
	childIDs := make([]string, 0, len(childStatuses))
	for id := range childStatuses {
		childIDs = append(childIDs, id)
	}
	parent := &job.Job{
		ID:     parentID,
		Type:   job.TypeScriptGenerate,
		Status: job.StatusFinalizing,
		Result: makeScriptParentStateWaitingChildren(parentID, childIDs, len(childIDs)),
		// Revision defaults to 0 in tests; production reads parent.Revision
		// from the SQLite row at tick start.
	}
	children := make(map[string]*job.Job, len(childStatuses))
	for id, status := range childStatuses {
		ok, okSet := childResultOKs[id]
		if !okSet {
			ok = true
		}
		children[id] = &job.Job{
			ID:     id,
			Type:   job.TypeScriptGenerateItem,
			Status: status,
			Result: makeScriptChildResultOK(id, ok),
		}
	}
	return &stubScriptAggregatorJobsService{
		parentJob: parent,
		childJobs: children,
	}
}

// Test 1: all children FAILED → parent broker FAILED via FinalizeAggregateParent.
func TestScriptParentBrokerStatusIsFAILEDWhenAllChildrenFailed(t *testing.T) {
	stub := buildParentStub("parent-fail", map[string]job.Status{
		"c1": job.StatusFailed,
		"c2": job.StatusFailed,
		"c3": job.StatusFailed,
	}, map[string]bool{"c1": false, "c2": false, "c3": false})

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	if _, ok := stub.flipped["parent-fail"]; !ok {
		t.Fatal("P0 #4: aggregator must call FinalizeAggregateParent on full-failure aggregate")
	}
	got := stub.flipped["parent-fail"]
	if got.targetStatus != job.StatusFailed {
		t.Errorf("P0 #4: aggregate=failed_terminal MUST flip broker-status to FAILED, got %s",
			got.targetStatus)
	}
	if got.errMsg == "" {
		t.Error("P0 #4: failed-terminal flip must carry a non-empty errMsg (audit forensics)")
	}
}

// Test 2: child broker=SUCCEEDED but result.ok=false → P0.1-gate-extension
// overrides child to FAILED. With all-children-failed-after-gate, the
// parent broker flips to FAILED.
func TestScriptParentBrokerFAILEDWhenChildResultOKIsFalse(t *testing.T) {
	stub := buildParentStub("parent-ok-false", map[string]job.Status{
		"c1": job.StatusSucceeded,
		"c2": job.StatusSucceeded,
		"c3": job.StatusSucceeded,
	}, map[string]bool{"c1": false, "c2": false, "c3": false})

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	if _, ok := stub.flipped["parent-ok-false"]; !ok {
		t.Fatal("P0 #4: aggregator must call FinalizeAggregateParent when P0.1-gate-extension forces all-failed aggregate")
	}
	got := stub.flipped["parent-ok-false"]
	if got.targetStatus != job.StatusFailed {
		t.Errorf("P0 #4: P0.1-gate-extension (result.ok=false on broker-SUCCEEDED children) MUST flip broker-status to FAILED, got %s",
			got.targetStatus)
	}
}

// Test 3: idempotent re-aggregation. When FinalizeAggregateParent returns
// ErrAlreadyTerminalAggregate (a previous tick already landed the
// terminal flip), the aggregator must treat this as a silent no-op
// without re-attempting the flip. The stub's flipped map is NOT
// mutated (because flippedErr is set), so the test verifies that
// the aggregator's response to ErrAlreadyTerminalAggregate is
// non-fatal (logger.info + return).
func TestFinalizeAggregateParentReplayIsIdempotent_NoOp(t *testing.T) {
	stub := buildParentStub("parent-replay", map[string]job.Status{
		"c1": job.StatusSucceeded,
		"c2": job.StatusSucceeded,
	}, map[string]bool{"c1": true, "c2": true})
	stub.flippedErr = domainremote.ErrAlreadyTerminalAggregate

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	// Tick must NOT panic, must NOT mutate stub.flipped (the rejection
	// path treats flipped as a no-op signal).
	agg.Tick(context.Background())

	if _, ok := stub.flipped["parent-replay"]; ok {
		t.Error("P0 #4: aggregator must NOT mutate stub.flipped when FinalizeAggregateParent returns ErrAlreadyTerminalAggregate (idempotent no-op)")
	}
}

// Test 4: mixed-outcome stays broker=SUCCEEDED. Some children succeed,
// some fail — and because all script-batch children are OPTIONAL
// (no Required field), the aggregate settles on Succeeded (with
// warnings via PartialSuccess state). The broker status stays
// SUCCEEDED.
func TestScriptParentBrokerStatusSUCCEEDEDWhenMixedOutcome(t *testing.T) {
	stub := buildParentStub("parent-mix", map[string]job.Status{
		"c1": job.StatusSucceeded,
		"c2": job.StatusFailed,
		"c3": job.StatusSucceeded,
	}, map[string]bool{"c1": true, "c2": false, "c3": true})

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	if _, ok := stub.flipped["parent-mix"]; !ok {
		t.Fatal("P0 #4: aggregator must call FinalizeAggregateParent on partial-success aggregate")
	}
	got := stub.flipped["parent-mix"]
	if got.targetStatus != job.StatusSucceeded {
		t.Errorf("P0 #4: mixed-outcome aggregate (some optional failed) MUST preserve broker-status=SUCCEEDED, got %s",
			got.targetStatus)
	}
	if got.errMsg != "" {
		t.Errorf("P0 #4: SUCCEEDED flip must carry empty errMsg, got %q", got.errMsg)
	}
}

// Test 5 (positive canonical): all children SUCCEEDED with result.ok=true
// → parent broker=SUCCEEDED + parent_state=succeeded.
func TestAcceptance_HappyPath_AllSucceeded(t *testing.T) {
	stub := buildParentStub("parent-happy", map[string]job.Status{
		"c1": job.StatusSucceeded,
		"c2": job.StatusSucceeded,
		"c3": job.StatusSucceeded,
	}, map[string]bool{"c1": true, "c2": true, "c3": true})

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	if _, ok := stub.flipped["parent-happy"]; !ok {
		t.Fatal("P0 #4: aggregator must call FinalizeAggregateParent on all-succeeded aggregate")
	}
	got := stub.flipped["parent-happy"]
	if got.targetStatus != job.StatusSucceeded {
		t.Errorf("P0 #4: happy-path all-succeeded MUST flip broker-status to SUCCEEDED, got %s",
			got.targetStatus)
	}
}

// Test 6 (CAS conflict path): when FinalizeAggregateParent returns
// ErrAggregateCASConflict (revision bump mid-tick), the aggregator must
// treat this as a warn-level no-op.
func TestFinalizeAggregateParentCASConflict_LoggedAndNoFatal(t *testing.T) {
	stub := buildParentStub("parent-cas", map[string]job.Status{
		"c1": job.StatusSucceeded,
	}, map[string]bool{"c1": true})
	stub.flippedErr = domainremote.ErrAggregateCASConflict

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	// Must NOT panic, must NOT mutate stub.flipped.
	agg.Tick(context.Background())
	if _, ok := stub.flipped["parent-cas"]; ok {
		t.Error("P0 #4: aggregator must NOT mutate stub.flipped on ErrAggregateCASConflict")
	}
}

// ── NEW (2026-07-03): 4 CAS narrow-port tests below ─────────────────

// Helper: assert expectedVersion recorded equals parent.Revision.
func assertExpectedVersionIsParentRevision(t *testing.T, label string, rec scriptFlipRecord, parentJob *job.Job) {
	t.Helper()
	if rec.expectedVersion != parentJob.Revision {
		t.Errorf("P0 #4 CAS [%s]: FinalizeAggregateParent expectedVersion MUST be parent.Revision (SQL CAS fence), got %d (parent.Revision=%d)",
			label, rec.expectedVersion, parentJob.Revision)
	}
}

// countWarnMatching returns the number of Warn-level entries in the
// observer whose message contains snippet. Version-robust against the
// zap observer API drift: avoids FilterLevel / All-not-supported-methods
// and iterates All() directly. This project's zap version's
// *observer.ObservedLogs exposes All() but NOT FilterLevel.
func countWarnMatching(recorded *observer.ObservedLogs, snippet string) int {
	count := 0
	for _, e := range recorded.All() {
		if e.Level == zap.WarnLevel && strings.Contains(e.Message, snippet) {
			count++
		}
	}
	return count
}

// countAtLevel returns the number of entries logged at `level`. Used by
// the idempotent-replay test to assert ZERO Warn-level entries were
// emitted.
func countAtLevel(recorded *observer.ObservedLogs, level zapcore.Level) int {
	count := 0
	for _, e := range recorded.All() {
		if e.Level == level {
			count++
		}
	}
	return count
}

// Test 7 (NEW): mixed-outcome → broker=SUCCEEDED + parent_state=partial_success
// + errMsg empty + FinalizeAggregateParent's expectedVersion arg == parent.Revision
// (the SQL CAS-fence value pulled from the row at tick start). Real
// SQL UPDATE atomically bumps revision (revision+1) on success; the
// stub assertion pins the arg-side discipline that the consumer-side
// aggregate-flip MUST observe parent.Revision (NOT a StateMachine-
// local counter).
func TestFinalizeAggregateParent_Mixed_PreservesRevision(t *testing.T) {
	stub := buildParentStub("parent-mix-rev", map[string]job.Status{
		"c1": job.StatusSucceeded,
		"c2": job.StatusFailed,
		"c3": job.StatusSucceeded,
	}, map[string]bool{"c1": true, "c2": false, "c3": true})
	// Give parent a non-zero Revision so the assertion is meaningful.
	stub.parentJob.Revision = 7

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	rec, ok := stub.flipped["parent-mix-rev"]
	if !ok {
		t.Fatal("P0 #4 CAS: aggregator must call FinalizeAggregateParent on partial-success aggregate")
	}
	if rec.targetStatus != job.StatusSucceeded {
		t.Errorf("P0 #4 CAS: mixed-outcome (partial_success) MUST flip broker-status to SUCCEEDED, got %s",
			rec.targetStatus)
	}
	if rec.errMsg != "" {
		t.Errorf("P0 #4 CAS: SUCCEEDED flip must carry empty errMsg (no failure context), got %q",
			rec.errMsg)
	}
	assertExpectedVersionIsParentRevision(t, "mixed-outcome", rec, stub.parentJob)
}

// Test 8 (NEW): all-children-FAILED → broker=FAILED + parent_state=failed
// + errMsg carries the canonical aggregate marker literal
// "script aggregate: all child jobs definitively failed" + Revision
// CAS-fence assertion.
func TestFinalizeAggregateParent_AllFailed_PopulatesErrMsg(t *testing.T) {
	stub := buildParentStub("parent-all-fail", map[string]job.Status{
		"c1": job.StatusFailed,
		"c2": job.StatusFailed,
		"c3": job.StatusFailed,
	}, map[string]bool{"c1": false, "c2": false, "c3": false})
	stub.parentJob.Revision = 12

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	rec, ok := stub.flipped["parent-all-fail"]
	if !ok {
		t.Fatal("P0 #4 CAS: aggregator must call FinalizeAggregateParent on all-failed aggregate")
	}
	if rec.targetStatus != job.StatusFailed {
		t.Errorf("P0 #4 CAS: all-failed MUST flip broker-status to FAILED, got %s",
			rec.targetStatus)
	}
	// errMsg must carry the canonical aggregate marker (literal substring
	// match for forward-compat with future suffixes).
	if !strings.Contains(rec.errMsg, "script aggregate: all child jobs definitively failed") {
		t.Errorf("P0 #4 CAS: FAILED flip must carry aggregate marker literal, got %q",
			rec.errMsg)
	}
	assertExpectedVersionIsParentRevision(t, "all-failed", rec, stub.parentJob)
}

// Test 9 (NEW): terminal-CAS-rejection path. Stub.FinalizeAggregateParent returns
// domainremote.ErrAggregateCASConflict (simulating a concurrent tick's
// revision bump racing the WHERE revision=? UPDATE). Aggregator must:
//
//	(a) NOT mutate stub.flipped — the rejection short-circuits before
//	    FinalizeAggregateParent's record step (stub impl: flippedErr returns the
//	    error without writing to stub.flipped).
//	(b) Emit a Warn-level log with the canonical "FinalizeAggregateParent CAS conflict"
//	    snippet (audit-forensic readable on operator log streams).
//
// Uses zap/zaptest/observer to capture log entries non-destructively
// (zap.NewNop would discard them).
func TestFinalizeAggregateParent_StaleRevision_ReturnsErrAggregateCASConflict(t *testing.T) {
	stub := buildParentStub("parent-stale", map[string]job.Status{
		"c1": job.StatusSucceeded,
		"c2": job.StatusSucceeded,
	}, map[string]bool{"c1": true, "c2": true})
	stub.flippedErr = domainremote.ErrAggregateCASConflict

	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  logger,
	})
	agg.Tick(context.Background())

	if _, ok := stub.flipped["parent-stale"]; ok {
		t.Error("P0 #4 CAS: aggregator must NOT mutate stub.flipped on ErrAggregateCASConflict (no fake-success)")
	}

	// The aggregator emits a Warn-level log with the canonical message
	// snippet. The observer API in this zap version's *observer.ObservedLogs
	// does NOT expose FilterLevel (compile error in prior iteration);
	// the helper countWarnMatching iterates All() directly checking
	// both level + snippet. If the aggregator's WARN log is emitted
	// with the canonical snippet, countWarnMatching returns >= 1.
	if got := countWarnMatching(recorded, "FinalizeAggregateParent CAS conflict"); got == 0 {
		t.Error("P0 #4 CAS: aggregator must emit Warn log on ErrAggregateCASConflict (audit forensics)")
	}
}

// Test 10 (NEW): second-tick-after-finalization replay idempotence.
// Scenario: first tick succeeds (mutates stub.flipped). Second tick
// arrives with stub simulating "already-terminal" state — broker
// already at terminal so any further flip is a no-op. Aggregator must:
//
//	(a) NOT overwrite the first tick's stub.flipped record.
//	(b) Emit ZERO Warn-level log entries (idempotent replay is silent
//	    — INFO level acceptable, WARN forbidden; per godlike/07).
//
// Uses zap/zaptest/observer to assert zero warn entries.
func TestFinalizeAggregateParent_ReplayIdempotentAfterAlreadyTerminal(t *testing.T) {
	// Setup: first tick will succeed (no flippedErr, default zero value).
	stub := buildParentStub("parent-replay-second", map[string]job.Status{
		"c1": job.StatusSucceeded,
		"c2": job.StatusSucceeded,
	}, map[string]bool{"c1": true, "c2": true})
	stub.parentJob.Revision = 3

	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  logger,
	})
	agg.Tick(context.Background())

	firstRecord, ok := stub.flipped["parent-replay-second"]
	if !ok {
		t.Fatal("P0 #4 CAS: first tick must call FinalizeAggregateParent (precondition for replay scenario)")
	}
	if firstRecord.expectedVersion != stub.parentJob.Revision {
		t.Errorf("P0 #4 CAS [replay scenario]: first tick CAS-fence expectedVersion MUST be parent.Revision, got %d",
			firstRecord.expectedVersion)
	}

	// Now flip the stub to "already-terminal" state (simulating another
	// path — e.g. operator manual flip, prior tick race landed first).
	stub.flippedErr = domainremote.ErrAlreadyTerminalAggregate
	agg.Tick(context.Background())

	// stub.flipped still has exactly ONE entry (no overwrite from
	// second tick's CAS-rejection path).
	if len(stub.flipped) != 1 {
		t.Errorf("P0 #4 CAS: second tick must NOT mutate stub.flipped map; expected 1 entry, got %d",
			len(stub.flipped))
	}
	recSecond := stub.flipped["parent-replay-second"]
	if recSecond.targetStatus != firstRecord.targetStatus ||
		recSecond.errMsg != firstRecord.errMsg ||
		recSecond.expectedVersion != firstRecord.expectedVersion {
		t.Errorf("P0 #4 CAS: second tick must NOT overwrite first tick's record (CAS-rejection preserves state); got %+v, want %+v",
			recSecond, firstRecord)
	}

	// Zero Warn log entries on idempotent replay (godlike/07 silent-success).
	// Same observer-API versioning caveat: countAtLevel iterates All()
	// directly (this project lacks FilterLevel on *observer.ObservedLogs).
	if got := countAtLevel(recorded, zapcore.WarnLevel); got != 0 {
		t.Errorf("P0 #4 CAS: idempotent replay must NOT emit Warn log, got %d entries: %v",
			got, recorded.All())
	}
}

// Test 11 (FASE 4 close-out, July 2026): zero children enqueued →
// FAILED terminal immediately, without querying any child jobs. The
// aggregator short-circuits when parentResult.ChildJobIDs is empty
// after filtering. FASE 4 spec: zero children created = FAILED terminal
// (not partial_success). The pre-FASE-4 mapping conflated dispatch
// failure (zero enqueued) with partial-success (mixed terminal) —
// two semantically distinct states. The canonical terminal for "no
// children enqueued" is ScriptParentFailedTerminal (per the 5-state
// machine). This test pins that the aggregator emits the canonical
// P0 #4 aggregate marker in errMsg for audit forensics.
func TestScriptParentBrokerStatusIsFAILEDWhenZeroChildren(t *testing.T) {
	stub := buildParentStub("parent-zero", map[string]job.Status{}, map[string]bool{})

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	rec, ok := stub.flipped["parent-zero"]
	if !ok {
		t.Fatal("FASE 4: aggregator must call FinalizeAggregateParent on zero-children aggregate")
	}
	if rec.targetStatus != job.StatusFailed {
		t.Errorf("FASE 4: zero-children aggregate MUST flip broker-status to FAILED (not partial_success), got %s",
			rec.targetStatus)
	}
	if rec.result == nil {
		t.Fatal("FASE 4: result map must be captured for zero-children FAILED assertion")
	}
	ps, _ := rec.result["parent_state"].(string)
	if ps != string(ScriptParentFailedTerminal) {
		t.Errorf("FASE 4: zero-children aggregate MUST emit parent_state=%q in result, got %q",
			ScriptParentFailedTerminal, ps)
	}
	if !strings.Contains(rec.errMsg, "script aggregate: all child jobs definitively failed") {
		t.Errorf("FASE 4: zero-children FAILED flip must carry canonical aggregate marker, got %q",
			rec.errMsg)
	}
}

// Ensure the compile-time assertion still holds after Commit 3 interface
// narrowing (removed List/Complete — the aggregator doesn't use them).
var _ ScriptAggregatorJobsService = (*appjobs.Service)(nil)

// ── PR-4: child→parent doc_id/doc_link propagation (3 NEW tests) ──────
//
// Closes the canonical contract loop established in
// script_generation_item_handler.go::toScriptItemResultMap (2026-07-07
// fix): the child writes doc_link + doc_id into its result map when
// the per-item pipeline produced a Google Doc; the aggregator
// collects them into per-item maps and surfaces them in the parent
// result as child_doc_links + child_doc_ids. The 3 tests below
// pin the aggregator side of the contract.
//
// Test 12 (NEW PR-4): succeeded child with doc_link + doc_id → the
// aggregator collects them into the per-item maps and writes the
// maps to the result arg passed to FinalizeAggregateParent. The
// maps are keyed by item_id (NOT child_job_id) so downstream
// consumers can correlate with the parent's original child_job_ids.
func TestScriptParentAggregator_AggregateOne_CollectsDocFieldsFromSucceededChild(t *testing.T) {
	stub := buildParentStubWithDocs("parent-doc-ok", map[string]struct {
		Status  job.Status
		OK      bool
		DocLink string
		DocID   string
	}{
		"child-doc-1": {Status: job.StatusSucceeded, OK: true,
			DocLink: "https://docs.google.com/document/d/abc123/edit", DocID: "abc123"},
	})

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	rec, ok := stub.flipped["parent-doc-ok"]
	if !ok {
		t.Fatal("PR-4: aggregator must call FinalizeAggregateParent on succeeded child")
	}
	if rec.result == nil {
		t.Fatal("PR-4: result map must be captured for child→parent doc propagation assertion")
	}
	links, ok := rec.result["child_doc_links"].(map[string]string)
	if !ok {
		t.Fatalf("PR-4: result must contain child_doc_links map[string]string, got %T (%v)", rec.result["child_doc_links"], rec.result["child_doc_links"])
	}
	if got := links["child-doc-1"]; got != "https://docs.google.com/document/d/abc123/edit" {
		t.Errorf("PR-4: child_doc_links[child-doc-1] must be the child's doc_link, got %v", got)
	}
	ids, ok := rec.result["child_doc_ids"].(map[string]string)
	if !ok {
		t.Fatalf("PR-4: result must contain child_doc_ids map[string]string, got %T (%v)", rec.result["child_doc_ids"], rec.result["child_doc_ids"])
	}
	if got := ids["child-doc-1"]; got != "abc123" {
		t.Errorf("PR-4: child_doc_ids[child-doc-1] must be the child's doc_id, got %v", got)
	}
}

// Test 13 (NEW PR-4): FAILED child with doc fields populated in its
// result body → the aggregator MUST NOT propagate the doc fields.
// The contract is strict: only succeeded children (status=SUCCEEDED
// AND childOK=true) surface doc links to operators. A failed
// child — even one that produced a Google Doc before the rest of
// the pipeline failed — must not leak doc metadata to the parent
// (the child is a failure surface; the doc may be incomplete or
// stale).
func TestScriptParentAggregator_AggregateOne_IgnoresDocFieldsFromFailedChild(t *testing.T) {
	stub := buildParentStubWithDocs("parent-doc-fail", map[string]struct {
		Status  job.Status
		OK      bool
		DocLink string
		DocID   string
	}{
		"child-doc-fail": {Status: job.StatusFailed, OK: false,
			DocLink: "https://docs.google.com/document/d/leaked/edit", DocID: "leaked"},
	})

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	rec, ok := stub.flipped["parent-doc-fail"]
	if !ok {
		t.Fatal("PR-4: aggregator must call FinalizeAggregateParent on all-failed aggregate")
	}
	if rec.result == nil {
		t.Fatal("PR-4: result map must be captured")
	}
	if _, present := rec.result["child_doc_links"]; present {
		t.Errorf("PR-4: FAILED child must NOT contribute to child_doc_links, got %v", rec.result["child_doc_links"])
	}
	if _, present := rec.result["child_doc_ids"]; present {
		t.Errorf("PR-4: FAILED child must NOT contribute to child_doc_ids, got %v", rec.result["child_doc_ids"])
	}
}

// Test 14 (NEW PR-4): succeeded children without doc fields → the
// aggregator MUST NOT write child_doc_links / child_doc_ids keys to
// the result map. The omitempty contract at finalizeParent is
// load-bearing: empty maps stay out of the JSON wire shape so
// downstream consumers (operator dashboards, replay tooling) do
// not see noisy empty-key entries.
func TestScriptParentAggregator_FinalizeParent_OmitsMapsWhenEmpty(t *testing.T) {
	// Succeeded child with NO doc fields (using the original
	// makeScriptChildResultOK helper which emits no doc fields).
	stub := buildParentStub("parent-no-doc", map[string]job.Status{
		"c1": job.StatusSucceeded,
		"c2": job.StatusSucceeded,
	}, map[string]bool{"c1": true, "c2": true})

	agg := NewScriptParentAggregator(ScriptAggregatorDeps{
		JobsSvc: stub,
		Logger:  zap.NewNop(),
	})
	agg.Tick(context.Background())

	rec, ok := stub.flipped["parent-no-doc"]
	if !ok {
		t.Fatal("PR-4: aggregator must call FinalizeAggregateParent on all-succeeded-no-doc aggregate")
	}
	if rec.result == nil {
		t.Fatal("PR-4: result map must be captured")
	}
	if _, present := rec.result["child_doc_links"]; present {
		t.Errorf("PR-4: when no child produced a doc, child_doc_links must be OMITTED from result, got %v", rec.result["child_doc_links"])
	}
	if _, present := rec.result["child_doc_ids"]; present {
		t.Errorf("PR-4: when no child produced a doc, child_doc_ids must be OMITTED from result, got %v", rec.result["child_doc_ids"])
	}
}
