// Package jobs — parent_aggregator_state.go
// (PR-VO-PARENT-AGGREGATOR-SPLIT, P0 #4 in VO-DECOMPOSITION-2026-07-04, deadline 2026-08-01)
// (PR-VO-PARENT-STATE-COLUMN, P1.2 typed column migration, deadline 2026-07-25).
//
// parent_aggregator_state.go is the SINGLE canonical owner of the
// P1.2 typed-column migration contract for parent state. It
// declares the typed column name + the dual-write contract that
// the SQL/infrastructure layer is expected to implement.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - this file owns: the typed column name + the dual-write
//     contract documentation for PR-VO-PARENT-STATE-COLUMN (P1.2).
//   - The voiceover.ParentState enum lives in
//     internal/capabilities/voiceover/parent_state.go (STAYS THERE).
//   - The SQL migration that adds the typed column lives in
//     migrations/sqlite/129_add_parent_state_typed_to_jobs.sql
//     (canonical SSOT for the schema change).
//   - parent_aggregator.go owns the orchestrator (Tick, aggregateOne,
//     finalizeParent). The aggregator's finalizeParent continues
//     to write the JSON key "parent_state" in resultMap for
//     back-compat; the SQL layer is expected to read that key and
//     dual-write to the typed column in the SAME transaction.
//   - parent_eligibility.go owns the cache + gate logic.
//   - parent_state_machine.go owns the domain → voiceover mapping.
//
// godlike/07 minimal-blast-radius: the EXPAND phase adds the
// typed column + documents the contract; the BACKFILL phase
// (SQL layer dual-write implementation) + CUTOVER phase (drop
// the JSON key reading in readers) are forward-pointers below.
package jobs

