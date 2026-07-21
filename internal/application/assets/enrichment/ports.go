// Package enrichment — ports.go is the canonical Pattern 0 port
// surface for the per-asset enrichment state machine
// (PR-ENRICHMENT-STATE-MACHINE, July 2026, godlike/06 SSOT).
//
// The typed state-machine wrapper (state_machine.go) implements
// EnrichStateMachinePort; the SQL primitive (SetEnrichState on
// ClipsRepository) implements EnrichRepositoryPort. The two ports
// together form the canonical closure of "one owner per fact" for
// the media_assets.enrich_state column — only the wrapper reads
// (assetID)→(current state) and writes transitions; only the SQL
// primitive performs the atomic UPDATE. The ports decouple the
// application-layer canonical state-machine logic from the
// SQLite-shaped concrete so test fixtures can swap the port pair
// for in-memory mocks without violating the godlike/06 SSOT contract
// (the VLM sweeper tests use the same port pair with a mock
// repository; production wires the concrete via
// internal/app/build_bundles_process.go).
package enrichment

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// EnrichRepositoryPort is the canonical read+write seam to the
// media_assets.enrich_state column. Implemented in production by
// *ClipsRepository.SetEnrichState + GetEnrichState (mirrors
// SetIndexState + GetIndexState shape — the index_state.go file
// holds GetIndexState as a similar read primitive).
//
// Pattern 0 contract per godlike/06:
//   - The typed state-machine wrapper is the only caller that should
//     invoke SetEnrichStateViaPort; production-side ad-hoc writes
//     (e.g. operator tooling) should go through the state-machine
//     wrapper to enforce transition validation. The port itself is
//     intentionally permissive (no validEdges check) so test mocks
//     stay simple and direct.
type EnrichRepositoryPort interface {
	// SetEnrichState writes the typed enum column + atomic
	// enrich_state_updated_at stamp. Returns ErrEnrichStateMissing
	// if the asset row is not in media_assets.
	//
	// Idempotent on same-state: the column flip on an already-target
	// state row is a no-op write. This method is intentionally
	// unconditional — callers that need the current state to match a
	// specific from-state must use SetEnrichStateIfCurrent.
	SetEnrichState(ctx context.Context, id string, state asset.EnrichState) error

	// SetEnrichStateIfCurrent atomically flips the column from the
	// expected from-state to the target to-state, stamping
	// enrich_state_updated_at. Returns ErrEnrichStateMissing when the
	// asset row is absent OR when the row's current state is not the
	// expected from-state (CAS lost). This is the primitive used by
	// the state-machine wrapper for all validated transitions so
	// that concurrent sweepers cannot claim the same PENDING row.
	SetEnrichStateIfCurrent(ctx context.Context, id string, from, to asset.EnrichState) error

	// GetEnrichState reads the current typed enum column. Returns
	// EnrichStatePending when the row exists and the column carries
	// the canonical default; returns the empty + ErrEnrichStateMissing
	// when the row is absent.
	GetEnrichState(ctx context.Context, id string) (asset.EnrichState, error)
}

// EnrichStateMachinePort is the canonical typed state-machine surface
// (one owner per fact for the enrich_state transition space). The
// state-machine wrapper is the only caller-side validated path; future
// direct-repo callers must go through Transition + not touch the
// column directly, otherwise the canonical transition history drifts
// (godlike/06 SSOT).
type EnrichStateMachinePort interface {
	// Transition validates from→to via the canonical validEdges set
	// (PENDING→{ENRICHING}, ENRICHING→{ENRICHED,FAILED}, optionally
	// FAILED→PENDING for admin-reindex tooling per godlike/07 explicit-
	// retry-via-admin), then performs the atomic column flip via the
	// repository port. Returns:
	//   - ErrIllegalEnrichTransition (typed envelope via errors.As)
	//     when the requested edge is not in validEdges.
	//   - ErrEnrichStateMissing when the asset row is absent from
	//     media_assets.
	//   - ErrEnrichAssetIDRequired when id is empty (pre-flight check).
	Transition(ctx context.Context, assetID string, from, to asset.EnrichState) error

	// MarkPending stamps PENDING on a freshly-ingested row.
	// Convenience wrapper that bypasses the from-state check
	// (the canonical ingest path stamps PENDING unconditionally
	// because the row is brand-new — there is no prior state to
	// validate against). Equivalent to Transition(id, "", PENDING)
	// but expresses the intent at the type system level.
	MarkPending(ctx context.Context, assetID string) error

	// ClaimForEnrichment performs the VLM-sweeper claim:
	// PENDING→ENRICHING or FAILED→ENRICHING (the latter only when
	// an admin tool has reset state to FAILED-transitioned-PENDING,
	// see godlike/07 explicit-retry-via-admin forward-pointer). The
	// wrapper writes the updated_at stamp atomically so a competing
	// sweep tick sees the new state and skips the row (claim fence
	// is the canonical race-mitigation we discussed in
	// PR-EMBEDDING-CHANNEL-REGISTRY / VLM-sweeper-update chain).
	ClaimForEnrichment(ctx context.Context, assetID string, fromState asset.EnrichState) error

	// MarkEnriched closes the success terminal.
	MarkEnriched(ctx context.Context, assetID string) error

	// MarkFailed closes the failure terminal.
	//    Note: returns godlike/07 explicit-retry-via-admin forward-
	// pointer as part of the typed-error contract — operators MUST
	// use the admin reindex endpoint to reset FAILED→PENDING; the
	// state-machine wrapper does NOT auto-retry on FAILED.
	MarkFailed(ctx context.Context, assetID string) error
}
