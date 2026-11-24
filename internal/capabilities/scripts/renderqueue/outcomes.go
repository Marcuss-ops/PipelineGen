package renderqueue

// Render-attempt outcomes are the bounded labels persisted in analytics and
// emitted on the render metrics. They are wire facts of this phase.
const (
	renderOutcomeSuccess  = "success"
	renderOutcomeFailure  = "failure"
	renderOutcomeCacheHit = "cache_hit"
)
