// Package asset — AssetState is the canonical 14-state journey state
// machine for the asset pipeline from discovery to multilingual ready.
//
// AssetStateAlphabetCount is the single literal source of truth for the
// canonical count. It is cross-checked by the canonical-14 archcheck
// scanner and by the asset_state_test.go regression tests.
//
// godlike/07 fail-closed: an uninitialised AssetState ("") does NOT pass
// any IsValidTransition check. The zero-value guard lives in
// asset_state_transitions.go.
package asset

// AssetStateAlphabetCount is the canonical inventory size for the
// AssetState enum. All consumers MUST reference this constant instead of
// a literal `14` so a future change to the alphabet changes one literal.
const AssetStateAlphabetCount = 14

// AssetState is the canonical per-asset journey state machine. Values
// are UPPERCASE strings persisted in media_assets.asset_state.
type AssetState string

// CanonicalAssetStateValues returns the closed enumeration of canonical
// AssetState strings, in canonical-declaration order.
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

// String returns the wire-format value of the state.
func (s AssetState) String() string { return string(s) }
