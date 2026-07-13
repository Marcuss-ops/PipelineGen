// Package asset — asset_state.go (PR-CATALOG-MULTILINGUA step 7, July 2026).
//
// AssetState is the canonical, EXPLICIT 14-state machine for the
// asset journey from discovery to multilingual ready. This file
// is the single source of truth for the alphabet + transition
// matrix + helper predicates; no other package declares an
// `AssetState` enum or shadowed constant.
//
// godlike/06 SSOT (non-negotiable): this file is the SOLE canonical
// owner of the AssetState values, the IsValidTransition matrix,
// and the helper predicates. No convenience method like
// `GetReadyMultilingualAssets()` is allowed at any other layer —
// such a method would smuggle a pre-applied filter back into the
// SSOT and is a godlike/06 SSOT violation.
//
// godlike/07 fail-closed (mirrors pipeline_state.go's stricter-
// than-existing-machines contract): an uninitialised AssetState
// ("") must NOT pass any IsValidTransition check. The zero-value
// guard is placed BEFORE the self-loop check so the (zero, zero)
// self-loop is also rejected.
//
// Relationship to the existing 3 layered state machines (godlike/06
// SSOT split — orthogonality is the architecture decision):
//
//   - LifecycleState (lifecycle_state.go) — orthogonal; tracks
//     deletion/online semantics. An asset can be
//     LifecycleState=ACTIVE and AssetState=READY_MULTILINGUAL
//     simultaneously; or LifecycleState=DELETE_REQUESTED while
//     AssetState stays at READY_MULTILINGUAL until the soft-delete
//     stamp lands.
//
//   - PipelineState (pipeline_state.go) — append-only per-item
//     event log (media_assets_pipeline_events.fase). AssetState
//     is the asset's "current journey" SSOT; PipelineState is the
//     diagnostic log. A future follow-up commit wires the
//     finalizer to write AssetState on terminal success.
//
//   - IndexState (index_state.go) — orthogonal; the indexer's
//     narrow progress view (EMBEDDING/INDEXED/etc.). AssetState
//     is the journey view; IndexState stays as the worker's
//     progress surface.
//
// godlike/06 forward-pointer: a future step wires
// SetAssetStateTx in the asset repository + replaces some
// PipelineState readers in the operator dashboard with
// AssetState readers. The wire shape (media_assets.asset_state
// column, TEXT NOT NULL DEFAULT 'DISCOVERED') is stable across
// that commit.
package asset

// AssetState is the canonical per-asset journey state machine.
// 14 values, all UPPERCASE.
type AssetState string

