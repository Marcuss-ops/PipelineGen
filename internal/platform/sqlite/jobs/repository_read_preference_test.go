// repository_read_preference_test.go — PR-P1.2-SQL-DUAL-WRITE
// (July 2026, deadline 2026-08-15). The 4 tests pin the canonical
// read-side contract for the typed parent_state_typed column:
//
//  1. TestScanJobColumns_PopulatesParentStateTyped — scanJobColumns
//     populates j.ParentStateTyped from the parent_state_typed column
//     (the SSOT seam: every Job read now carries the typed state).
//  2. TestListAwaitingAggregation_MatchesTypedColumn — a row with
//     typed column set + JSON key missing/empty is FOUND by the
//     list query (the typed column is the AUTHORITATIVE source).
//  3. TestListAwaitingAggregation_FallsBackToJSONKey — a pre-P1.2
//     row with empty typed column + JSON key set is FOUND by the
//     list query (the BACKFILL window OR-fallback contract).
//  4. TestListAwaitingAggregation_ExcludesNonMatching — a row with
//     typed column = 'succeeded' (terminal) is NOT found by the
//     list query (the WHERE clause is strictly scoped to
//     'waiting_children').
//
// godlike/06 SSOT: the typed column name lives ONLY in
// internal/application/voiceover/jobs/parent_aggregator_state.go::JobParentStateColumn
// (canonical) + the SQL mirror
// internal/platform/sqlite/jobs/repository_lifecycle.go::parentStateTypedColumn
// (package-private). Both constants MUST equal "parent_state_typed"
// (the cross-package drift test was DROPPED per godlike/07 minimum-
// blast-radius — see repository_lifecycle_dualwrite_test.go header).
//
// godlike/07 no-fake-availability: each test asserts a real SQL
// round-trip (seed → ListAwaitingAggregation → assert the returned
// job has the expected ParentStateTyped value). Failures are real;
// passes are real. No t.Skip, no log-as-success, no white-box mocks.
package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
)

// seedParentStateTypedJob inserts a job with the typed parent_state_typed
// column set to typedValue and the JSON resultMap["parent_state"] set
// to jsonValue. The 2-value surface lets the read-preference tests
// distinguish between "typed column wins" and "JSON fallback" cases.
//
// Status defaults to "SUCCEEDED" (matches the pre-aggregator terminal
// state) so the ListAwaitingAggregation WHERE clause's status filter
// passes. type defaults to "test.voiceover.generate" so the
// ListAwaitingAggregation type filter passes when called with that
// type.
func seedParentStateTypedJob(t *testing.T, db *sql.DB, typedValue, jsonValue string) string {
	t.Helper()
	jobID := fmt.Sprintf("job_readpref_%d", time.Now().UnixNano())
	// The JSON result is either {"parent_state": "<jsonValue>"} (when
	// jsonValue != "") or {"ok": true} (when jsonValue == "" — pre-P1.2
	// row shape, no parent_state key at all).
	var resultJSON string
	if jsonValue != "" {
		resultJSON = fmt.Sprintf(`{"parent_state":%q,"ok":true}`, jsonValue)
	} else {
		resultJSON = `{"ok":true}`
	}
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO jobs (id, type, status, priority, project, video_name, active_key,
			correlation_id, payload_json, result_json, progress, error, retry_count, max_retries,
			worker_id, lease_id, lease_expiry, created_at, updated_at, started_at, completed_at,
			cancelled_at, parent_state_typed, revision)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		jobID, "test.voiceover.generate", "SUCCEEDED", 0, "test-project", "test-video", "",
		"corr-test", "{}", resultJSON, 100, "", 0, 3,
		"", "", nil,
		"2026-07-05T00:00:00Z", "2026-07-05T00:00:00Z", "2026-07-05T00:00:00Z", "2026-07-05T00:00:00Z",
		nil, typedValue, 1)
	if err != nil {
		t.Fatalf("seed parent_state_typed job %q: %v", jobID, err)
	}
	return jobID
}

// TestScanJobColumns_PopulatesParentStateTyped pins the SSOT seam:
// every Job returned by the broker carries the typed parent_state_typed
// value via the canonical scanJobColumns helper. This is the
// contract the aggregator's read-side preference depends on.
//
// godlike/07 no-fake-availability: real SQL round-trip; the
// assertion fails closed if scanJobColumns drops the typed column
// (e.g. a future refactor that removes the column from
// jobColumns). The aggregator would then read "" instead of the
// authoritative value → silent split-brain state.
func TestScanJobColumns_PopulatesParentStateTyped(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())

	const typedValue = "waiting_children"
	jobID := seedParentStateTypedJob(t, db, typedValue, "")

	got, err := store.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Get %q: %v", jobID, err)
	}
	if got.ParentStateTyped != typedValue {
		t.Errorf("scanJobColumns: expected ParentStateTyped=%q, got %q (typed column was dropped from the scan list — aggregator will read empty string + fall back to JSON during the BACKFILL window, missing the typed-column authoritative source)",
			typedValue, got.ParentStateTyped)
	}
}

