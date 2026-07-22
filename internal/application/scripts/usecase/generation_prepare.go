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
package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// PreparedGeneration holds everything produced by the prepare phase
// that the engine and postprocess phases need.
type PreparedGeneration struct {
	Item            scriptpkg.GenerationItemV2
	Plan            scriptpkg.ResolvedGenerationPlan
	ResolvedSource  *scriptpkg.ResolvedSource
	SourceResolveMs int64
	PlanBuildMs     int64
}

// GenerationPreparer orchestrates the prepare phase for a single
// generation item. It is constructed once per use case and reused
// across calls.
type GenerationPreparer struct {
	cfg             adapters.NormalizationConfig
	registry        *adapters.SourceRegistry
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
	cfg adapters.NormalizationConfig,
	registry *adapters.SourceRegistry,
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
// PreparedGeneration. The method is deterministic and side-effect
// free except for tracker events and logging.
func (p *GenerationPreparer) Prepare(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	preset scriptpkg.Preset,
	tracker *ProgressTracker,
) (*PreparedGeneration, error) {
	// ── Phase 1: Normalize ──────────────────────────────────────────
	tracker.PhaseNormalize()
	adapters.NormalizeItem(&item, preset, p.cfg)

	// ── Phase 2: Validate ─────────────────────────────────────────────
	tracker.PhaseValidate()
	if err := ValidateItem(item); err != nil {
		return nil, p.logPhaseError(item, "validate", scriptpkg.ErrPlanInvalid, err, tracker)
	}
	tracker.TrackEvent("request.validated", "Generation request validated", map[string]any{
		"item_id":     item.ID,
		"source_type": string(item.Source.Type),
	})

	// ── Phase 3: Source enrichment / resolution ───────────────────────
	tracker.PhaseResolveSource()
	sourceStart := time.Now()
	var resolved *scriptpkg.ResolvedSource
	if p.registry != nil {
		resCtx := buildResolutionContext(item)

		// Try the research cache short-circuit first. On hit the real
		// resolver (and any LLM it would call) is skipped entirely.
		cacheHit := false
		if p.enricher != nil {
			cacheResult, cacheErr := p.enricher.Enrich(ctx, &item)
			if cacheErr != nil {
				return nil, p.logPhaseError(item, "source_enrichment", scriptpkg.ErrSourceResolutionFailed, cacheErr, tracker)
			}
			cacheHit = cacheResult == scriptports.EnrichHit
		}

		if !cacheHit {
			var resolveErr error
			resolved, resolveErr = p.registry.Resolve(ctx, item.Source, resCtx)
			if resolveErr != nil {
				return nil, p.logPhaseError(item, "source_resolve", scriptpkg.ErrSourceResolutionFailed, resolveErr, tracker)
			}
			// Persist the freshly resolved source text so the next
			// identical request avoids the LLM call.
			if p.enricher != nil && resolved != nil && resolved.SourceText != "" {
				if saveErr := p.enricher.Save(ctx, item, resolved.SourceText); saveErr != nil && p.log != nil {
					p.log.Warn("source cache save failed", zap.String("item_id", item.ID), zap.Error(saveErr))
				}
			}
		} else {
			// Cache hit already populated item.Source.SourceText; build a
			// minimal ResolvedSource so downstream plan building works.
			resolved = &scriptpkg.ResolvedSource{
				Type:       scriptpkg.SourceType(item.Source.Type),
				Topic:      item.Source.Topic,
				Title:      item.Title,
				SourceText: item.Source.SourceText,
				Language:   item.Language,
			}
		}
	}
	sourceResolveMs := time.Since(sourceStart).Milliseconds()
	p.trackSourceResolved(item, resolved, tracker)

	// ── Phase 4: Build plan ─────────────────────────────────────────
	tracker.PhaseBuildPlan()
	planStart := time.Now()
	resolvedItem, resolveVOErr := ResolveVoiceoverFolderForItem(
		ctx, item, p.voGroupResolver, p.voRootID, p.log,
	)
	if resolveVOErr != nil {
		return nil, p.logPhaseError(item, "voiceover_resolve", scriptpkg.ErrVoiceoverResolveFailed, resolveVOErr, tracker)
	}
	item = resolvedItem
	plan := BuildPlan(item)

	// Merge resolved source into plan.
	if resolved != nil {
		if resolved.Topic != "" {
			plan.Topic = resolved.Topic
		}
		if resolved.Title != "" {
			plan.Title = resolved.Title
		}
		// Wave 2.x directive (PR-REFACTOR-P1-CYCLOMATIC, July 2026):
		// ResolvedSource.SourceText MUST NOT overwrite plan.SourceText.
		// rationale: plan.SourceText is owned by the normalizer + BuildPlan
		// (item.Source.SourceText); letting the source resolver overwrite it
		// re-introduces a side-effect where the same logical plan has two
		// textual identities (item.Source.SourceText vs resolved.SourceText).
		// The clip-evidence text assembled into plan.ClipEvidence.NarrativeText
		// remains the canonical editorial substrate (consumed by the engine
		// via plan.ClipEvidence.ModelSourceText()).
		if resolved.ClipEvidence != nil {
			plan.ClipEvidence = resolved.ClipEvidence
		}
		if resolved.Fingerprint != "" {
			plan.SourceFingerprint = resolved.Fingerprint
		}
		if resolved.Type != "" {
			plan.SourceKind = string(resolved.Type)
		}
		if item.Source.GroundingPolicy != "" {
			plan.GroundingPolicy = item.Source.GroundingPolicy
		}
	}
	plan.CacheKey = scriptpkg.BuildCacheKey(&plan)

	// ── Phase 4b: Clip evidence support check ─────────────────────────
	// For clip-based sources, the caller-provided source_text must not
	// exceed what the resolved clip evidence duration can support.
	if err := enforceClipEvidenceTextSupport(&plan, p.cfg); err != nil {
		return nil, p.logPhaseError(item, "plan_validation", scriptpkg.ErrPlanInvalid, err, tracker)
	}

	planBuildMs := time.Since(planStart).Milliseconds()
	if plan.ClipEvidence != nil && len(plan.ClipEvidence.AcceptedClipIDs) > 0 {
		tracker.TrackEvent("clip_evidence.built", "Clip evidence assembled into plan", map[string]any{
			"item_id":    item.ID,
			"clip_count": len(plan.ClipEvidence.AcceptedClipIDs),
		})
	}
	tracker.TrackEvent("narrative.planned", "Generation plan built", map[string]any{
		"item_id":      item.ID,
		"source_kind":  plan.SourceKind,
		"target_words": plan.TargetWords,
	})

	if p.ppReg != nil {
		if err := p.ppReg.ValidateRequested(plan.Postprocessors); err != nil {
			return nil, p.logPhaseError(item, "registry_validate", scriptpkg.ErrPlanInvalid, err, tracker)
		}
	}

	return &PreparedGeneration{
		Item:            item,
		Plan:            plan,
		ResolvedSource:  resolved,
		SourceResolveMs: sourceResolveMs,
		PlanBuildMs:     planBuildMs,
	}, nil
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
		tracker.TrackEvent("stage.failed", "Pipeline stage failed", map[string]any{
			"item_id": item.ID,
			"phase":   phase,
			"error":   err.Error(),
		})
	}
	return fmt.Errorf("%w: %w: %w", scriptpkg.ErrScriptGenerationFailed, sentinel, err)
}