const (
	// StateAssetDiscovered — initial sentinel. The asset has
	// been identified (by the search pipeline, by the user,
	// by an external import) but no work has been requested
	// yet. Migration 157's ALTER TABLE DEFAULT writes this
	// on every existing row.
	StateAssetDiscovered AssetState = "DISCOVERED"

	// StateAssetDownloaded — the asset's bytes are local; ready
	// for normalization + hashing.
	StateAssetDownloaded AssetState = "DOWNLOADED"

	// StateAssetNormalized — the asset has been normalized to
	// the canonical codec/container shape (ffmpeg pass).
	StateAssetNormalized AssetState = "NORMALIZED"

	// StateAssetHashed — content-hash computed; idempotency
	// keys for downstream stages derived from this hash.
	StateAssetHashed AssetState = "HASHED"

	// StateAssetUploaded — the asset is on Drive (render_master
	// verified); ready for the catalog enrichment chain.
	StateAssetUploaded AssetState = "UPLOADED"

	// StateAssetTranscribed — original-language transcript
	// (asset_text_tracks row with kind='transcript' AND
	// is_current=1 AND is_original=true) is present.
	StateAssetTranscribed AssetState = "TRANSCRIBED"

	// StateAssetEnriched — original-language description +
	// visual_summary catalogues are present.
	StateAssetEnriched AssetState = "ENRICHED"

	// StateAssetTranslated — every language in
	// LanguageRegistry.EnabledLanguages() has at least one
	// current track entry (asset_text_tracks row with that
	// language_code AND is_current=1).
	StateAssetTranslated AssetState = "TRANSLATED"

	// StateAssetIndexPending — Qdrant upsert is enqueued but
	// the indexer worker hasn't picked it up yet. Operators
	// see this state when the queue is healthy but the worker
	// pool is saturated.
	StateAssetIndexPending AssetState = "INDEX_PENDING"

	// StateAssetIndexed — Qdrant upsert confirmed; the asset
	// is search-ready in its primary language.
	StateAssetIndexed AssetState = "INDEXED"

	// StateAssetReady — the single-language readiness gate
	// passes: indexed + originals (transcript/description/
	// visual_summary) present + render_master verified on
	// Drive. Operators see this state as the "single-locale
	// ready" check.
	StateAssetReady AssetState = "READY"

	// StateAssetReadyMultilingual — the full multilingual gate
	// passes (render_master + originals + all required
	// languages + Qdrant updated + outbox empty). The
	// canonical publishable state for the multilingual
	// pipeline. readiness.go's EvaluateMultilingualReadiness
	// is the only path that flips this state.
	StateAssetReadyMultilingual AssetState = "READY_MULTILINGUAL"

	// StateAssetFailedRetryable — semi-terminal failure. The
	// worker classified the error as transient (network 5xx,
	// 429, rate-limit) or retryable. Operator-driven re-entry:
	// any pre-terminal state can be the destination. NOT
	// terminal in the IsTerminal sense (IsRetryable returns
	// true). LifecycleState stays unchanged so the row
	// remains online while retries are coordinated.
	StateAssetFailedRetryable AssetState = "FAILED_RETRYABLE"

	// StateAssetFailedPermanent — true terminal failure. The
	// worker classified the error as permanent (404, content
	// filter, validation) OR retryable errors exhausted the
	// retry budget. No out-edge. Operator must fix and re-
	// create the asset to leave this state. IsTerminal
	// returns true.
	StateAssetFailedPermanent AssetState = "FAILED_PERMANENT"
)

// CanonicalAssetStateValues returns the closed enumeration of
// canonical AssetState strings, in canonical-declaration order.
// Callers use this as the canonical source-of-truth list for
// migrations, dashboards, and (future) CHECK constraints on
// media_assets.asset_state.
func CanonicalAssetStateValues() []AssetState {
	return []AssetState{
		StateAssetDiscovered,
		StateAssetDownloaded,
		StateAssetNormalized,
		StateAssetHashed,
		StateAssetUploaded,
		StateAssetTranscribed,
		StateAssetEnriched,
		StateAssetTranslated,
		StateAssetIndexPending,
		StateAssetIndexed,
		StateAssetReady,
		StateAssetReadyMultilingual,
		StateAssetFailedRetryable,
		StateAssetFailedPermanent,
	}
}

// validAssetStateSet is the O(1) membership set backing
// Valid(). Built once at init from
// CanonicalAssetStateValues().
var validAssetStateSet = func() map[AssetState]struct{} {
	m := make(map[AssetState]struct{}, len(CanonicalAssetStateValues()))
	for _, s := range CanonicalAssetStateValues() {
		m[s] = struct{}{}
	}
	return m
}()

// Valid returns true if s is one of the canonical AssetState
// values. Defensive against ad-hoc string values; mirrors the
// pattern in LifecycleState.Valid / PipelineState.Valid /
// IndexState.Valid.
func (s AssetState) Valid() bool {
	_, ok := validAssetStateSet[s]
	return ok
}

// String makes AssetState satisfy fmt.Stringer so the canonical
// log/diagnostic tag (zap.Stringer(...) rendering) shows the
// wire-format value without explicit casts.
func (s AssetState) String() string { return string(s) }

// IsTerminal reports whether the state is a terminal value (no
// further automatic transitions expected unless the operator
// explicitly acts). Terminal states are:
//   - StateAssetReadyMultilingual (success terminal)
//   - StateAssetFailedPermanent (true failure terminal)
//
// StateAssetFailedRetryable is NOT terminal — operator-driven
// re-entry is allowed.
func (s AssetState) IsTerminal() bool {
	switch s {
	case StateAssetReadyMultilingual, StateAssetFailedPermanent:
		return true
	}
	return false
}

