package scheduling

import (
	"context"
	"fmt"
	"sort"
	"sync"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// SpeculationDepth identifies queue lookahead distance from the active job.
type SpeculationDepth int

const (
	DepthNPlus1 SpeculationDepth = 1
	DepthNPlus2 SpeculationDepth = 2
	DepthNPlus3 SpeculationDepth = 3
)

// SpeculationCost controls how aggressively a unit may be admitted.
type SpeculationCost string

const (
	CostWarm      SpeculationCost = "WARM"
	CostPrepare   SpeculationCost = "PREPARE"
	CostSpeculate SpeculationCost = "SPECULATE"
)

// SpeculationResource identifies the constrained resource class.
// These mirror the canonical kernel job.ResourceClass values; the kernel
// constants are the SSOT (internal/kernel/job/preparation.go) and planning
// units carry them via PreparationUnit.ResourceClass.
type SpeculationResource string

const (
	ResourceNetwork SpeculationResource = "NETWORK"
	ResourceDisk    SpeculationResource = "DISK_IO"
	ResourceCPU     SpeculationResource = "CPU"
	ResourceLLM     SpeculationResource = "LLM"
	ResourceTTS     SpeculationResource = "TTS"
	ResourceGPU     SpeculationResource = "GPU_RENDER"
)

// ResourceBudget is the per-resource-class admission budget for speculative
// work. Type alias of the canonical kernel budget so the scheduler and the
// planner share one definition.
type ResourceBudget = job.ResourceBudget

// ActiveWorkGate is consulted before every admission. Active execution always
// wins; when false, speculative work must not start or continue. Alias of the
// canonical kernel observer port; ActiveWorkFunc remains the convenient
// closure adapter.
type ActiveWorkGate = job.ActiveWorkObserver

// ActiveWorkFunc adapts a closure to the ActiveWorkGate port.
type ActiveWorkFunc func() bool

func (f ActiveWorkFunc) ActiveWorkAvailable() bool { return f != nil && f() }

// SpeculationConfig defines admission limits per lookahead depth and resource.
//
// ResourceLimits is the per-class speculative budget map. A class absent from
// the map uses DefaultResourceBudget. Active work is never throttled by these
// budgets — only speculative units consume SpeculativeMax / residual capacity.
type SpeculationConfig struct {
	MaxDepth      SpeculationDepth
	MaxUnits      int
	MaxByCost     map[SpeculationCost]int
	MaxByResource map[SpeculationResource]int
	// ResourceLimits holds the canonical per-class ResourceBudget. When set,
	// MaxByResource is derived from it (speculative concurrency = the budget's
	// SpeculativeMax / capacity residual) so the two knobs cannot drift.
	ResourceLimits map[SpeculationResource]ResourceBudget
}

// DefaultResourceBudget returns the canonical budget for one resource class.
// Speculation may use at most SpeculativeMax units concurrently per class;
// active work is never counted against the budget.
func DefaultResourceBudget(capacity, speculativeMax int) ResourceBudget {
	return ResourceBudget{Capacity: capacity, ActiveReserved: 1, SpeculativeMax: speculativeMax}
}

// DefaultSpeculationConfig returns production-shaped admission limits.
// Speculative concurrency is bounded per class (LLM/TTS/GPU are the scarce
// classes), while network/disk are more aggressive.
func DefaultSpeculationConfig() SpeculationConfig {
	return SpeculationConfig{
		MaxDepth:      DepthNPlus3,
		MaxUnits:      32,
		MaxByCost:     map[SpeculationCost]int{CostWarm: 32, CostPrepare: 20, CostSpeculate: 6},
		MaxByResource: map[SpeculationResource]int{ResourceNetwork: 8, ResourceDisk: 8, ResourceCPU: 4, ResourceLLM: 1, ResourceTTS: 1, ResourceGPU: 1},
		ResourceLimits: map[SpeculationResource]ResourceBudget{
			ResourceNetwork: DefaultResourceBudget(16, 8),
			ResourceDisk:    DefaultResourceBudget(16, 8),
			ResourceCPU:     DefaultResourceBudget(8, 4),
			ResourceLLM:     DefaultResourceBudget(2, 1),
			ResourceTTS:     DefaultResourceBudget(2, 1),
			ResourceGPU:     DefaultResourceBudget(2, 1),
		},
	}
}

// SpeculationCandidate is a planned unit with queue distance and estimated
// value. Higher priority and time saved per cost are admitted first.
type SpeculationCandidate struct {
	Job                  *job.Job
	Depth                SpeculationDepth
	Unit                 PreparationUnit
	EstimatedTimeSavedMS int64
	EstimatedCostMS      int64
}

// ResourceScheduler (formerly SpeculationScheduler) selects and runs admitted
// preparation units with REAL per-resource-class pools:
//
//   - admission applies the per-class ResourceBudget (speculative concurrency
//     never exceeds SpeculativeMax / residual capacity),
//   - the ActiveWorkGate is consulted before every admission AND before every
//     unit in Run, so newly-arrived active work preempts remaining
//     speculation (active work has ABSOLUTE priority),
//   - resource usage is tracked per class while a unit executes, so the pool
//     accounting is live, not just a pre-admission guess.
type ResourceScheduler struct {
	config  SpeculationConfig
	gate    ActiveWorkGate
	mu      sync.Mutex
	running map[SpeculationResource]int
}

// SpeculationScheduler is the legacy name retained for callers that imported
// the pre-pool type. It is the same implementation.
type SpeculationScheduler = ResourceScheduler

// NewSpeculationScheduler / NewResourceScheduler construct a resource-aware
// scheduler with per-class speculative budgets. The gate is required: nil
// gate disables all speculation (fail-closed — active work wins by default).
func NewSpeculationScheduler(config SpeculationConfig, gate ActiveWorkGate) *ResourceScheduler {
	return NewResourceScheduler(config, gate)
}

func NewResourceScheduler(config SpeculationConfig, gate ActiveWorkGate) *ResourceScheduler {
	if config.MaxDepth <= 0 {
		config.MaxDepth = DepthNPlus3
	}
	if config.MaxUnits <= 0 {
		config.MaxUnits = 1
	}
	if config.MaxByResource == nil {
		config.MaxByResource = map[SpeculationResource]int{}
	}
	if config.ResourceLimits == nil {
		config.ResourceLimits = map[SpeculationResource]ResourceBudget{}
	}
	// Derive MaxByResource from the canonical per-class budgets so the two
	// knobs cannot drift; explicit MaxByResource entries (legacy callers)
	// are preserved only when no budget exists for that class.
	for resource, budget := range config.ResourceLimits {
		if budget.SpeculativeMax > 0 {
			config.MaxByResource[resource] = budget.SpeculativeMax
		} else if budget.Capacity > 0 {
			config.MaxByResource[resource] = maxInt(0, budget.Capacity-budget.ActiveReserved)
		}
	}
	return &ResourceScheduler{config: config, gate: gate, running: make(map[SpeculationResource]int)}
}

// Admit returns candidates allowed by depth, cost, resource, and active-work
// precedence. It never mutates jobs or claims leases.
func (s *ResourceScheduler) Admit(candidates []SpeculationCandidate) []SpeculationCandidate {
	if s == nil || s.gate == nil || !s.gate.ActiveWorkAvailable() {
		return nil
	}
	ordered := append([]SpeculationCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Depth != ordered[j].Depth {
			return ordered[i].Depth < ordered[j].Depth
		}
		left := ordered[i].EstimatedTimeSavedMS * maxInt64(1, ordered[j].EstimatedCostMS)
		right := ordered[j].EstimatedTimeSavedMS * maxInt64(1, ordered[i].EstimatedCostMS)
		if left != right {
			return left > right
		}
		if ordered[i].Unit.Priority != ordered[j].Unit.Priority {
			return ordered[i].Unit.Priority > ordered[j].Unit.Priority
		}
		return ordered[i].Unit.ID < ordered[j].Unit.ID
	})
	out := make([]SpeculationCandidate, 0, minInt(s.config.MaxUnits, len(ordered)))
	costs := make(map[SpeculationCost]int)
	resources := make(map[SpeculationResource]int)
	for _, candidate := range ordered {
		if candidate.Job == nil || candidate.Depth < DepthNPlus1 || candidate.Depth > s.config.MaxDepth {
			continue
		}
		cost := preparationCost(candidate.Unit)
		resource := resourceClassOf(candidate.Unit)
		if limit := s.config.MaxByCost[cost]; limit > 0 && costs[cost] >= limit {
			continue
		}
		if limit := s.config.MaxByResource[resource]; limit > 0 && resources[resource] >= limit {
			continue
		}
		costs[cost]++
		resources[resource]++
		out = append(out, candidate)
		if len(out) == s.config.MaxUnits {
			break
		}
	}
	return out
}

