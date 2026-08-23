// Package usecase keeps the compatibility facade for script generation plan
// construction during WAVE-21 PR-G BACKFILL. The canonical implementation
// lives in the sibling generation package; this facade remains until CUTOVER
// migrates all callers away from the usecase import path.
package usecase

import (
	generationpkg "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/generation"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// BuildPlan preserves the legacy usecase API while routing all plan creation
// through generation, the canonical owner of the plan-builder boundary.
func BuildPlan(item scriptpkg.GenerationItemV2) scriptpkg.ResolvedGenerationPlan {
	return generationpkg.BuildPlan(item)
}

// BuildPlans preserves the legacy batch API while routing all plan creation
// through generation, the canonical owner of the plan-builder boundary.
func BuildPlans(items []scriptpkg.GenerationItemV2) []scriptpkg.ResolvedGenerationPlan {
	return generationpkg.BuildPlans(items)
}