// IsFailedTerminal reports whether the state is the true failure
// terminal (no out-edge). Only StateAssetFailedPermanent;
// StateAssetFailedRetryable is excluded (operator retry may
// re-enter from there).
func (s AssetState) IsFailedTerminal() bool {
	return s == StateAssetFailedPermanent
}

// IsRetryable reports whether the state is the semi-terminal-
// failure state from which operator-driven re-entry is allowed.
// Only StateAssetFailedRetryable; the true terminal
// FAILED_PERMANENT is excluded.
func (s AssetState) IsRetryable() bool {
	return s == StateAssetFailedRetryable
}

// IsSucceededTerminal reports whether the state is the success
// terminal. Only StateAssetReadyMultilingual.
func (s AssetState) IsSucceededTerminal() bool {
	return s == StateAssetReadyMultilingual
}

// IsMultilingualGate reports whether the state is the canonical
// "multilingual pipeline publishable" gate. Only
// StateAssetReadyMultilingual returns true; READY returns false
// even though READY's gate (single-locale) is also a success
// sub-state.
func (s AssetState) IsMultilingualGate() bool {
	return s == StateAssetReadyMultilingual
}

// canonicalPreTerminalStates is the 11-element set of
// happy-path states that admit a FAILED_* exit. The order
// matches CanonicalAssetStateValues() prefix. Kept
// production-private so callers don't accidentally use the
// wrong shape — they go through CanonicalAssetStateValues()
// instead.
//
// godlike/06 SSOT invariant: this slice shares its prefix
// with CanonicalAssetStateValues() up to StateAssetReady
// (11 elements). The archcheck forward-prevention check
// `percheck_asset_state_canonical_14` pins that the slice
// length is 14 at the canonical surface, AND the test
// `TestAssetState_PreTerminalStatesLength` pins that this
// private slice has exactly 11 entries (mirrors the test's
// nonTerminalAssetStates slice).
var canonicalPreTerminalStates = []AssetState{
	StateAssetDiscovered,
	StateAssetDownloaded,
	StateAssetNormalized,
	StateAssetHashed,
	StateAssetUploaded,
	StateAssetTranscribed,
	StateAssetEnriched,
	StateAssetTranslated,
	StateAssetIndexPending,
	StateAssetIndexed,
	StateAssetReady,
}

