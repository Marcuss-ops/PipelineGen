// Package adapters — postprocessor_composite.go: core registry infrastructure.
//
// Extracted from postprocessor_registry.go (July 2026).
// Owns: PostProcessor interface, PostProcessorRegistry struct + all methods.
//
// PR-COMPOSITE-SPLIT (July 2026): decomposed into 3 files per AGENTS.md
// Pattern 5:
//
//	postprocessor_composite.go       — types + constructor + simple methods
//	                                    (this file)
//	postprocessor_composite_run.go   — Run method
//	postprocessor_composite_merge.go — mergePostProcessResult helper
package adapters

import (
	"context"
	"strings"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// PostProcessor executes one post-generation phase.
//
// PR 5 (June 2026): the second argument changed from `script string`
// to `input ProcessInput`. The envelope carries the canonical
// output text plus typed fields required by individual processors.
type PostProcessor interface {
	Name() ProcessorName
	Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy
	Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error)
}

// PostProcessorRegistry runs enabled processors in order.
type PostProcessorRegistry struct {
	processors map[ProcessorName]PostProcessor
	policies   map[ProcessorName]ProcessorPolicy
	frozen     bool
	mu         sync.RWMutex
	log        *zap.Logger
}

// NewPostProcessorRegistry creates an empty, unfrozen registry.
func NewPostProcessorRegistry(log *zap.Logger) *PostProcessorRegistry {
	return &PostProcessorRegistry{
		processors: make(map[ProcessorName]PostProcessor),
		policies:   make(map[ProcessorName]ProcessorPolicy),
		log:        log,
	}
}

// Register adds a processor.
func (r *PostProcessorRegistry) Register(proc PostProcessor) bool {
	if r == nil || proc == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		if r.log != nil {
			r.log.Warn("postprocessor registry: register called after freeze",
				zap.String("name", string(proc.Name())))
		}
		return false
	}

	name := proc.Name()
	if _, exists := r.processors[name]; exists {
		if r.log != nil {
			r.log.Warn("postprocessor registry: duplicate registration rejected",
				zap.String("name", string(name)))
		}
		return false
	}

	policy := proc.Policy(nil)
	r.processors[name] = proc
	r.policies[name] = policy
	if r.log != nil {
		r.log.Debug("postprocessor registered",
			zap.String("name", string(name)),
			zap.String("policy", string(policy)))
	}
	return true
}

// Registered returns true when a processor with the given name is registered.
func (r *PostProcessorRegistry) Registered(name ProcessorName) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.processors[name]
	return ok
}

// LookupPolicy returns the registered policy for the named processor.
func (r *PostProcessorRegistry) LookupPolicy(name ProcessorName) ProcessorPolicy {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.policies[name]
}

// Freeze prevents further registration.
func (r *PostProcessorRegistry) Freeze() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
	if r.log != nil {
		r.log.Debug("postprocessor registry: frozen",
			zap.Int("processors", len(r.processors)))
	}
}

// IsFrozen returns true after Freeze() has been called.
func (r *PostProcessorRegistry) IsFrozen() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// Len returns the number of registered processors.
func (r *PostProcessorRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.processors)
}

// ValidateRequested checks every name in the supplied list against the registry.
func (r *PostProcessorRegistry) ValidateRequested(names []ProcessorName) error {
	if r == nil || len(names) == 0 {
		return nil
	}

	seen := make(map[ProcessorName]struct{}, len(names))
	unique := make([]ProcessorName, 0, len(names))
	for _, n := range names {
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		unique = append(unique, n)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var missing []string
	for _, name := range unique {
		if _, ok := r.processors[name]; ok {
			continue
		}
		policy := r.policies[name]
		if policy == "" {
			policy = DefaultPolicyFor(name)
		}
		if policy == ProcessorRequired {
			missing = append(missing, string(name))
		} else if r.log != nil {
			r.log.Warn("postprocessor best-effort not registered at preflight",
				zap.String("name", string(name)))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &scriptpkg.PlanInvalidError{
		ItemID:  string(names[0]),
		Details: []string{"preflight: required postprocessor(s) not registered: " + strings.Join(missing, ", ")},
	}
}
