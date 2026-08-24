// Package enrichment — stateachine.go implements the canonical typed
// state-machine wrapper for the media_assets.enrich_state column
// (PR-ENRICHMENT-STATE-MACHINE, July 2026, godlike/06 SSOT).
//
// The wrapper consults the canonical validEdges closed-set before
// invoking the repository port. Direct calls to
// EnrichRepositoryPort.SetEnrichState bypass Transition validation
// (intentional — production concrete writers go through Transition;
// test fixtures can short-circuit the validation for golden-path
// coverage). The wrapper is the single owner of validEdges mutation
// history — every status change flows through it so the YAML+code
// audit trail stays consistent across the godlike/06 SSOT contract.
//
// State machine (canonical validEdges closed-set):
//
//	       ┌─→ ENRICHING ─┬─→ ENRICHED   (terminal success)
//	       │               └─→ FAILED     (terminal operator-must-intervene)
//	PENDING ┤
//	       │                ...
//	       (initial sentinel stamped by canonical ingest path)
//	       │
//	FAILED ─┴─→ ENRICHING   (admin-reindex reset; godlike/07 explicit-
//	                         retry-via-admin forward-pointer; the
//	                         VLM sweeper does NOT auto-retry on FAILED)
//
//	ENRICHING → FAILED is invalid (only ENRICHED or terminal FAILED
//	is allowed from ENRICHING; godlike/07 typed-error contract
//	surfaces this as ErrIllegalEnrichTransition). MarkEnriched +
//	MarkFailed are convenience helpers that internally call
//	Transition(_, ENRICHING, ENRICHED) / Transition(_, ENRICHING,
//	FAILED) respectively.
package enrichment

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// validEdges is the canonical closed-set of legal state-machine
// transitions per godlike/06 SSOT (one owner per fact: this map is
// the single source of truth for "what legal transitions exist"; the
// typed-error envelope ErrIllegalEnrichTransition surfaces rejected
// edges). Adding a new edge requires:
//
//  1. Adding the entry below.
//  2. Adding a corresponding TDD test for the now-legal edge.
//  3. Adding a usage_metric site or godlike/07 typed-error envelope
//     contract justification.
//
// Removal of an edge is a godlike/07 CUTOVER/CONTRACT wave candidate
// (the deprecation record must carry a removal deadline + a
// compatibility_test + a usage_metric, per architecture/deprecations.yaml).
var validEdges = map[asset.EnrichState]map[asset.EnrichState]bool{
	asset.EnrichStatePending: {
		asset.EnrichStateEnriching: true,
		// PENDING→PENDING is invalid (idempotent no-op write would
		// create noise in the enrich_state_updated_at column); the
		// typed-error envelope rejects this so callers guard against
		// accidental double-stamps (e.g. an ingest retry that
		// mistakenly re-emits MarkPending on an already-PENDING row).
	},
	asset.EnrichStateEnriching: {
		asset.EnrichStateEnriched: true,
		asset.EnrichStateFailed:   true,
	},
	// FAILED is a sink — terminal-non-retryable per godlike/07
	// no-fake-availability. The only recovery path is operator-
	// triggered reset via the reindex admin endpoint
	// (architecture/current.yaml#PR-ENRICHMENT-STATE-BACKFILL
	// forward-pointer), which lands the row back to PENDING; the
	// VLM sweeper then claims PENDING→ENRICHING on the next tick.
	// A direct FAILED→ENRICHING edge would defeat that explicit-
	// retry-via-admin discipline (a sweep tick would silently
	// retry failed rows without operator intervention, masking
	// the no-fake-availability contract). Self-loop FAILED→FAILED
	// is invalid (idempotent no-op write would be a silent bug
	// surfacing later as a stale enrich_state_updated_at).
	// ENRICHED is a sink — terminal success has no further edge.
}

// EnrichStateMachine is the canonical concrete implementation of
// EnrichStateMachinePort (godlike/06 SSOT: ONE CONCRETE per
// capability; consumers wire *EnrichStateMachine directly via
// composition root + adapters-handler-factory pattern).
type EnrichStateMachine struct {
	repo EnrichRepositoryPort
}

// NewEnrichStateMachine constructs the typed state-machine wrapper
// (Pattern 0 fail-closed constructor). Returns
// ErrEnrichStateNotWired when the repository port is nil so
// composition-root wiring never accidentally skips the validation.
func NewEnrichStateMachine(repo EnrichRepositoryPort) (*EnrichStateMachine, error) {
	if repo == nil {
		return nil, ErrEnrichStateNotWired
	}
	return &EnrichStateMachine{repo: repo}, nil
}

