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
