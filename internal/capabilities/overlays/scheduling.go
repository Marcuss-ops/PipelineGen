package overlays

// Queue priorities used by PipelineGen when it feeds RenderingGen. The job
// broker already orders claims by priority; keeping these values in the
// overlay capability contract prevents creator code from inventing its own
// ordering policy.
const (
	PriorityUrgentRender   = 100
	PriorityCurrentPrepare = 80
	PriorityFutureRender   = 50
	PriorityFuturePrepare  = 20
)

func PreparePriority(current bool) int {
	if current {
		return PriorityCurrentPrepare
	}
	return PriorityFuturePrepare
}

func RenderPriority(current bool) int {
	if current {
		return PriorityUrgentRender
	}
	return PriorityFutureRender
}
