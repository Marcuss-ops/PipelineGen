package asset

// validAssetStateSet is the O(1) membership set backing Valid().
// Built once at init from CanonicalAssetStateValues().
var validAssetStateSet = func() map[AssetState]struct{} {
	m := make(map[AssetState]struct{}, len(CanonicalAssetStateValues()))
	for _, s := range CanonicalAssetStateValues() {
		m[s] = struct{}{}
	}
	return m
}()

// Valid returns true if s is one of the canonical AssetState values.
// Defensive against ad-hoc string values.
func (s AssetState) Valid() bool {
	_, ok := validAssetStateSet[s]
	return ok
}

// IsTerminal reports whether the state is terminal (no further automatic
// transitions expected). Terminal states are READY_MULTILINGUAL and
// FAILED_PERMANENT. FAILED_RETRYABLE is NOT terminal because an operator
// may explicitly re-enter a pre-terminal state.
func (s AssetState) IsTerminal() bool {
	switch s {
	case StateAssetReadyMultilingual, StateAssetFailedPermanent:
		return true
	}
	return false
}

// IsFailedTerminal reports whether the state is the true failure terminal.
// Only StateAssetFailedPermanent; FAILED_RETRYABLE is excluded.
func (s AssetState) IsFailedTerminal() bool {
	return s == StateAssetFailedPermanent
}

// IsRetryable reports whether the state is the semi-terminal failure state
// from which operator-driven re-entry is allowed. Only
// StateAssetFailedRetryable.
func (s AssetState) IsRetryable() bool {
	return s == StateAssetFailedRetryable
}

// IsSucceededTerminal reports whether the state is the success terminal.
// Only StateAssetReadyMultilingual.
func (s AssetState) IsSucceededTerminal() bool {
	return s == StateAssetReadyMultilingual
}

// IsMultilingualGate reports whether the state is the canonical
// "multilingual pipeline publishable" gate. Only
// StateAssetReadyMultilingual returns true; READY returns false even
// though READY is itself a success sub-state.
func (s AssetState) IsMultilingualGate() bool {
	return s == StateAssetReadyMultilingual
}