// enforceClipEvidenceTextSupport rejects source_text that exceeds
// what the resolved clip evidence duration can support. It only
// applies to clip-based sources when the caller provided source_text
// and WordsPerSecondClipEvidence is configured.
func enforceClipEvidenceTextSupport(plan *scriptpkg.ResolvedGenerationPlan, cfg adapters.NormalizationConfig) error {
	if cfg.WordsPerSecondClipEvidence <= 0 {
		return nil
	}
	if plan.ClipEvidence == nil || len(plan.ClipEvidence.ClipDetails) == 0 {
		return nil
	}
	if plan.SourceText == "" {
		return nil
	}
	if !scriptpkg.IsClipSourceType(scriptpkg.SourceType(plan.SourceKind)) {
		return nil
	}

	var totalSeconds float64
	for _, detail := range plan.ClipEvidence.ClipDetails {
		if detail.EndMs > detail.StartMs {
			totalSeconds += float64(detail.EndMs-detail.StartMs) / 1000.0
		}
	}
	if totalSeconds <= 0 {
		return nil
	}

	words := countWords(plan.SourceText)
	maxWords := int(totalSeconds * cfg.WordsPerSecondClipEvidence)
	if words <= maxWords {
		return nil
	}

	return &scriptpkg.PayloadValidationError{
		Code:      "SOURCE_TEXT_EXCEEDS_CLIP_EVIDENCE",
		Message:   "source_text word count exceeds what the available clip evidence duration can support",
		Stage:     "plan.validation",
		Retryable: false,
		Extra: map[string]any{
			"actual_words":     words,
			"max_words":        maxWords,
			"evidence_seconds": totalSeconds,
			"words_per_second": cfg.WordsPerSecondClipEvidence,
			"source_type":      plan.SourceKind,
		},
	}
}
