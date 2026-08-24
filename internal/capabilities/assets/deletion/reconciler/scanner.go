// Package reconciler — scanner.go (Blocco 3.2 commit 2/2, June 2026)
//
// Pure classification function: StuckRow → ClassifyResult.
//
// Mirrors qdrant/reconciler/scanner.go's "no IO, no time.Now side
// effects" test-friendly invariant. Classify consumes rows only;
// the service layer (service.go) is responsible for the IO and
// time.Now side effects.
//
// Classification rules (by row.State / row.LifecycleState):
//
//	DELETE_REQUESTED      → ActionRequeueDrive
//	DRIVE_DELETE_PENDING  → ActionRequeueIndex
//	INDEX_DELETE_PENDING  → ActionRequeueIndex
//	(every other)         → Skipped (reason=unknown_state)
//
// "Unknown state" is reachable only via production bugs (a row in
// a state not in the 9 canonical LifecycleState values). The
// service-layer classifies this as Skipped + logs to operator
// dashboards via the metrics port so the row gets operator
// attention; it's deliberately NOT re-emitted through the
// outbox layer (operating on an unknown state could produce
// outbox rows with mismatched event_key shapes).
package assets

// Classify decides the RepairAction for a single StuckRow. Pure
// function — no IO, no time.Now — test-friendly. The Skip field is
// populated only when Action is empty; callers ignore Skip when
// Action is non-empty.
//
// Returned values:
//   - Row:                 the input row (passthrough for ergonomic
//     collector-style dispatch loops)
//   - Action:              ActionRequeueDrive | ActionRequeueIndex | ""
//   - Skip:                "" when Action is non-empty; otherwise the
//     human-readable skip reason
//
// The classify "skipped" reason uses the lowercased lifecycle_state
// value for unambiguous operator-dashboard grep:
//
//	dead_row, terminal_deleted, etc.
func Classify(row StuckRow) ClassifyResult {
	switch row.State {
	case "DELETE_REQUESTED":
		return ClassifyResult{Row: row, Action: ActionRequeueDrive}
	case "DRIVE_DELETE_PENDING", "INDEX_DELETE_PENDING":
		return ClassifyResult{Row: row, Action: ActionRequeueIndex}
	default:
		return ClassifyResult{
			Row:  row,
			Skip: "unknown_state:" + row.State,
		}
	}
}