// IsValidTransition reports whether moving from `s` (the
// receiver) to `to` is one of the allowed edges of the
// explicit asset state machine (PR-CATALOG-MULTILINGUA
// step 7, July 2026).
//
// Strict-machine contract:
//
//	Happy path (11 forward edges):
//	    DISCOVERED    → DOWNLOADED
//	    DOWNLOADED    → NORMALIZED
//	    NORMALIZED    → HASHED
//	    HASHED        → UPLOADED
//	    UPLOADED      → TRANSCRIBED
//	    TRANSCRIBED   → ENRICHED
//	    ENRICHED      → TRANSLATED
//	    TRANSLATED    → INDEX_PENDING
//	    INDEX_PENDING → INDEXED
//	    INDEXED       → READY
//	    READY         → READY_MULTILINGUAL
//
//	Degradation (1 edge):
//	    READY_MULTILINGUAL → READY
//
//	    Use case: a topology change moves the asset out of
//	    multilingual readiness (operator removes a required
//	    language from the YAML; Drive file deleted post-
//	    success; outbox event re-fires). The asset degrades
//	    gracefully to the single-locale ready state rather
//	    than to a tombstone-shaped failure.
//
//	Failure exits (any pre-terminal → FAILED_RETRYABLE or
//	FAILED_PERMANENT; 22 edges across the 11 happy-path
//	states):
//	    <any pre-terminal> → FAILED_RETRYABLE
//	    <any pre-terminal> → FAILED_PERMANENT
//
//	    READY_MULTILINGUAL is the success terminal; FAILED_*
//	    are the failure exits themselves — neither is in the
//	    source set for the failure exit.
//
//	Retry re-entry (11 edges; from FAILED_RETRYABLE only):
//	    FAILED_RETRYABLE → DISCOVERED
//	    FAILED_RETRYABLE → DOWNLOADED
//	    ...etc, every pre-terminal state.
//
//	    Operator-driven re-entry only; the state machine
//	    itself does NOT auto-retry. The future RetrySuggested
//	    bool on the FAILED_RETRYABLE edge tells schedulers
//	    whether to dequeue the asset for retry; the state
//	    machine is the source of truth, the scheduler is the
//	    policy.
//
//	FAILED_PERMANENT:
//	    terminal — zero out-edges. Operator must fix and re-
//	    create the asset to leave this state.
//
//	Self-loops are IDEMPOTENT (writing the same state twice
//	is harmless; the future SetAssetStateTx uses this for
//	safe re-entry on partial-write recovery).
//
//	Unknown target values (e.g. "discovered", "READY X", "")
//	are REJECTED (Valid() check on `to`).
//
// All other transitions (including out of the true terminal
// READY_MULTILINGUAL's other out-edges, or any edge from
// FAILED_PERMANENT) are rejected. The future writer
// SetAssetStateTx gates on IsValidTransition so a programmer
// error becomes a typed error rather than a runtime tombstone
// of an in-flight asset.
func (s AssetState) IsValidTransition(to AssetState) bool {
	// Zero-value from-state guard (godlike/07 fail-closed,
	// mirrors pipeline_state_test.go's stricter-than-existing-
	// machines contract): a caller that hasn't initialized
	// an AssetState must NOT get a silent false-positive.
	// Placed BEFORE the self-loop check so the guard also
	// rejects the (zero, zero) self-loop.
	if !s.Valid() {
		return false
	}
	if s == to {
		return true // idempotent self-loop (both must be canonical)
	}
	if !to.Valid() {
		return false // unknown target
	}
	// Failure exits: any of the 11 pre-terminal happy-path
	// states can move to FAILED_RETRYABLE or FAILED_PERMANENT.
	// READY_MULTILINGUAL (success terminal) and the failure
	// semi-/full terminals are excluded from the source set.
	if to == StateAssetFailedRetryable || to == StateAssetFailedPermanent {
		for _, nt := range canonicalPreTerminalStates {
			if nt == s {
				return true
			}
		}
		return false
	}
	// Degradation: READY_MULTILINGUAL → READY (only).
	if s == StateAssetReadyMultilingual {
		return to == StateAssetReady
	}
	// Retry re-entry: FAILED_RETRYABLE → any of the 11
	// pre-terminal states.
	if s == StateAssetFailedRetryable {
		for _, nt := range canonicalPreTerminalStates {
			if nt == to {
				return true
			}
		}
		return false
	}
	// FAILED_PERMANENT: terminal — zero out-edges (except
	// the universal self-loop handled above).
	if s == StateAssetFailedPermanent {
		return false
	}
	// Happy-path forward edges: explicit table; reject any
	// pair not listed.
	switch s {
	case StateAssetDiscovered:
		return to == StateAssetDownloaded
	case StateAssetDownloaded:
		return to == StateAssetNormalized
	case StateAssetNormalized:
		return to == StateAssetHashed
	case StateAssetHashed:
		return to == StateAssetUploaded
	case StateAssetUploaded:
		return to == StateAssetTranscribed
	case StateAssetTranscribed:
		return to == StateAssetEnriched
	case StateAssetEnriched:
		return to == StateAssetTranslated
	case StateAssetTranslated:
		return to == StateAssetIndexPending
	case StateAssetIndexPending:
		return to == StateAssetIndexed
	case StateAssetIndexed:
		return to == StateAssetReady
	case StateAssetReady:
		return to == StateAssetReadyMultilingual
	}
	// Catch-all: any (from, to) pair not handled above is
	// rejected. The 3 case branches above (FAILED_* source,
	// READY_MULTILINGUAL source, FAILED_RETRYABLE source,
	// FAILED_PERMANENT source) plus the explicit happy-path
	// cases exhaust the canonical-state-machines.
	return false
}