// TestListAwaitingAggregation_MatchesTypedColumn pins the BACKFILL-
// window OR-fallback contract: a row with the typed column set + the
// JSON key missing is FOUND by the list query. This is the
// post-CUTOVER case (forward-pointer) where the JSON key is
// retired but the typed column carries the state.
//
// godlike/07 no-fake-availability: real SQL round-trip. The
// assertion fails closed if the WHERE clause is strictly
// json_extract-only (a regression that would silently miss
// post-CUTOVER rows).
func TestListAwaitingAggregation_MatchesTypedColumn(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())

	// Seed: typed column = "waiting_children", JSON key MISSING
	// (post-CUTOVER shape: writer no longer writes the JSON key).
	jobID := seedParentStateTypedJob(t, db, "waiting_children", "")

	jobs, err := store.ListAwaitingAggregation(context.Background(), "test.voiceover.generate", 10)
	if err != nil {
		t.Fatalf("ListAwaitingAggregation: %v", err)
	}

	found := false
	for _, j := range jobs {
		if j.ID == jobID {
			found = true
			if j.ParentStateTyped != "waiting_children" {
				t.Errorf("matched job %q: expected typed column %q, got %q", jobID, "waiting_children", j.ParentStateTyped)
			}
			break
		}
	}
	if !found {
		t.Errorf("ListAwaitingAggregation: row with typed column set + JSON key missing was NOT found (the WHERE clause is strictly json_extract-only — post-CUTOVER rows are silently dropped; the OR-fallback contract is broken)")
	}
}

// TestListAwaitingAggregation_FallsBackToJSONKey pins the
// BACKFILL-window OR-fallback contract: a pre-P1.2 row with empty
// typed column + JSON key set is FOUND by the list query. This is
// the pre-BACKFILL-CLI case (forward-pointer) where existing rows
// have not yet been backfilled to the typed column.
//
// godlike/07 no-fake-availability: real SQL round-trip. The
// assertion fails closed if the WHERE clause drops the JSON
// fallback (a regression that would silently miss pre-P1.2 rows
// during the BACKFILL transition window).
func TestListAwaitingAggregation_FallsBackToJSONKey(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())

	// Seed: typed column = "" (pre-P1.2 row, default), JSON key
	// = "waiting_children" (the legacy writer shape).
	jobID := seedParentStateTypedJob(t, db, "", "waiting_children")

	jobs, err := store.ListAwaitingAggregation(context.Background(), "test.voiceover.generate", 10)
	if err != nil {
		t.Fatalf("ListAwaitingAggregation: %v", err)
	}

	found := false
	for _, j := range jobs {
		if j.ID == jobID {
			found = true
			if j.ParentStateTyped != "" {
				t.Errorf("matched job %q: expected typed column empty (pre-P1.2 shape), got %q", jobID, j.ParentStateTyped)
			}
			break
		}
	}
	if !found {
		t.Errorf("ListAwaitingAggregation: pre-P1.2 row with empty typed column + JSON key set was NOT found (the OR-fallback was removed; pre-P1.2 rows are silently dropped during the BACKFILL transition window)")
	}
}

// TestListAwaitingAggregation_ExcludesNonMatching pins the
// WHERE-clause scope: only rows with parent_state = 'waiting_children'
// are matched (NOT succeeded/failed/partial_success). The typed
// column is a strict superset of the JSON match — terminal states
// must NOT be in the awaiting-aggregation set.
//
// godlike/07 no-fake-availability: the typed column is the
// AUTHORITATIVE source; a regression that matches 'succeeded'
// would silently re-process terminal parents.
func TestListAwaitingAggregation_ExcludesNonMatching(t *testing.T) {
	db := newBrokerTestDB(t)
	store := NewSQLiteStore(db, zap.NewNop())

	// Seed: typed column = "succeeded" (terminal), JSON key
	// would be "waiting_children" (legacy, pre-CUTOVER). The
	// row should NOT be matched because the typed column wins
	// (and it's "succeeded", not "waiting_children").
	jobID := seedParentStateTypedJob(t, db, "succeeded", "waiting_children")

	jobs, err := store.ListAwaitingAggregation(context.Background(), "test.voiceover.generate", 10)
	if err != nil {
		t.Fatalf("ListAwaitingAggregation: %v", err)
	}

	for _, j := range jobs {
		if j.ID == jobID {
			t.Errorf("ListAwaitingAggregation: row with typed column=%q (terminal) was matched (the typed column is correctly authoritative but the WHERE clause is over-matching terminal states — aggregator will re-process terminal parents)",
				"succeeded")
		}
	}
}
