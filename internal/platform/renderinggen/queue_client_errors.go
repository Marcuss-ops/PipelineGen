package renderinggen

import (
	"errors"
	"fmt"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	queueclient "github.com/Marcuss-ops/RenderingGen/queue/client"
)

// classifyQueueSubmitError maps Queue's atomic submit outcomes. A 200 replay
// arrives as ErrJobExists only after Queue compared the immutable request;
// every unverified 409 is a conflict and never triggers a producer-side GET.
func classifyQueueSubmitError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, queueclient.ErrJobExists) {
		return fmt.Errorf("%w: %v", scriptgen.ErrJobExists, err)
	}
	if errors.Is(err, queueclient.ErrIdempotencyConflict) || errors.Is(err, queueclient.ErrDuplicateJobID) {
		return fmt.Errorf("%w: %v", scriptgen.ErrRenderRequestConflict, err)
	}
	return nil
}
