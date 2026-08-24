// Package enrichment — errors.go carries the godlike/07 typed-error
// sentinel surface for the canonical EnrichStateMachine wrapper
// (PR-ENRICHMENT-STATE-MACHINE, July 2026).
//
// All sentinel errors are constructed once via errors.New so the
// canonical-identity stays stable across test agents and operator
// dashboards — a future grep for "ErrIllegalEnrichTransition" must
// return exactly this package regardless of where the state-machine
// is consumed (ingest / VLM sweeper / admin reindex tooling).
//
// Companion surface: internal/kernel/asset/enrich_state.go (the typed
// enum canonical 4-state vocabulary lives there per godlike/06 SSOT
// one-owner-per-fact).
package enrichment

import (
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ErrEnrichStateNotWired is returned when a typed state-machine
// caller invokes Transition on a *StateMachine whose deps are not
// fully wired (e.g. nil repository port). The constructor
// NewEnrichStateMachine validates the deps surface; this sentinel
// surfaces the failure mode only when the wiring is bypassed by
// ad-hoc test fixtures (Pattern 0 fail-closed contract).
var ErrEnrichStateNotWired = errors.New("enrichment: state machine not wired (clips repository port nil)")

// ErrIllegalEnrichTransition is the typed-error envelope returned by
// the canonical state-machine wrapper when a caller requests a
// transition that is not in the closed validEdges set. Callers
// errors.As(err, &ite) to inspect the rejected edge:
//
//	var ite *enrichment.IllegalEnrichTransitionError
//	if errors.As(err, &ite) {
//	   log.Warn("rejected transition",
//	       zap.String("from", string(ite.From)),
//	       zap.String("to", string(ite.To)))
//	}
//
// The From/To typed fields let operator dashboards rank rejected
// edges by frequency (e.g. "X callers attempted PENDING→ENRICHED
// which is not a valid edge — they should be calling the ingest
// stamp path instead").
type IllegalEnrichTransitionError struct {
	From asset.EnrichState
	To   asset.EnrichState
}

// Error implements the error interface. Format pins the canonical
// 4-state names so test assertions can match against
// "illegal enrichment transition: PENDING -> ENRICHED" verbatim.
func (e *IllegalEnrichTransitionError) Error() string {
	return "enrichment: illegal transition " + string(e.From) + " -> " + string(e.To)
}

// Is supports errors.Is(err, ErrIllegalEnrichTransition). The
// receiver has pointer-receiver semantics so errors.As works
// correctly (errors.Is compares by identity when the sentinel does
// not match; callers should use errors.As for the typed envelope
// access).
func (e *IllegalEnrichTransitionError) Is(target error) bool {
	return target == ErrIllegalEnrichTransition
}

// ErrIllegalEnrichTransition is the canonical sentinel-mirror of
// IllegalEnrichTransitionError{}. callers can errors.Is(err,
// ErrIllegalEnrichTransition) WITHOUT errors.As — useful for
// pre-flight filter gates (e.g. the VLM sweeper logs WARN but does
// not abort on illegal-transition rejections from the state-machine
// wrapper).
var ErrIllegalEnrichTransition = errors.New("enrichment: illegal transition rejected by state machine")

// ErrEnrichStateMissing is returned by Transition when the asset row
// is not present in media_assets (clips_repository.SetEnrichState's
// underlying UPDATE returns RowsAffected=0). The state-machine
// wrapper surfaces this as a distinct typed sentinel so callers can
// distinguish "row missing" from "illegal transition" without
// parsing DB error strings.
var ErrEnrichStateMissing = errors.New("enrichment: asset row missing for state transition")

// ErrEnrichAssetIDRequired is returned by Transition when the caller
// passes an empty assetID. The state-machine wrapper is internally
// nil-safe on the port call (SetEnrichState has its own id-check),
// but the pre-flight validation rejects empty IDs BEFORE the SQL
// roundtrip so the typed-error contract surfaces a stable sentinel
// rather than the lower-level fmt.Errorf format-string.
var ErrEnrichAssetIDRequired = errors.New("enrichment: asset id required for state transition")
