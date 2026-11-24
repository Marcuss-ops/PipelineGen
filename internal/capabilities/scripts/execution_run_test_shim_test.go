package scriptgeneration

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// executionRun is a test-only fixture for the two audio-overlap tests. The
// production orchestrator now lives in scripts/runner; these tests exercise
// the root host's audioCompile phase directly, with the same run-scoped values
// they used before the inversion.
type executionRun struct {
	r       *Runner
	ctx     context.Context
	runID   string
	req     contract.GenerateRequest
	exec    ExecutionContext
	result  *contract.GenerateResult
	routing scriptpkg.ArtifactRoutingContext
}

func (e *executionRun) audioCompile() bool {
	return e.r.audioCompilePhase(e.ctx, e.runID, e.req, e.routing, e.exec, -1, e.result)
}
