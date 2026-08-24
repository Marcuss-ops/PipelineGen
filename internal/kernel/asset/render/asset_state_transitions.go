package render

// canonicalPreTerminalStates is the 11-element set of happy-path states
// that admit a FAILED_* exit. The order matches
// CanonicalAssetStateValues() up to StateAssetReady. The archcheck
// forward-prevention check percheck_asset_state_canonical_14 and the
// regression test TestAssetState_PreTerminalStatesLength pin this
// shape.
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

// IsValidTransition reports whether moving from s to to is an allowed
// edge of the explicit asset state machine.
//
// Allowed edges:
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
//	Failure exits (any pre-terminal → FAILED_RETRYABLE or FAILED_PERMANENT):
//
//	Retry re-entry (FAILED_RETRYABLE → any pre-terminal state).
//
//	FAILED_PERMANENT is terminal (zero out-edges).
//
// Self-loops are idempotent. Unknown source/target values are rejected.
func (s AssetState) IsValidTransition(to AssetState) bool {
	// godlike/07 fail-closed: an uninitialised AssetState must NOT get
	// a silent false-positive. This guard also rejects the (zero, zero)
	// self-loop.
	if !s.Valid() {
		return false
	}
	if s == to {
		return true
	}
	if !to.Valid() {
		return false
	}
	// Failure exits: any of the 11 pre-terminal happy-path states can
	// move to FAILED_RETRYABLE or FAILED_PERMANENT.
	if to == StateAssetFailedRetryable || to == StateAssetFailedPermanent {
		for _, nt := range canonicalPreTerminalStates {
			if nt == s {
				return true
			}
		}
		return false
	}
	// Degradation: READY_MULTILINGUAL → READY only.
	if s == StateAssetReadyMultilingual {
		return to == StateAssetReady
	}
	// Retry re-entry: FAILED_RETRYABLE → any pre-terminal state.
	if s == StateAssetFailedRetryable {
		for _, nt := range canonicalPreTerminalStates {
			if nt == to {
				return true
			}
		}
		return false
	}
	// FAILED_PERMANENT: terminal — zero out-edges.
	if s == StateAssetFailedPermanent {
		return false
	}
	// Happy-path forward edges.
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
	return false
}
