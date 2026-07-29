// parent_aggregator_read_preference_test.go — PR-P1.2-SQL-DUAL-WRITE
// (July 2026, deadline 2026-08-15). The 5 tests pin the canonical
// read-side preference contract for the parent_aggregator.go::aggregateOne
// function (MUST-FIX #2 + NIT #1 from code-reviewer):
//
//  1. TestParentAggregator_AggregateOne_TypedWinsOverDisagreeingJSON
//     — when the typed column AND the JSON resultMap["parent_state"]
//     are BOTH populated AND disagree with TYPED=terminal, the typed
//     column wins (per the BACKFILL contract: typed is AUTHORITATIVE).
//
//  2. TestParentAggregator_AggregateOne_FallsBackToJSONWhenTypedEmpty
//     — when the typed column is empty (pre-P1.2 row, or concurrent
//     write in flight) the JSON resultMap["parent_state"] is used
//     (BACKFILL-window fallback contract).
//
//  3. TestParentAggregator_AggregateOne_RejectsGarbageTypedValue
//     — when the typed column is non-empty but not a known
//     voiceover.ParentState constant (writer bug or backfill-CLI
//     race), the aggregator falls back to the JSON value with a
//     Warn log (MUST-FIX #1 from code-reviewer: no silent skip
//     on garbage typed values).
//
//  4. TestParentAggregator_AggregateOne_LogsDisagreementOnNonTerminal
//     — MUST-FIX coverage gap from code-reviewer: when typed and
//     JSON disagree with BOTH non-terminal (e.g. typed="partial_success",
//     JSON="waiting_children"), the aggregator must proceed with
//     the typed value AND emit a Warn log (NIT #1 from code-reviewer:
//     no silent preference — the disagreement is a diagnostic signal).
//
//  5. TestParentAggregator_AggregateOne_ExcludesTerminalTyped
//     — when the typed column is terminal (e.g. typed="succeeded")
//     AND the JSON is empty (post-CUTOVER shape), the aggregator
//     must NOT flip the parent (terminal state cannot be re-aggregated).
//
// godlike/07 no-fake-availability: each test uses the real
// aggregateOne flow (stubAggregatorJobsService for the Get/Complete
// round-trips + a hand-built job.Job literal for the parent) so
// the test exercises the REAL read-side preference logic. Failures
// are real; passes are real. No t.Skip, no log-as-success.
package jobs

import (
	"context"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// newReadPreferenceAggregator constructs a ParentAggregator with a
// stubAggregatorJobsService that returns the given parentJob from
// Get + List (so Tick can also be exercised if needed) and tracks
// FinalizeAggregateParent calls in the `flipped` map (the
// map[string]flipRecord signature is shared with the existing
// parent_aggregator_test.go suite per godlike/06 SSOT). The
// logger is a zaptest/observer so the test can assert on the
// Warn logs emitted by the read-side preference (NIT #1 from
// code-reviewer: Warn on typed/JSON disagreement + Warn on garbage
// typed value).
//
// godlike/06 SSOT: the helper uses the existing stubAggregatorJobsService
// struct (defined in parent_aggregator_test.go) without modifying
// it — the test fits the existing test surface per godlike/07
// minimum-blast-radius (no stub refactor, no test-infra churn).
func newReadPreferenceAggregator(parentJob *job.Job, childJobs map[string]*job.Job) (*ParentAggregator, *observer.ObservedLogs) {
	core, recorded := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)
	stub := &stubAggregatorJobsService{
		parentJob: parentJob,
		childJobs: childJobs,
		completed: map[string]map[string]any{},
		flipped:   map[string]flipRecord{},
		getCalls:  []string{},
	}
	return NewParentAggregator(AggregatorDeps{
		JobsSvc: stub,
		Logger:  logger,
	}), recorded
}

