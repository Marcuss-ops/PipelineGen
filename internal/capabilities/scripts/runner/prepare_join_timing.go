package runner

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// PrepareJoinBreakdown is owned by contract; the alias keeps the runner
// package's historical API for its callers and tests.
type PrepareJoinBreakdown = contract.PrepareJoinBreakdown

// BuildPrepareJoinBreakdown is the runner-facing bridge to the contract owner.
func BuildPrepareJoinBreakdown(
	branchStart, joinStart, joinedAt, semanticDone, assetsDone time.Time,
) *PrepareJoinBreakdown {
	return contract.BuildPrepareJoinBreakdown(branchStart, joinStart, joinedAt, semanticDone, assetsDone)
}
