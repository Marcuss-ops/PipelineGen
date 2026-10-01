// Package adapters — materialization_concurrency.go: the bounded worker
// count used by the VidRush materialization fan-out.
//
// Materialization is I/O bound (provider session + download + Drive upload),
// so a single worker leaves the host idle while many hundreds of images are
// fetched, and an unbounded fan-out exhausts sockets, temp files and Drive
// quota. The count therefore has one owner: the plan may raise it, the
// adapter clamps it.
package adapters

import (
	"runtime"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const (
	// DefaultMaterializationWorkers is used when the plan does not request a
	// worker count and the host is otherwise under-committed.
	DefaultMaterializationWorkers = 4
	// MinMaterializationWorkers keeps at least one segment (and its provider
	// pipeline) in flight.
	MinMaterializationWorkers = 1
	// MaxMaterializationWorkers caps the fan-out so a large plan value cannot
	// open an unbounded number of provider sessions and temp files.
	MaxMaterializationWorkers = 16
)

// materializationWorkers resolves the bounded concurrency for the segment
// materialization fan-out. A positive plan value wins (clamped); otherwise the
// default is used but never more than the host can actually run in parallel.
func materializationWorkers(plan *scriptpkg.ResolvedGenerationPlan) int {
	if plan != nil && plan.MediaPlan.Materialization.Workers > 0 {
		return clampMaterializationWorkers(plan.MediaPlan.Materialization.Workers)
	}
	workers := runtime.GOMAXPROCS(0)
	if workers < 2 {
		workers = 2
	}
	if workers > DefaultMaterializationWorkers {
		workers = DefaultMaterializationWorkers
	}
	return workers
}

// clampMaterializationWorkers pins a requested worker count into the supported
// range. Values below the minimum are raised, values above the maximum are
// capped, so a malformed plan degrades to a safe bound instead of failing.
func clampMaterializationWorkers(requested int) int {
	if requested < MinMaterializationWorkers {
		return MinMaterializationWorkers
	}
	if requested > MaxMaterializationWorkers {
		return MaxMaterializationWorkers
	}
	return requested
}