// Test 1: typed column wins over disagreeing JSON (terminal case).
//
// godlike/07 no-fake-availability: the test seeds the parent with
// ParentStateTyped="succeeded" (terminal, aggregator should NOT
// re-process) + Result containing parent_state="waiting_children"
// (would normally be processed). The aggregator MUST classify the
// parent as terminal (not awaiting aggregation) and skip
// FinalizeAggregateParent. A regression that removed the typed-
// column override would cause the aggregator to process the
// parent as if it were awaiting, calling FinalizeAggregateParent
// with the (succeeded, succeeded_count=N) outcome — a silent
// re-finalize of a terminal parent.
func TestParentAggregator_AggregateOne_TypedWinsOverDisagreeingJSON(t *testing.T) {
	const parentID = "test-parent-typed-wins"
	resultJSON := []byte(`{"parent_state":"waiting_children","ok":true,"child_job_ids":["c1","c2"]}`)
	parent := &job.Job{
		ID:               parentID,
		Type:             job.TypeVoiceoverGenerate,
		Status:           job.StatusSucceeded,
		Result:           resultJSON,
		ParentStateTyped: "succeeded", // AUTHORITATIVE — terminal.
		Revision:         5,
	}
	agg, _ := newReadPreferenceAggregator(parent, nil)
	_ = agg.aggregateOne(context.Background(), *parent)
	if _, ok := agg.deps.JobsSvc.(*stubAggregatorJobsService).flipped[parentID]; ok {
		t.Errorf("aggregateOne: parent with typed column=%q (terminal) was flipped via FinalizeAggregateParent; the typed column should win and the aggregator should NOT process a terminal parent (typed/JSON disagreement regression)",
			"succeeded")
	}
}

// Test 2: JSON fallback when typed column empty (BACKFILL window
// pre-P1.2 row + concurrent-write-in-flight contract).
//
// godlike/07 no-fake-availability: the test seeds the parent with
// ParentStateTyped="" (empty, pre-P1.2 row) + Result containing
// parent_state="waiting_children". The aggregator MUST classify
// the parent as awaiting aggregation (via the JSON fallback) and
// call FinalizeAggregateParent. A regression that broke the JSON
// fallback would cause the aggregator to silently skip the
// parent — a godlike/07 no-fake-availability failure.
func TestParentAggregator_AggregateOne_FallsBackToJSONWhenTypedEmpty(t *testing.T) {
	const parentID = "test-parent-typed-empty"
	resultJSON := []byte(`{"parent_state":"waiting_children","ok":true,"child_job_ids":[]}`)
	parent := &job.Job{
		ID:               parentID,
		Type:             job.TypeVoiceoverGenerate,
		Status:           job.StatusSucceeded,
		Result:           resultJSON,
		ParentStateTyped: "", // empty — pre-P1.2 row / concurrent write
		Revision:         5,
	}
	agg, _ := newReadPreferenceAggregator(parent, nil)
	_ = agg.aggregateOne(context.Background(), *parent)
	// Zero children → finalizeParent is called with ZeroChildrenAggregateResult
	// (ParentFailed per FASE 4 close-out, July 2026) per the existing
	// aggregateOne short-circuit. The previous comment said
	// "ParentPartialSuccess" — that mapping was a pre-FASE-4 dispatch-
	// failure-as-partial-success false-positive terminal leak; FASE 4
	// spec close-out changed the canonical terminal to ParentFailed.
	if _, ok := agg.deps.JobsSvc.(*stubAggregatorJobsService).flipped[parentID]; !ok {
		t.Errorf("aggregateOne: parent with empty typed column + JSON parent_state=%q was NOT flipped via FinalizeAggregateParent; the JSON fallback should engage (BACKFILL-window contract regression)",
			"waiting_children")
	}
}

