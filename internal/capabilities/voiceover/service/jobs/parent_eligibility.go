// Package jobs — parent_eligibility.go
// (PR-VO-PARENT-AGGREGATOR-SPLIT, P0 #4 in VO-DECOMPOSITION-2026-07-04, deadline 2026-08-01).
//
// parent_eligibility.go is the SINGLE canonical owner of the
// parent-aggregator eligibility-check + terminal-child cache logic
// per godlike/06 SSOT (one owner per fact). It is the
// thin-mechanical extraction of the cache + gate logic from
// parent_aggregator.go, with the §15.2 (July 2026) cache contract
// preserved VERBATIM.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - this file owns: cachedChildTerminalState struct +
//     loadCachedTerminalChild / storeCachedTerminalChild /
//     clearCachedTerminalChildren cache helpers +
//     IsParentAwaitingAggregation gate +
//     ZeroChildrenAggregateResult short-circuit helper.
//   - parent_aggregator.go owns: ParentAggregator struct +
//     NewParentAggregator + Start + Tick + aggregateOne + finalizeParent.
//   - parent_state_machine.go owns: domainToVoiceoverParentState
//     (the 5-state → 4-state wire-shape mapping).
//   - parent_aggregator_state.go owns: the P1.2 typed column
//     constants + dual-write documentation.
//
// godlike/07 minimal-blast-radius: pure code-motion. The §15.2
// cache contract (cache only TRULY terminal children; cache is
// cleared on finalize) is preserved EXACTLY. The Required flag from
// the child payload is preserved in the cache so REQUIRED-failed
// children are not downgraded to optional on retry ticks.
package jobs

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// cachedChildTerminalState records the terminal state of a child
// from the previous tick so the retry tick can skip re-querying it
// via Get(). Only terminal children are cached; non-terminal children
// are always re-queried. The cache is cleared when the parent is
// finalised (via FinalizeAggregateParent) or when the aggregator restarts.
//
// §15.2 (July 2026) contract — preserved verbatim from the
// pre-PR-VO-PARENT-AGGREGATOR-SPLIT parent_aggregator.go:
//   - Only status.IsTerminal() children are cached.
//   - RETRY_WAIT/RUNNING/LEASED/etc. are NEVER cached (would
//     mask a still-in-flight child as terminal on the retry tick).
//   - The Required flag is preserved in the cache so REQUIRED-failed
//     children are not downgraded to optional on retry ticks.
//   - The cache is cleared when the parent is finalised OR when
//     the aggregator restarts (both paths covered).
type cachedChildTerminalState struct {
	status   job.Status
	required bool
	errStr   string
}

// loadCachedTerminalChild returns the cached terminal state for a
// (parent, child) pair, if present. Returns (state, true) on hit,
// (zero, false) on miss. The caller MUST verify `status.IsTerminal()`
// is still true on the cached value before trusting it (the cache
// is invalidated only on finalize or restart; a child that was
// terminal at cache time may have been re-queued in pathological
// scenarios, though this is gated by the SQLite broker's
// terminal-immutability contract).
func loadCachedTerminalChild(
	cache map[string]map[string]cachedChildTerminalState,
	parentJobID, childID string,
) (cachedChildTerminalState, bool) {
	if cache == nil {
		return cachedChildTerminalState{}, false
	}
	parentCache, ok := cache[parentJobID]
	if !ok {
		return cachedChildTerminalState{}, false
	}
	state, ok := parentCache[childID]
	return state, ok
}

// storeCachedTerminalChild records a terminal child in the cache.
// The cache map is lazily initialised (nil-safe). Only the caller
// should invoke this AFTER verifying status.IsTerminal() (the cache
// contract is "terminal children only").
func storeCachedTerminalChild(
	cache map[string]map[string]cachedChildTerminalState,
	parentJobID, childID string,
	state cachedChildTerminalState,
) {
	if cache[parentJobID] == nil {
		cache[parentJobID] = make(map[string]cachedChildTerminalState)
	}
	cache[parentJobID][childID] = state
}

// clearCachedTerminalChildren removes the per-parent cache entry.
// Called by the aggregator's finalizeParent after a successful OR
// failed FinalizeAggregateParent attempt (the cached state is no
// longer needed regardless of outcome: success, idempotent replay,
// or CAS conflict).
func clearCachedTerminalChildren(
	cache map[string]map[string]cachedChildTerminalState,
	parentJobID string,
) {
	if cache == nil {
		return
	}
	delete(cache, parentJobID)
}

// IsParentAwaitingAggregation is the canonical gate that decides
// whether a parent job should be processed by the aggregator's
// Tick. A parent is "awaiting aggregation" iff its
// VoiceoverParentResult.ParentState is one of waiting_children
// or partial_success.
//
// PR-VO-PARENT-AGGREGATOR-SPLIT (P0 #4): the gate logic was
// previously inline at the top of aggregateOne; extracted here
// so the eligibility surface is testable in isolation.
//
// The gate is a thin wrapper around the canonical method on
// VoiceoverParentResult (defined in result_dto.go) so the
// one-canonical-owner-per-fact contract is preserved: the
// enum + IsAwaitingAggregation method live in the DTO file;
// this wrapper is the aggregator-side projection.
func IsParentAwaitingAggregation(pr *VoiceoverParentResult) bool {
	if pr == nil {
		return false
	}
	return pr.IsAwaitingAggregation()
}

// ZeroChildrenAggregateResult is the canonical aggregate for the
// zero-children short-circuit. When a parent has no enqueued
// children (e.g. all child enqueues failed at dispatch time), the
// canonical aggregate is voiceover.ParentFailed with TotalChildren=0.
// The aggregator's aggregateOne short-circuits to this state without
// constructing a domain StateMachine (which requires ≥1 child).
//
// FASE 4 (July 2026) spec close-out: zero children created = FAILED
// terminal (not partial_success). The pre-FASE-4 mapping
// (ParentPartialSuccess) was a “no children complete is partial”
// pre-P0.5 audit pinning that conflated dispatch-failure (zero
// enqueued) with partial-success (mixed terminal). The two states
// are SEMANTICALLY DISTINCT: dispatch-failure is a true terminal
// (no children will EVER run); partial-success is a mixed-terminal
// state where some children succeeded. Mapping zero-enqueued
// to partial-success was a false-positive terminal leak: the
// operator dashboard treated "0 enqueued" as "some succeeded",
// masking the dispatch bug. Per FASE 4 spec, this branch
// correctly maps to ParentFailed (canonical "all children
// definitively failed" terminal per parent_state.go:62 + 78).
//
// godlike/07 fail-closed: this helper exists so the zero-children
// branch is a single named surface, not an inline literal in
// aggregateOne. Future contributors MUST NOT re-introduce the
// pre-FASE-4 ParentPartialSuccess mapping — it would re-open the
// dispatch-failure-as-partial-success false-positive terminal.
func ZeroChildrenAggregateResult() VoiceoverAggregateResult {
	return VoiceoverAggregateResult{
		ParentState:         voiceover.ParentFailed,
		TotalChildren:       0,
		StateMachineVersion: 0,
	}
}

// logCacheHit emits the canonical debug log for a cache hit on a
// previously-terminal child. Centralised here so the log line
// format is consistent across callers (per §15.2 audit-pin
// discipline).
func logCacheHit(log *zap.Logger, parentJobID, childID, cachedStatus string) {
	log.Debug("ParentAggregator: skipping Get for already-terminal child (cache hit)",
		zap.String("parent_job_id", parentJobID),
		zap.String("child_job_id", childID),
		zap.String("cached_status", cachedStatus))
}
