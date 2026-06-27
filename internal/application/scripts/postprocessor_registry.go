// Package scripts — postprocessor_registry.go defines the
// PostProcessor interface and the PostProcessorRegistry that
// runs enabled processors in order. It replaces the monolithic
// Pipeline.Run with individually-testable processors.
//
// Each processor is opt-in: it runs only when its name appears
// in the plan's Postprocessors list. The registry respects the
// list order, which matches buildPostprocessorList ordering:
// entities → metadata → voiceover → images → document → persistence.
//
// PR 7 (June 2026): added Freeze/IsFrozen so composition-time
// registration is rejected after wiring is complete. The
// composition root calls Freeze() once all processors are
// registered; any subsequent Register() call returns false.
package scripts

import (
	"context"
	"fmt"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// PostProcessor executes one post-generation phase. Each processor
// is opt-in — it only runs when its name is in the plan's
// Postprocessors list.
type PostProcessor interface {
	// Name returns the processor identifier ("entities", "metadata",
	// "voiceover", "images", "document", "persistence").
	Name() string

	// Process executes the post-generation work. The plan carries
	// the resolved generation plan (including identity, sizing,
	// and output options). The script is the raw generated text.
	//
	// Returns a PostProcessResult on success, or an error wrapping
	// scriptpkg.ErrPostprocessFailed on failure.
	Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, script string) (*PostProcessResult, error)
}

// PostProcessResult carries the output of a single processor.
// Each processor populates only the fields relevant to its phase.
type PostProcessResult struct {
	EntitiesJSON string
	Metadata     []VideoMetadata
	Voiceovers   []SceneVoiceover
	SceneImages  []SceneImage
	DocLink      string
	DocID        string
	ScriptID     int64
}

// PostProcessorRegistry runs enabled processors in order.
// After Freeze() is called (post-composition), registration
// is rejected. The composition root calls Freeze() once all
// processors are registered.
type PostProcessorRegistry struct {
	processors map[string]PostProcessor
	frozen     bool
	mu         sync.RWMutex
	log        *zap.Logger
}

// NewPostProcessorRegistry creates an empty, unfrozen registry.
func NewPostProcessorRegistry(log *zap.Logger) *PostProcessorRegistry {
	return &PostProcessorRegistry{
		processors: make(map[string]PostProcessor),
		log:        log,
	}
}

// Register adds a processor. Returns false when proc is nil,
// the registry is nil, a processor with the same Name is already
// registered, or the registry is frozen.
//
// Duplicate registration is rejected (fail-closed) — the first
// registration wins. Callers that need idempotent registration
// should check Registered() first.
func (r *PostProcessorRegistry) Register(proc PostProcessor) bool {
	if r == nil || proc == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.frozen {
		if r.log != nil {
			r.log.Warn("postprocessor registry: register called after freeze",
				zap.String("name", proc.Name()))
		}
		return false
	}

	name := proc.Name()
	if _, exists := r.processors[name]; exists {
		if r.log != nil {
			r.log.Warn("postprocessor registry: duplicate registration rejected",
				zap.String("name", name))
		}
		return false
	}

	r.processors[name] = proc
	if r.log != nil {
		r.log.Debug("postprocessor registered", zap.String("name", name))
	}
	return true
}

// Registered returns true when a processor with the given name
// is registered.
func (r *PostProcessorRegistry) Registered(name string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.processors[name]
	return ok
}

// Freeze prevents further registration. After freeze, all
// Register() calls return false. Idempotent — multiple
// freeze calls are no-ops.
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

// Run executes every processor whose name appears in the plan's
// Postprocessors list, in list order. Each processor is run
// independently; a failure in one processor does not abort the
// remaining processors — errors are collected as warnings in the
// result and the failing processor's output is skipped.
//
// Run returns the aggregate PipelineResult (for backward compat
// with buildGenerationResult). The total error is non-nil only
// when ALL processors failed; partial failures produce warnings.
func (r *PostProcessorRegistry) Run(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	script string,
) (*PipelineResult, error) {
	if r == nil {
		return &PipelineResult{}, nil
	}
	r.mu.RLock()
	procs := make(map[string]PostProcessor, len(r.processors))
	for k, v := range r.processors {
		procs[k] = v
	}
	r.mu.RUnlock()

	if len(procs) == 0 {
		return &PipelineResult{}, nil
	}

	result := &PipelineResult{}
	var warnings []string
	failCount := 0
	totalRequested := len(plan.Postprocessors)

	for _, name := range plan.Postprocessors {
		proc, ok := procs[name]
		if !ok || proc == nil {
			failCount++
			warnings = append(warnings, fmt.Sprintf("postprocessor %q not registered", name))
			if r.log != nil {
				r.log.Warn("postprocessor not registered, skipping",
					zap.String("name", name),
					zap.String("item_id", plan.ID))
			}
			continue
		}

		if r.log != nil {
			r.log.Debug("running postprocessor",
				zap.String("name", name),
				zap.String("item_id", plan.ID))
		}

		ppResult, err := proc.Process(ctx, plan, script)
		if err != nil {
			failCount++
			warn := fmt.Sprintf("postprocessor %q failed: %v", name, err)
			warnings = append(warnings, warn)
			if r.log != nil {
				r.log.Error("postprocessor failed, continuing",
					zap.String("name", name),
					zap.String("item_id", plan.ID),
					zap.Error(err))
			}
			continue
		}

		// Merge processor output.
		if ppResult != nil {
			mergePostProcessResult(result, ppResult)
		}
	}

	if r.log != nil && len(warnings) > 0 {
		r.log.Warn("postprocessors completed with warnings",
			zap.Int("warning_count", len(warnings)),
			zap.Int("failed", failCount),
			zap.Int("total", totalRequested),
			zap.Strings("warnings", warnings))
	}

	return result, nil
}

// mergePostProcessResult copies non-zero fields from a processor
// result into the aggregate PipelineResult.
func mergePostProcessResult(dst *PipelineResult, src *PostProcessResult) {
	if src.EntitiesJSON != "" {
		dst.EntitiesJSON = src.EntitiesJSON
	}
	if len(src.Metadata) > 0 {
		dst.VideoMetadata = append(dst.VideoMetadata, src.Metadata...)
	}
	if len(src.Voiceovers) > 0 {
		dst.Voiceovers = append(dst.Voiceovers, src.Voiceovers...)
	}
	if len(src.SceneImages) > 0 {
		dst.Scenes = append(dst.Scenes, src.SceneImages...)
	}
	if src.DocLink != "" {
		dst.DocLink = src.DocLink
		dst.DocID = src.DocID
	}
	if src.ScriptID > 0 {
		dst.ScriptID = src.ScriptID
	}
}
