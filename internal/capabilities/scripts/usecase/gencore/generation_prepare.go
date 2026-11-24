// Package usecase — generation_prepare.go owns the canonical
// prepare phase for single-item script generation.
//
// Responsibilities:
//   - normalize the incoming GenerationItemV2
//   - validate the normalized item
//   - resolve the source via SourceRegistry (with source-text cache short-circuit)
//   - resolve voiceover group → folder ID
//   - build the ResolvedGenerationPlan
//   - derive the cache key
//   - validate clip evidence text support
//   - validate requested postprocessors
//
// The prepare phase is intentionally stateless except for its
// dependencies. It returns a PreparedGeneration value object that
// feeds the engine and postprocess phases.
package gencore

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// PreparedGeneration holds everything produced by the prepare phase
// that the engine and postprocess phases need.
type PreparedGeneration struct {
	Item           scriptpkg.GenerationItemV2
	Plan           scriptpkg.ResolvedGenerationPlan
	ResolvedSource *scriptpkg.ResolvedSource
}

// PrepareStageReports holds the canonical StageReport observations for the
// prepare substages. The orchestrator projects them into the legacy
// GenerationTimings via CanonicalTimingAdapter (no second clock). Reports are
// zero-valued when no Run is bound (instrumentation never changes behaviour).
type PrepareStageReports struct {
	Normalize kernobs.StageReport
	Validate  kernobs.StageReport
	Resolve   kernobs.StageReport
	Plan      kernobs.StageReport
}

// GenerationPreparer orchestrates the prepare phase for a single
// generation item. It is constructed once per use case and reused
// across calls.
type GenerationPreparer struct {
	cfg             processor.NormalizationConfig
	registry        *processor.SourceRegistry
	ppReg           *adapters.PostProcessorRegistry
	voGroupResolver scriptports.VoiceoverGroupResolver
	voRootID        string
	log             *zap.Logger
	enricher        scriptports.SourceTextEnricher
}

// SetSourceTextEnricher wires the source-text enricher into the prepare phase.
func (p *GenerationPreparer) SetSourceTextEnricher(enricher scriptports.SourceTextEnricher) {
	if p != nil {
		p.enricher = enricher
	}
}

// SetTopicSourceCache wires the source-text cache into the prepare phase.
func (p *GenerationPreparer) SetTopicSourceCache(cache scriptports.TopicSourceCache) {
	if p != nil {
		p.enricher = NewSourceTextEnricher(cache, p.log)
	}
}

// NewGenerationPreparer constructs a GenerationPreparer.
// registry may be nil (source resolution is skipped); ppReg may be
// nil (postprocessor validation is skipped).
func NewGenerationPreparer(
	cfg processor.NormalizationConfig,
	registry *processor.SourceRegistry,
	ppReg *adapters.PostProcessorRegistry,
	log *zap.Logger,
) *GenerationPreparer {
	return &GenerationPreparer{
		cfg:      cfg,
		registry: registry,
		ppReg:    ppReg,
		log:      log,
	}
}

// SetVoiceoverRouting wires the resolver and parent ID used by the
// pre-BuildPlan step (fix/voiceover-group-resolver, June 2026).
func (p *GenerationPreparer) SetVoiceoverRouting(resolver scriptports.VoiceoverGroupResolver, parentID string) {
	if p == nil {
		return
	}
	p.voGroupResolver = resolver
	p.voRootID = parentID
}

// Prepare runs the prepare phase for a single item and returns a
// PreparedGeneration plus the canonical substage reports. The method is
// deterministic and side-effect free except for tracker events and logging.
func (p *GenerationPreparer) Prepare(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	preset scriptpkg.Preset,
	tracker *ProgressTracker,
) (*PreparedGeneration, PrepareStageReports, error) {
	var reports PrepareStageReports
	var err error

	item, reports.Normalize, err = p.normalizePhase(ctx, item, preset, tracker)
	if err != nil {
		return nil, PrepareStageReports{}, err
	}
	reports.Validate, err = p.validatePhase(ctx, item, tracker)
	if err != nil {
		return nil, PrepareStageReports{}, err
	}
	var resolved *scriptpkg.ResolvedSource
	resolved, reports.Resolve, err = p.resolvePhase(ctx, item, tracker)
	if err != nil {
		return nil, PrepareStageReports{}, err
	}
	var plan scriptpkg.ResolvedGenerationPlan
	item, plan, reports.Plan, err = p.buildPlanPhase(ctx, item, resolved, tracker)
	if err != nil {
		return nil, PrepareStageReports{}, err
	}

	return &PreparedGeneration{
		Item:           item,
		Plan:           plan,
		ResolvedSource: resolved,
	}, reports, nil
}

// trackSourceResolved emits tracker events for source resolution.
func (p *GenerationPreparer) trackSourceResolved(
	item scriptpkg.GenerationItemV2,
	resolved *scriptpkg.ResolvedSource,
	tracker *ProgressTracker,
) {
	if resolved == nil || resolved.ClipEvidence == nil {
		return
	}
	if len(resolved.ClipEvidence.AcceptedClipIDs) > 0 {
		tracker.TrackEvent("clips.hydrated", "Clip source material hydrated", map[string]any{
			"item_id":    item.ID,
			"clip_count": len(resolved.ClipEvidence.AcceptedClipIDs),
			"clip_ids":   resolved.ClipEvidence.AcceptedClipIDs,
		})
	}
	missingCount := len(resolved.ClipEvidence.MissingClipIDs)
	excludedCount := len(resolved.ClipEvidence.Excluded)
	tracker.TrackEvent("clips.validated", "Clip source material validated", map[string]any{
		"item_id":        item.ID,
		"clip_count":     len(resolved.ClipEvidence.AcceptedClipIDs),
		"missing_count":  missingCount,
		"excluded_count": excludedCount,
		"valid":          missingCount == 0 && excludedCount == 0,
	})
}

// logPhaseError logs a phase failure and returns a typed wrapped error.
func (p *GenerationPreparer) logPhaseError(
	item scriptpkg.GenerationItemV2,
	phase string,
	sentinel error,
	err error,
	tracker *ProgressTracker,
) error {
	if p.log != nil {
		p.log.Warn("generate-one: phase failed",
			zap.String("item_id", item.ID),
			zap.String("phase", phase),
			zap.Error(err))
	}
	if tracker != nil {
		tracker.FailActivity(err)
		tracker.TrackEvent("stage.failed", "Pipeline stage failed", map[string]any{
			"item_id": item.ID,
			"phase":   phase,
			"error":   err.Error(),
		})
	}
	return fmt.Errorf("%w: %w: %w", scriptpkg.ErrScriptGenerationFailed, sentinel, err)
}