// Test 3: garbage typed value falls back to JSON with Warn log
// (MUST-FIX #1 from code-reviewer: no silent skip on garbage
// typed values).
//
// godlike/07 no-fake-availability: the test seeds the parent with
// ParentStateTyped="garbage" (a malformed value that would cause
// IsParentAwaitingAggregation to silently return false) + Result
// containing parent_state="waiting_children" (the canonical
// well-formed JSON value). The aggregator MUST fall back to the
// JSON value AND emit a Warn log naming the malformed typed
// value. A regression that either (a) silently skipped the parent
// (no log, no flip) or (b) flipped the parent with the garbage
// value (no validation) would be a godlike/07 no-fake-availability
// failure.
func TestParentAggregator_AggregateOne_RejectsGarbageTypedValue(t *testing.T) {
	const parentID = "test-parent-typed-garbage"
	resultJSON := []byte(`{"parent_state":"waiting_children","ok":true,"child_job_ids":[]}`)
	parent := &job.Job{
		ID:               parentID,
		Type:             job.TypeVoiceoverGenerate,
		Status:           job.StatusSucceeded,
		Result:           resultJSON,
		ParentStateTyped: "garbage", // malformed — not a known voiceover.ParentState
		Revision:         5,
	}
	agg, recorded := newReadPreferenceAggregator(parent, nil)
	_ = agg.aggregateOne(context.Background(), *parent)

	// Assert 1: the parent WAS flipped (the JSON fallback engaged
	// because the typed value was rejected as malformed).
	stub := agg.deps.JobsSvc.(*stubAggregatorJobsService)
	if _, ok := stub.flipped[parentID]; !ok {
		t.Errorf("aggregateOne: parent with garbage typed column + JSON parent_state=%q was NOT flipped; the JSON fallback should engage when the typed value is rejected as malformed (MUST-FIX #1 regression: silent skip on garbage typed values)",
			"waiting_children")
	}

	// Assert 2: a Warn log was emitted naming the malformed typed
	// value. Per MUST-FIX #1: a malformed typed value MUST log
	// Warn + fall back to JSON. A regression that silently swallowed
	// the malformed value would fail this assertion.
	warnLogs := recorded.FilterMessageSnippet("typed parent_state_typed has unknown value").All()
	if len(warnLogs) == 0 {
		t.Errorf("aggregateOne: no Warn log emitted for malformed typed value; MUST-FIX #1 contract violated (silent success on garbage typed value)")
	} else {
		// Assert the Warn log names the malformed typed value AND the JSON fallback.
		// MUST-FIX #3 (code-reviewer): field keys use parent_state_typed +
		// parent_state_json (the convention locked in this PR) — NOT the
		// legacy typed_column / json_fallback keys.
		foundTypedValue := false
		foundJSONFallback := false
		for _, log := range warnLogs {
			for _, field := range log.Context {
				if field.Key == "parent_state_typed" && strings.Contains(field.String, "garbage") {
					foundTypedValue = true
				}
				if field.Key == "parent_state_json" && strings.Contains(field.String, "waiting_children") {
					foundJSONFallback = true
				}
			}
		}
		if !foundTypedValue {
			t.Errorf("aggregateOne: Warn log did not name the malformed typed value (expected 'garbage' in parent_state_typed field)")
		}
		if !foundJSONFallback {
			t.Errorf("aggregateOne: Warn log did not name the JSON fallback (expected 'waiting_children' in parent_state_json field)")
		}
	}
}

// Test 4: typed/JSON disagreement logs a Warn (NIT #1 from
// code-reviewer: no silent preference — the disagreement is a
// diagnostic signal). MUST-FIX coverage gap: BOTH surfaces are
// non-terminal and disagreeing, so the aggregator must proceed
// with the typed value AND emit a Warn log.
//
// godlike/07 no-fake-availability: the test seeds the parent with
// ParentStateTyped="partial_success" (non-terminal, awaiting) +
// Result containing parent_state="waiting_children" (also awaiting,
// but disagrees). The aggregator MUST proceed with
// parent_state="partial_success" (typed wins) AND emit a Warn log.
// A regression that either (a) silently let the typed column win
// (no log) or (b) skipped the parent (silent failure) would be a
// godlike/07 no-fake-availability failure.
func TestParentAggregator_AggregateOne_LogsDisagreementOnNonTerminal(t *testing.T) {
	const parentID = "test-parent-typed-nonterminal-disagreement"
	resultJSON := []byte(`{"parent_state":"waiting_children","ok":true,"child_job_ids":[]}`)
	parent := &job.Job{
		ID:               parentID,
		Type:             job.TypeVoiceoverGenerate,
		Status:           job.StatusSucceeded,
		Result:           resultJSON,
		ParentStateTyped: "partial_success", // non-terminal — disagrees with JSON
		Revision:         5,
	}
	agg, recorded := newReadPreferenceAggregator(parent, nil)
	_ = agg.aggregateOne(context.Background(), *parent)

	// Assert 1: the parent WAS flipped (typed="partial_success" is
	// awaiting aggregation; the aggregator proceeds with the typed
	// value).
	stub := agg.deps.JobsSvc.(*stubAggregatorJobsService)
	if _, ok := stub.flipped[parentID]; !ok {
		t.Errorf("aggregateOne: parent with typed column=%q (non-terminal, disagrees with JSON=%q) was NOT flipped; the typed column should win and the aggregator should proceed with the typed value",
			"partial_success", "waiting_children")
	} else {
		// Assert the typed value was used (not the JSON value) by
		// inspecting the flipRecord.result["parent_state"].
		rec := stub.flipped[parentID]
		if ps, ok := rec.result["parent_state"].(string); !ok || ps != "partial_success" {
			t.Errorf("aggregateOne: flipped with parent_state=%q (expected %q — the typed column should win, not the JSON value)",
				rec.result["parent_state"], "partial_success")
		}
	}

	// Assert 2: a Warn log was emitted naming the typed/JSON disagreement.
	warnLogs := recorded.FilterMessageSnippet("typed parent_state_typed and JSON parent_state disagree").All()
	if len(warnLogs) == 0 {
		t.Errorf("aggregateOne: no Warn log emitted for non-terminal typed/JSON disagreement; NIT #1 contract violated (silent preference is a diagnostic signal regression)")
	}
}

