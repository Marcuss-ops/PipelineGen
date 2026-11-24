package contract

// SemanticBindingIssue reports a resolved retrieval hit that could not become
// an annotation image. UNBOUND_REQUIRES_ACQUISITION is the overlay-only case:
// the plan found a URL but has no verified content address yet.
type SemanticBindingIssue struct {
	UnitID, SceneID, EntityID, Reason string
}
