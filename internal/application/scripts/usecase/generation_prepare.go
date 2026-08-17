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

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"

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
// PreparedGeneration plus the canonical substage reports. The method is
// deterministic and side-effect free except for tracker events and logging.
func (p *GenerationPreparer) Prepare(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	preset scriptpkg.Preset,
	tracker *ProgressTracker,
) (*PreparedGeneration, PrepareStageReports, error) {
	var reports PrepareStageReports

	// ── Phase 1: Normalize ──────────────────────────────────────────
	// The canonical Run owns the timing; MeasureStageReport is the only
	// clock (no ad-hoc time.Now() at a phase boundary).
	tracker.PhaseNormalize()
	if report, err := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptNormalize, func(stageCtx context.Context) error {
		adapters.NormalizeItem(&item, preset, p.cfg)
		return nil
	}); err != nil {
		return nil, PrepareStageReports{}, p.logPhaseError(item, "normalize", scriptpkg.ErrPlanInvalid, err, tracker)
	} else {
		reports.Normalize = report
	}

	// ── Phase 2: Validate ─────────────────────────────────────────────
	tracker.PhaseValidate()
	if report, err := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptValidate, func(stageCtx context.Context) error {
		return ValidateItem(item)
	}); err != nil {
		return nil, PrepareStageReports{}, p.logPhaseError(item, "validate", scriptpkg.ErrPlanInvalid, err, tracker)
	} else {
		reports.Validate = report
	}
	tracker.TrackEvent("request.validated", "Generation request validated", map[string]any{
		"item_id":     item.ID,
		"source_type": string(item.Source.Type),
	})

	// ── Phase 3: Source enrichment / resolution ───────────────────────
	// source.resolve is a STAGE boundary; the cache/resolver calls are the
	// work it encloses. The resolved source escapes the closure so plan
	// building can consume it below.
	tracker.PhaseResolveSource()
	var resolved *scriptpkg.ResolvedSource
	if p.registry != nil {
		if report, err := kernobs.MeasureStageReport(ctx, scriptgen.StageSourceResolve, func(stageCtx context.Context) error {
			resCtx := buildResolutionContext(item)

			// Try the research cache short-circuit first. On hit the real
			// resolver (and any LLM it would call) is skipped entirely.
			cacheHit := false
			if p.enricher != nil {
				cacheResult, cacheErr := p.enricher.Enrich(stageCtx, &item)
				if cacheErr != nil {
					return p.logPhaseError(item, "source_enrichment", scriptpkg.ErrSourceResolutionFailed, cacheErr, tracker)
				}
				cacheHit = cacheResult == scriptports.EnrichHit
			}

			if !cacheHit {
				var resolveErr error
				resolved, resolveErr = p.registry.Resolve(stageCtx, item.Source, resCtx)
				if resolveErr != nil {
					return p.logPhaseError(item, "source_resolve", scriptpkg.ErrSourceResolutionFailed, resolveErr, tracker)
				}
				// Persist the freshly resolved source text so the next
				// identical request avoids the LLM call.
				if p.enricher != nil && resolved != nil && resolved.SourceText != "" {
					if saveErr := p.enricher.Save(stageCtx, item, resolved.SourceText); saveErr != nil && p.log != nil {
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
			return nil
		}); err != nil {
			return nil, PrepareStageReports{}, err
		} else {
			reports.Resolve = report
		}
	}
	p.trackSourceResolved(item, resolved, tracker)

	// ── Phase 4: Build plan ─────────────────────────────────────────
	// script.plan is a STAGE boundary enclosing voiceover routing, plan
	// build, resolved-source merge, cache-key derivation and the clip
	// evidence + registry validation gates. plan escapes the closure so
	// the orchestrator can return it.
	tracker.PhaseBuildPlan()
	var plan scriptpkg.ResolvedGenerationPlan
	if report, err := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptPlan, func(stageCtx context.Context) error {
		resolvedItem, resolveVOErr := ResolveVoiceoverFolderForItem(
			stageCtx, item, p.voGroupResolver, p.voRootID, p.log,
		)
		if resolveVOErr != nil {
			return p.logPhaseError(item, "voiceover_resolve", scriptpkg.ErrVoiceoverResolveFailed, resolveVOErr, tracker)
		}
		item = resolvedItem
		plan = BuildPlan(item)

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
			if resolved.ResearchEvidence != nil {
				plan.ResearchEvidence = resolved.ResearchEvidence.Clone()
			}
			if len(resolved.SearchResults) > 0 {
				plan.SearchResults = append([]scriptpkg.SearchResultItem(nil), resolved.SearchResults...)
			}
			if resolved.Fingerprint != "" {
				plan.SourceFingerprint = resolved.Fingerprint
			}
			if resolved.Type != "" {
				plan.SourceKind = string(resolved.Type)
			}
			if resolved.ResearchReport != nil {
				plan.ResearchSources = make([]scriptpkg.SourceReference, 0, len(resolved.ResearchReport.Sources))
				for _, source := range resolved.ResearchReport.Sources {
					plan.ResearchSources = append(plan.ResearchSources, scriptpkg.SourceReference{Title: source.Title, URL: source.URL, Type: "article"})
				}
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
			return p.logPhaseError(item, "plan_validation", scriptpkg.ErrPlanInvalid, err, tracker)
		}

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
			if err := p.ppReg.ValidateRequested(adapters.ProcessorNamesFromStrings(plan.Postprocessors)); err != nil {
				return p.logPhaseError(item, "registry_validate", scriptpkg.ErrPlanInvalid, err, tracker)
			}
		}
		return nil
	}); err != nil {
		return nil, PrepareStageReports{}, err
	} else {
		reports.Plan = report
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
		tracker.TrackEvent("stage.failed", "Pipeline stage failed", map[string]any{
			"item_id": item.ID,
			"phase":   phase,
			"error":   err.Error(),
		})
	}
	return fmt.Errorf("%w: %w: %w", scriptpkg.ErrScriptGenerationFailed, sentinel, err)
}
