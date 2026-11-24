package contract

// EntityImageDecisionOwner names the single flow allowed to decide which
// entity image is selected and bound for this run. The other flow may still
// produce diagnostics, but it does not write a competing binding.
type EntityImageDecisionOwner string
