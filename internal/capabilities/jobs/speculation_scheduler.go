package jobs

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/scheduling"
)

// Type aliases to the canonical lower layer.
type (
	SpeculationDepth     = scheduling.SpeculationDepth
	SpeculationCost      = scheduling.SpeculationCost
	SpeculationResource  = scheduling.SpeculationResource
	ResourceBudget       = scheduling.ResourceBudget
	ActiveWorkGate       = scheduling.ActiveWorkGate
	ActiveWorkFunc       = scheduling.ActiveWorkFunc
	SpeculationConfig    = scheduling.SpeculationConfig
	SpeculationCandidate = scheduling.SpeculationCandidate
	ResourceScheduler    = scheduling.ResourceScheduler
)

// SpeculationScheduler is the legacy name retained for callers that imported
// the pre-pool type. It is the same implementation.
type SpeculationScheduler = scheduling.ResourceScheduler

// Re-exported constants.
const (
	DepthNPlus1 = scheduling.DepthNPlus1
	DepthNPlus2 = scheduling.DepthNPlus2
	DepthNPlus3 = scheduling.DepthNPlus3

	CostWarm      = scheduling.CostWarm
	CostPrepare   = scheduling.CostPrepare
	CostSpeculate = scheduling.CostSpeculate

	ResourceNetwork = scheduling.ResourceNetwork
	ResourceDisk    = scheduling.ResourceDisk
	ResourceCPU     = scheduling.ResourceCPU
	ResourceLLM     = scheduling.ResourceLLM
	ResourceTTS     = scheduling.ResourceTTS
	ResourceGPU     = scheduling.ResourceGPU
)

// DefaultResourceBudget is the canonical per-class budget for speculative
// work; the value lives in jobs/scheduling.
func DefaultResourceBudget(capacity, speculativeMax int) scheduling.ResourceBudget {
	return scheduling.DefaultResourceBudget(capacity, speculativeMax)
}

// DefaultSpeculationConfig returns production-shaped admission limits.
func DefaultSpeculationConfig() scheduling.SpeculationConfig {
	return scheduling.DefaultSpeculationConfig()
}

// NewResourceScheduler constructs a resource-aware scheduler with per-class
// speculative budgets. The gate is required: nil gate disables all
// speculation (fail-closed — active work wins by default).
func NewResourceScheduler(config scheduling.SpeculationConfig, gate scheduling.ActiveWorkGate) *scheduling.ResourceScheduler {
	return scheduling.NewResourceScheduler(config, gate)
}

// NewSpeculationScheduler is the legacy-name constructor for the same
// resource-aware scheduler.
func NewSpeculationScheduler(config scheduling.SpeculationConfig, gate scheduling.ActiveWorkGate) *scheduling.ResourceScheduler {
	return scheduling.NewSpeculationScheduler(config, gate)
}