// Test 5: typed column == JSON value (no disagreement) MUST NOT
// emit a Warn log (MUST-FIX #2 coverage gap from code-reviewer).
//
// godlike/07 no-fake-availability: the test seeds the parent with
// ParentStateTyped="waiting_children" (canonical) + JSON
// parent_state="waiting_children" (also canonical, agrees). The
// aggregator MUST proceed with the flip (typed wins, but no
// disagreement so no Warn). A regression that always logs a Warn
// (even on agreement) would fail this assertion — a diagnostic-
// signal regression (the operator dashboard would see a flood of
// false-positive disagreement warnings).
func TestParentAggregator_AggregateOne_NoWarnWhenTypedEqualsJSON(t *testing.T) {
	const parentID = "test-parent-typed-equals-json"
	resultJSON := []byte(`{"parent_state":"waiting_children","ok":true,"child_job_ids":[]}`)
	parent := &job.Job{
		ID:               parentID,
		Type:             job.TypeVoiceoverGenerate,
		Status:           job.StatusSucceeded,
		Result:           resultJSON,
		ParentStateTyped: "waiting_children", // agrees with JSON
		Revision:         5,
	}
	agg, recorded := newReadPreferenceAggregator(parent, nil)
	_ = agg.aggregateOne(context.Background(), *parent)

	// Assert 1: the parent WAS flipped (typed column is "waiting_children",
	// awaiting aggregation; the aggregator proceeds).
	stub := agg.deps.JobsSvc.(*stubAggregatorJobsService)
	if _, ok := stub.flipped[parentID]; !ok {
		t.Errorf("aggregateOne: parent with typed column=%q (agrees with JSON) was NOT flipped; the typed column should win and the aggregator should proceed",
			"waiting_children")
	}

	// Assert 2: NO disagreement Warn was logged (typed == JSON → no
	// disagreement). A regression that always logs Warn would fail
	// this assertion.
	disagreementWarns := recorded.FilterMessageSnippet("typed parent_state_typed and JSON parent_state disagree").All()
	if len(disagreementWarns) > 0 {
		t.Errorf("aggregateOne: %d disagreement Warn(s) logged when typed == JSON (expected 0); a Warn-on-agreement regression floods the operator dashboard with false-positive diagnostic signals",
			len(disagreementWarns))
	}
}

// Test 6: typed terminal + empty JSON excludes the parent
// (post-CUTOVER shape, where the JSON key is retired).
//
// godlike/07 no-fake-availability: the test seeds the parent with
// ParentStateTyped="succeeded" (terminal) + Result with NO
// parent_state key (post-CUTOVER shape). The aggregator MUST
// classify the parent as terminal (typed wins, JSON fallback is
// not engaged because the JSON is empty) and NOT flip the parent.
// A regression that either (a) flipped the parent (the typed
// override failed) or (b) fell back to JSON incorrectly (the
// typed override ignored) would be a godlike/07 no-fake-availability
// failure.
func TestParentAggregator_AggregateOne_ExcludesTerminalTyped(t *testing.T) {
	const parentID = "test-parent-typed-terminal-empty-json"
	resultJSON := []byte(`{"ok":true,"child_job_ids":["c1"]}`) // NO parent_state key
	parent := &job.Job{
		ID:               parentID,
		Type:             job.TypeVoiceoverGenerate,
		Status:           job.StatusSucceeded,
		Result:           resultJSON,
		ParentStateTyped: "succeeded", // AUTHORITATIVE — terminal.
		Revision:         5,
	}
	agg, _ := newReadPreferenceAggregator(parent, nil)
	_ = agg.aggregateOne(context.Background(), *parent)
	if _, ok := agg.deps.JobsSvc.(*stubAggregatorJobsService).flipped[parentID]; ok {
		t.Errorf("aggregateOne: parent with typed column=%q (terminal) + empty JSON was flipped via FinalizeAggregateParent; the typed column should win and the aggregator should NOT process a terminal parent (post-CUTOVER-shape regression)",
			"succeeded")
	}
}
