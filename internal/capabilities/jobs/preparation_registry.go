package jobs

import (
	"context"
	"fmt"
	"sort"
	"sync"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// PreparationPlanner builds the speculative work plan for one job type.
type PreparationPlanner interface {
	Plan(context.Context, *job.Job) (PreparationPlan, error)
}

// PreparationPlan is the planner output consumed by the preparation scheduler.
type PreparationPlan struct {
	JobID string
	Units []PreparationUnit
}

// PreparationUnit describes one independently cacheable preparation action.
type PreparationUnit struct {
	ID               string
	Kind             string
	Fingerprint      string
	DependsOn        []string
	ResourceClass    string
	Priority         int
	CostClass        string
	Reusable         bool
	Inputs           job.InputManifest
	ProcessorVersion string
	SourceSHA256     string
	ParametersJSON   string
}

// JobPreparationRegistry resolves a planner by canonical job type.
type JobPreparationRegistry struct {
	mu       sync.RWMutex
	planners map[string]PreparationPlanner
	frozen   bool
}

func NewJobPreparationRegistry() *JobPreparationRegistry {
	return &JobPreparationRegistry{planners: make(map[string]PreparationPlanner)}
}

func (r *JobPreparationRegistry) Register(jobType string, planner PreparationPlanner) error {
	if r == nil {
		return fmt.Errorf("preparation registry is nil")
	}
	if jobType == "" {
		return fmt.Errorf("preparation job type must not be empty")
	}
	if planner == nil {
		return fmt.Errorf("preparation planner for %q must not be nil", jobType)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return fmt.Errorf("preparation registry is frozen: cannot register %s", jobType)
	}
	if _, exists := r.planners[jobType]; exists {
		return fmt.Errorf("preparation planner for %q already registered", jobType)
	}
	r.planners[jobType] = planner
	return nil
}

func (r *JobPreparationRegistry) Resolve(jobType string) (PreparationPlanner, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	planner, ok := r.planners[jobType]
	return planner, ok
}

func (r *JobPreparationRegistry) Plan(ctx context.Context, j *job.Job) (PreparationPlan, error) {
	if j == nil {
		return PreparationPlan{}, fmt.Errorf("preparation job must not be nil")
	}
	planner, ok := r.Resolve(j.Type)
	if !ok {
		return PreparationPlan{}, fmt.Errorf("no preparation planner registered for job type %q", j.Type)
	}
	plan, err := planner.Plan(ctx, j)
	if err != nil {
		return PreparationPlan{}, fmt.Errorf("plan preparation for %q: %w", j.Type, err)
	}
	if plan.JobID == "" {
		plan.JobID = j.ID
	}
	if err := plan.Validate(); err != nil {
		return PreparationPlan{}, fmt.Errorf("validate preparation plan for %q: %w", j.Type, err)
	}
	return plan, nil
}

func (r *JobPreparationRegistry) Freeze() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func (r *JobPreparationRegistry) IsFrozen() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

func (r *JobPreparationRegistry) AllTypes() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.planners))
	for jobType := range r.planners {
		out = append(out, jobType)
	}
	sort.Strings(out)
	return out
}

// RegisterPreparationPlanner is the explicit composition-root registration seam.
func RegisterPreparationPlanner(registry *JobPreparationRegistry, jobType string, planner PreparationPlanner) error {
	if registry == nil {
		return fmt.Errorf("preparation registry is nil")
	}
	return registry.Register(jobType, planner)
}