// Transition validates from→to via the canonical validEdges set,
// then performs the atomic column flip via the repository port.
// Returns ErrIllegalEnrichTransition (typed envelope via errors.As)
// when the requested edge is not in validEdges. Returns
// ErrEnrichStateMissing (errors.Is probe-friendly) when the
// repository port surfaces a row-missing error during the column
// flip — godlike/07 typed-error contract requires the wrapper to
// remap the SQL primitive's fmt.Errorf format-string into a stable
// sentinel so callers can probe without parsing error strings.
func (m *EnrichStateMachine) Transition(ctx context.Context, assetID string, from, to asset.EnrichState) error {
	if assetID == "" {
		return ErrEnrichAssetIDRequired
	}
	if !from.Valid() {
		return &IllegalEnrichTransitionError{
			From: from,
			To:   to,
		}
	}
	if !to.Valid() {
		return &IllegalEnrichTransitionError{
			From: from,
			To:   to,
		}
	}
	dests, ok := validEdges[from]
	if !ok || !dests[to] {
		return &IllegalEnrichTransitionError{
			From: from,
			To:   to,
		}
	}
	if err := m.repo.SetEnrichStateIfCurrent(ctx, assetID, from, to); err != nil {
		// Remap the SQL primitive's row-missing/current-state-mismatch
		// fmt.Errorf into the canonical typed sentinel (godlike/07).
		// The probe format is "clips.SetEnrichStateIfCurrent(<id>,
		// <from>, <to>): asset row missing or current state mismatch"
		// (clips_enrich_state.go) — stable enough to string-match for
		// the remap without coupling to internal format details.
		if strings.Contains(err.Error(), "asset row missing or current state mismatch") {
			return ErrEnrichStateMissing
		}
		return err
	}
	return nil
}

// MarkPending stamps PENDING on a freshly-ingested row. Bypasses
// the from-state check because the canonical ingest path stamps
// PENDING unconditionally (the row is brand-new — there is no
// prior state to validate against; idempotent re-stamp on
// already-PENDING is a no-op write that the SQL primitive
// silently absorbs). MarkPending is the canonical godlike/06 SSOT
// ingestion "stamp" path — production composition root wires it
// into Service.Ingest immediately after pipeline.Lifecycle.ProcessAsset
// success.
func (m *EnrichStateMachine) MarkPending(ctx context.Context, assetID string) error {
	if assetID == "" {
		return ErrEnrichAssetIDRequired
	}
	return m.repo.SetEnrichState(ctx, assetID, asset.EnrichStatePending)
}

// ClaimForEnrichment performs the VLM-sweeper claim: from PENDING or
// FAILED (admin-reset) into ENRICHING. The wrapper writes the
// enrich_state_updated_at stamp atomically (via the SQL primitive's
// updated_at side-effect) so a competing sweep tick sees the new
// state and skips the row (claim fence, mirrors the PR-EMBEDDING-
// CHANNEL-REGISTRY race-mitigation pattern).
//
// Returns ErrIllegalEnrichTransition when fromState is not in the
// canonical validEdges PENDING/FAILED→ENRICHING subset (callers
// should pre-flight via FromState.IsScrapeCandidate or just retry
// the sweep next tick).
func (m *EnrichStateMachine) ClaimForEnrichment(ctx context.Context, assetID string, fromState asset.EnrichState) error {
	if assetID == "" {
		return ErrEnrichAssetIDRequired
	}
	return m.Transition(ctx, assetID, fromState, asset.EnrichStateEnriching)
}

// MarkEnriched closes the success terminal from ENRICHING.
//   - Convenience wrapper for Transition(_, ENRICHING, ENRICHED).
//   - The typed-error contract surfaces ErrIllegalEnrichTransition
//     if the row was NEVER in ENRICHING (e.g. an old admin-tooling
//     path that stamped PENDING then claimed ENRICHED directly).
func (m *EnrichStateMachine) MarkEnriched(ctx context.Context, assetID string) error {
	if assetID == "" {
		return ErrEnrichAssetIDRequired
	}
	return m.Transition(ctx, assetID, asset.EnrichStateEnriching, asset.EnrichStateEnriched)
}

// MarkFailed closes the failure terminal.
//
// godlike/07 explicit-retry-via-admin forward-pointer:
//
//	MarkFailed is terminal-non-retryable. The VLM sweeper does NOT
//	auto-flip FAILED→PENDING on the next tick; the only recovery
//	path is operator-triggered reset via the reindex admin endpoint
//	(OUT OF SCOPE this PR — committed as a forward-pointer to keep
//	the wave-tracker honest).
//
//	RATIONALE: silently flipping FAILED→PENDING on a worker retry
//	would defeat the no-fake-availability contract — the operator
//	MUST see "I attempted enrich, it failed, do something about it",
//	not "I attempted enrich, it failed, I tried again".
func (m *EnrichStateMachine) MarkFailed(ctx context.Context, assetID string) error {
	if assetID == "" {
		return ErrEnrichAssetIDRequired
	}
	return m.Transition(ctx, assetID, asset.EnrichStateEnriching, asset.EnrichStateFailed)
}