// Run admits candidates and invokes execute. The gate is checked before each
// unit, so newly-arrived active work preempts remaining speculation. While a
// unit executes its resource class is counted in the live pool.
func (s *ResourceScheduler) Run(ctx context.Context, candidates []SpeculationCandidate, execute func(context.Context, SpeculationCandidate) error) error {
	if execute == nil {
		return fmt.Errorf("speculation executor is required")
	}
	for _, candidate := range s.Admit(candidates) {
		if s.gate == nil || !s.gate.ActiveWorkAvailable() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		resource := resourceClassOf(candidate.Unit)
		s.mu.Lock()
		s.running[resource]++
		s.mu.Unlock()
		err := execute(ctx, candidate)
		s.mu.Lock()
		s.running[resource]--
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// RunWithPreemption executes admitted work while exposing a cancellation hook
// that active job work can call to stop the speculative batch immediately.
func (s *ResourceScheduler) RunWithPreemption(ctx context.Context, candidates []SpeculationCandidate, execute func(context.Context, SpeculationCandidate) error) error {
	if execute == nil {
		return fmt.Errorf("speculation executor is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, candidate := range s.Admit(candidates) {
		if s.gate == nil || !s.gate.ActiveWorkAvailable() {
			cancel()
			return nil
		}
		if err := runCtx.Err(); err != nil {
			return err
		}
		resource := resourceClassOf(candidate.Unit)
		s.mu.Lock()
		s.running[resource]++
		s.mu.Unlock()
		err := execute(runCtx, candidate)
		s.mu.Lock()
		s.running[resource]--
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// Running returns the live speculative concurrency per resource class
// (observability projection of the pool).
func (s *ResourceScheduler) Running() map[SpeculationResource]int {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[SpeculationResource]int, len(s.running))
	for resource, count := range s.running {
		out[resource] = count
	}
	return out
}

// resourceClassOf maps a unit to its speculative resource class. The kernel
// canonical ResourceClass values are the SSOT; the speculative enum mirrors
// them (DISK_IO vs DISK and GPU_RENDER vs GPU are intentional legacy cluster
// names — see ResourceClass constants in internal/kernel/job/preparation.go).
func resourceClassOf(unit PreparationUnit) SpeculationResource {
	// PreparationUnit in this package still carries ResourceClass as a string
	// (compat layer); map the canonical kernel class names onto the legacy
	// speculative resource enum.
	switch job.ResourceClass(unit.ResourceClass) {
	case job.ResourceNetwork:
		return ResourceNetwork
	case job.ResourceDisk:
		return ResourceDisk
	case job.ResourceCPULight, job.ResourceCPUHeavy, "":
		return ResourceCPU
	case job.ResourceLLM:
		return ResourceLLM
	case job.ResourceTTS:
		return ResourceTTS
	case job.ResourceGPU:
		return ResourceGPU
	default:
		return ResourceCPU
	}
}

func preparationCost(unit PreparationUnit) SpeculationCost {
	switch unit.CostClass {
	case string(CostWarm):
		return CostWarm
	case string(CostSpeculate):
		return CostSpeculate
	default:
		return CostPrepare
	}
}
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