import (
	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// JobParentStateColumn is the SQL column name for the typed
// parent_state column added by PR-VO-PARENT-STATE-COLUMN (P1.2).
// The column is TEXT, byte-equivalent with the voiceover.ParentState
// string values ("waiting_children" / "partial_success" /
// "failed" / "succeeded").
//
// godlike/06 SSOT: this constant is the SINGLE canonical
// reference to the column name. All readers + writers MUST use
// this constant — string literals like "parent_state_typed" in
// raw SQL are FORBIDDEN.
const JobParentStateColumn = "parent_state_typed"

// P1.2 dual-write contract (canonical):
//
// EXPAND phase (shipped 2026-07-04 via commit 21197634 /
// PR-VO-PARENT-STATE-COLUMN + 2026-07-05 via commit __PENDING__ /
// PR-P1.2-SQL-DUAL-WRITE):
//  1. Writers (parent_aggregator.go::finalizeParent +
//     generate_handler.go:267) CONTINUE to write
//     resultMap["parent_state"] = string(voiceover.ParentState) in
//     the JSON result column. This preserves back-compat for
//     existing readers.
//  2. The SQL layer implementation of FinalizeAggregateParent
//     (in internal/platform/sqlite/jobs/repository_lifecycle.go)
//     reads resultMap["parent_state"] and writes the same value to
//     the JobParentStateColumn typed column in the SAME transaction.
//     A mid-txn crash rolls back BOTH writes (atomicity preserved).
//  3. The typed column is the AUTHORITATIVE source going forward.
//     Readers prefer the typed column over the JSON key with JSON
//     fallback during the BACKFILL window.
//
// BACKFILL phase (shipped 2026-07-05 via commit __PENDING__ /
// PR-P1.2-SQL-DUAL-WRITE):
//   - Read-side: ListAwaitingAggregation's WHERE clause matches
//     BOTH parent_state_typed = 'waiting_children' AND
//     json_extract(result_json,'$.parent_state') = 'waiting_children'
//     (OR-fallback). Aggregator's aggregateOne overrides
//     parentResult.ParentState from j.ParentStateTyped when
//     non-empty (prefer typed, fall back to JSON).
//   - A one-shot backfill CLI migrates existing rows: reads
//     resultMap["parent_state"] from the JSON column + writes
//     the value to JobParentStateColumn. The CLI is forward-pointer
//     PR-VO-PARENT-STATE-BACKFILL-CLI (deadline TBD post-CUTOVER).
//     Until it lands, the read-side OR-fallback ensures pre-P1.2
//     rows continue to be found by the aggregator.
//
// CUTOVER phase (forward-pointer, deadline TBD):
//   - Writers stop writing the JSON key resultMap["parent_state"].
//   - Readers prefer the typed column; the JSON key reading is
//     kept as a fallback for the transition window.
//   - The JSON key is eventually removed from the wire shape.
//   - The ListAwaitingAggregation OR-fallback is REMOVED (only
//     the typed column is matched).
//
// forward-pointer: the CUTOVER wave is forward-pointer
// PR-P1.2-CUTOVER (deadline TBD, post-backfill-CLI).
// The backfill CLI is forward-pointer PR-VO-PARENT-STATE-BACKFILL-CLI
// (deadline TBD, post-CUTOVER).
//
// Note: JobParentStateColumn is exported (capital J), so Go's
// compiler does NOT dead-code-eliminate it. No `var _ = ...`
// pin is needed.

// reconcileParentState applies the P1.2 READ-SIDE PREFERENCE to a decoded
// parent result (GOP-11 extraction; logic and log keys preserved verbatim).
//
// The typed parent_state_typed column (added by migration 129,
// populated atomically by repository_lifecycle.go::FinalizeAggregateParent)
// is the AUTHORITATIVE source going forward. During the BACKFILL
// window, override the JSON-derived parentResult.ParentState with
// the typed-column value when it is non-empty AND well-formed.
// The JSON fallback (parentResult.ParentState) covers pre-P1.2
// rows + concurrent writes in flight (the typed column is empty
// until the FinalizeAggregateParent UPDATE commits) + malformed
// typed values (a writer bug or a backfill-CLI race that wrote
// an unknown string).
//
// godlike/06 SSOT (one canonical owner per fact): the typed
// column name lives ONLY in
// internal/capabilities/voiceover/jobs/parent_aggregator_state.go::JobParentStateColumn
// — the SQL mirror (internal/platform/sqlite/jobs/parentStateTypedColumn)
// is package-private. Both must agree (per the cross-package
// SSOT discipline; the explicit drift test was DROPPED per
// godlike/07 minimum-blast-radius — see repository_lifecycle_dualwrite_test.go).
//
// godlike/07 no-fake-availability (MUST-FIX #1 from code-reviewer):
// validate the typed-column value is a KNOWN voiceover.ParentState
// constant before overriding. An unknown value (e.g. "garbage"
// from a writer bug) would otherwise cause
// IsParentAwaitingAggregation to silently return false, the
// aggregator to silently skip the parent, and no log/counter to
// surface the issue. The validation is a 4-value whitelist
// (the canonical voiceover.ParentState value space) — anything
// outside it falls back to the JSON value with a Warn log so
// the operator dashboard can alert.
//
// godlike/07 NIT #1: when BOTH surfaces are populated AND
// disagree (e.g. typed="failed", JSON="waiting_children" from a
// concurrent writer or a stale reader view), the typed column
// silently wins. This is the correct precedence per the
// contract, but the disagreement is a diagnostic signal of a
// racing writer or a backfill-CLI bug. Log a Warn (no error
// return — the typed column is authoritative).
//
// Field-key convention (MUST-FIX #3 from code-reviewer, PR-P1.2-SQL-DUAL-WRITE):
// the 2 new Warn field keys use `parent_state_typed` and
// `parent_state_json` to match the existing aggregator log
// format (`zap.String("parent_state", ...)` in finalizeParent).
// Operator dashboards can group by field rather than by the
// legacy ad-hoc `typed_column` / `json_field` / `json_fallback`
// keys.
func reconcileParentState(log *zap.Logger, j job.Job, parentResult *VoiceoverParentResult) {
	if j.ParentStateTyped == "" {
		return
	}
	if isKnownTypedParentState(j.ParentStateTyped) {
		if parentResult.ParentState != "" && parentResult.ParentState != j.ParentStateTyped {
			// Typed/JSON disagreement — log a Warn, then typed wins.
			log.Warn("ParentAggregator: typed parent_state_typed and JSON parent_state disagree; typed column wins per BACKFILL contract",
				zap.String("parent_job_id", j.ID),
				zap.String("parent_state_typed", j.ParentStateTyped),
				zap.String("parent_state_json", parentResult.ParentState))
		}
		parentResult.ParentState = j.ParentStateTyped
		return
	}
	// Malformed typed column — log Warn + fall back to JSON
	// (the existing parentResult.ParentState from the
	// json.Unmarshal in decodeParentResult).
	log.Warn("ParentAggregator: typed parent_state_typed has unknown value; falling back to JSON resultMap[parent_state]",
		zap.String("parent_job_id", j.ID),
		zap.String("parent_state_typed", j.ParentStateTyped),
		zap.String("parent_state_json", parentResult.ParentState))
}
