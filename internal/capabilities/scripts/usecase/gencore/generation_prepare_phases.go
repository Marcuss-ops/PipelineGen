package gencore

import (
	"context"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"
	generationpkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/generation"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"

	"go.uber.org/zap"
)

// normalizePhase applies the canonical item normalization. The canonical Run
// owns the timing; MeasureStageReport is the only clock.
func (p *GenerationPreparer) normalizePhase(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	preset scriptpkg.Preset,
	tracker *ProgressTracker,
) (scriptpkg.GenerationItemV2, kernobs.StageReport, error) {
	tracker.PhaseNormalize()
	report, err := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptNormalize, func(stageCtx context.Context) error {
		processor.NormalizeItem(&item, preset, p.cfg)
		return nil
	})
	if err != nil {
		return item, report, p.logPhaseError(item, "normalize", scriptpkg.ErrPlanInvalid, err, tracker)
	}
	return item, report, nil
}

func (p *GenerationPreparer) validatePhase(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	tracker *ProgressTracker,
) (kernobs.StageReport, error) {
	tracker.PhaseValidate()
	report, err := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptValidate, func(stageCtx context.Context) error {
		return ValidateItem(item)
	})
	if err != nil {
		return report, p.logPhaseError(item, "validate", scriptpkg.ErrPlanInvalid, err, tracker)
	}
	tracker.TrackEvent("request.validated", "Generation request validated", map[string]any{
		"item_id":     item.ID,
		"source_type": string(item.Source.Type),
	})
	return report, nil
}

// resolvePhase runs source enrichment inside the canonical source.resolve
// stage boundary. The resolved source escapes the closure so plan building can
// consume it.
func (p *GenerationPreparer) resolvePhase(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	tracker *ProgressTracker,
) (*scriptpkg.ResolvedSource, kernobs.StageReport, error) {
	tracker.PhaseResolveSource()
	var resolved *scriptpkg.ResolvedSource
	var report kernobs.StageReport
	if p.registry != nil {
		var err error
		report, err = kernobs.MeasureStageReport(ctx, scriptgen.StageSourceResolve, func(stageCtx context.Context) error {
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
		})
		if err != nil {
			return nil, report, err
		}
	}
	p.trackSourceResolved(item, resolved, tracker)
	return resolved, report, nil
}

// buildPlanPhase encloses voiceover routing, plan build, resolved-source merge,
// cache-key derivation and the clip evidence + registry validation gates.
func (p *GenerationPreparer) buildPlanPhase(
	ctx context.Context,
	item scriptpkg.GenerationItemV2,
	resolved *scriptpkg.ResolvedSource,
	tracker *ProgressTracker,
) (scriptpkg.GenerationItemV2, scriptpkg.ResolvedGenerationPlan, kernobs.StageReport, error) {
	tracker.PhaseBuildPlan()
	var plan scriptpkg.ResolvedGenerationPlan
	report, err := kernobs.MeasureStageReport(ctx, scriptgen.StageScriptPlan, func(stageCtx context.Context) error {
		resolvedItem, resolveVOErr := ResolveVoiceoverFolderForItem(
			stageCtx, item, p.voGroupResolver, p.voRootID, p.log,
		)
		if resolveVOErr != nil {
			return p.logPhaseError(item, "voiceover_resolve", scriptpkg.ErrVoiceoverResolveFailed, resolveVOErr, tracker)
		}
		item = resolvedItem
		plan = generationpkg.BuildPlan(item)

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
			if resolved.ResearchReport != nil {
				report := *resolved.ResearchReport
				if resolved.ResearchReport.Evidence != nil {
					report.Evidence = resolved.ResearchReport.Evidence.Clone()
				}
				plan.ResearchReport = &report
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
		clipEvidenceCfg := p.cfg
		if clipEvidenceCfg.WordsPerSecondClipEvidence <= 0 {
			clipEvidenceCfg.WordsPerSecondClipEvidence = defaultWordsPerSecondClipEvidence
		}
		// Source resolution replaces plan.SourceText with the canonical
		// model-facing evidence projection. Validate the caller's explicit
		// source_text before that replacement, otherwise an oversized request
		// can evade the duration budget.
		clipSupportPlan := plan
		if explicit := strings.TrimSpace(item.Source.SourceText); explicit != "" {
			clipSupportPlan.SourceText = explicit
		}
		if err := enforceClipEvidenceTextSupport(&clipSupportPlan, clipEvidenceCfg); err != nil {
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
	})
	if err != nil {
		return item, plan, report, err
	}
	return item, plan, report, nil
}
