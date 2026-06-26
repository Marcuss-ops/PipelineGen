// Package scripts — postprocessor_registry.go defines the
// PostProcessor interface and the PostProcessorRegistry that
// runs enabled processors in order. It replaces the monolithic
// Pipeline.Run with individually-testable processors.
//
// Each processor is opt-in: it runs only when its name appears
// in the plan's Postprocessors list. The registry respects the
// list order, which matches buildPostprocessorList ordering:
// entities → metadata → voiceover → images → document → persistence.
package scripts

import (
	"context"
	"fmt"

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
	EntitiesJSON  string
	Metadata      []VideoMetadata
	Voiceovers    []SceneVoiceover
	SceneImages   []SceneImage
	DocLink       string
	DocID         string
	ScriptID      int64
}

// PostProcessorRegistry runs enabled processors in order.
type PostProcessorRegistry struct {
	processors map[string]PostProcessor
	log        *zap.Logger
}

// NewPostProcessorRegistry creates an empty registry.
func NewPostProcessorRegistry(log *zap.Logger) *PostProcessorRegistry {
	return &PostProcessorRegistry{
		processors: make(map[string]PostProcessor),
		log:        log,
	}
}

// Register adds a processor. Overwrites any previous registration
// for the same name. Returns false when proc is nil.
func (r *PostProcessorRegistry) Register(proc PostProcessor) bool {
	if r == nil || proc == nil {
		return false
	}
	if r.log != nil {
		r.log.Debug("postprocessor registered", zap.String("name", proc.Name()))
	}
	r.processors[proc.Name()] = proc
	return true
}

// Run executes every processor whose name appears in the plan's
// Postprocessors list, in list order. Each processor is run
// independently; a failure in one processor does not abort the
// remaining processors — errors are collected as warnings in the
// result and the failing processor's output is skipped.
func (r *PostProcessorRegistry) Run(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	script string,
) (*PipelineResult, error) {
	if r == nil || len(r.processors) == 0 {
		return &PipelineResult{}, nil
	}

	result := &PipelineResult{}
	var warnings []string

	for _, name := range plan.Postprocessors {
		proc, ok := r.processors[name]
		if !ok || proc == nil {
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
