// Package usecase — plan_resolution.go: usecase struct + plan-phase helpers.
//
// Extracted from generate_one_usecase.go + generate_one_usecase_plan.go (July 2026).
// Owns: GenerateOneUseCase struct, NewGenerateOneUseCase, SetVoiceoverRouting,
// buildResolutionContext.
package usecase

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
) // GenerateOneUseCase orchestrates the unified pipeline for a single
// generation item. It owns only the four phase collaborators and
// the canonical logger — no monolithic configuration state.
type GenerateOneUseCase struct {
	preparer       *GenerationPreparer
	engineRunner   *GenerationEngineRunner
	postprocessor  *GenerationPostprocessor
	finalizer      *GenerationFinalizer
	log            *zap.Logger
	audioProcessor mediaexec.AudioProcessor
}

func (uc *GenerateOneUseCase) SetAudioProcessor(processor mediaexec.AudioProcessor) {
	if uc != nil {
		uc.audioProcessor = processor
	}
}

// NewGenerateOneUseCase constructs the use case. engine and registry
// must be non-nil; ppReg may be nil (postprocessors are skipped).
// The voiceover_group resolver is optional — composition root wires
// it via SetVoiceoverRouting (post-construction, additive) so test
// fixtures that don't exercise routing continue to work without
// parameter churn.
func NewGenerateOneUseCase(
	cfg adapters.NormalizationConfig,
	registry *adapters.SourceRegistry,
	engine *Engine,
	ppReg *adapters.PostProcessorRegistry,
	log *zap.Logger,
) *GenerateOneUseCase {
	preparer := NewGenerationPreparer(cfg, registry, ppReg, log)
	engineRunner := NewGenerationEngineRunner(engine)
	postprocessor := NewGenerationPostprocessor(ppReg)
	finalizer := NewGenerationFinalizer(log, cfg)
	return &GenerateOneUseCase{
		preparer:      preparer,
		engineRunner:  engineRunner,
		postprocessor: postprocessor,
		finalizer:     finalizer,
		log:           log,
	}
}

// SetVoiceoverRouting wires the resolver and parent ID used by the
// pre-BuildPlan step (fix/voiceover-group-resolver, June 2026).
// Optional: if not called, resolver is nil and
// ResolveVoiceoverFolderForItem is a no-op (the existing test
// fixtures and default compositions skip this call, preserving
// behavior parity with pre-PR scripts).
//
// Pass an empty parentID to disable routing at runtime without
// nil-checking the resolver; an empty parentID makes the resolver
// return immediately because parentID == "" is rejected by the
// underlying GroupsResolver.
func (uc *GenerateOneUseCase) SetVoiceoverRouting(resolver scriptports.VoiceoverGroupResolver, parentID string) {
	if uc == nil {
		return
	}
	uc.preparer.SetVoiceoverRouting(resolver, parentID)
}

// SetTopicSourceCache wires the source-text cache into the prepare phase.
func (uc *GenerateOneUseCase) SetTopicSourceCache(cache scriptports.TopicSourceCache) {
	if uc != nil {
		uc.preparer.SetTopicSourceCache(cache)
	}
}

// SetMemoryService wires the gemmamemory service used to cache
// generated scripts in the finalizer. Optional: if not called, the
// finalizer does not persist to the script cache.
func (uc *GenerateOneUseCase) SetMemoryService(svc *adapters.Service) {
	if uc == nil {
		return
	}
	uc.finalizer.SetMemoryService(svc)
}

// SetVidRushCache wires the durable binding cache into the finalization
// phase. The processor-level provider caches are composed separately; this
// setter closes the cross-process warm-replay gap for scene bindings.
func (uc *GenerateOneUseCase) SetVidRushCache(cache scriptports.VidRushCachePort) {
	if uc == nil {
		return
	}
	uc.finalizer.SetVidRushCache(cache)
}

// ── Plan-phase helpers ────────────────────────────────────────────────

// buildResolutionContext constructs a SourceResolutionContext from a
// GenerationItemV2. PR 4 (June 2026): the resolver signature now
// expects resolution-context as a separate arg so resolvers see
// operator-side traits (language, tone, model, style, target words)
// without hijacking SourceSpec.Guidelines. SourceSpec.Guidelines
// remains for the pure-text editorial-overrides path; here we
// explicitly read operator intent from item-style fields.
//
// Field mapping:
//   - ItemID    — item.ID
//   - Title     — item.Title (canonical document title)
//   - Language  — item.Language (real target language; the curate
//     resolver previously hijacked Guidelines here —
//     the bug class)
//   - Tone      — item.Tone
//   - Model     — item.Model
//   - Style     — item.Style
//   - TargetWords — item.ScriptParams.TargetWords
func buildResolutionContext(item scriptpkg.GenerationItemV2) scriptpkg.SourceResolutionContext {
	return scriptpkg.SourceResolutionContext{
		ItemID:        item.ID,
		Title:         item.Title,
		Language:      item.Language,
		Tone:          item.Tone,
		Model:         item.Model,
		Style:         item.Style,
		TargetWords:   item.ScriptParams.TargetWords,
		NumClips:      item.Source.NumClips,
		SegmentWords:  item.ScriptParams.SegmentWords,
		SegmentTopics: append([]string(nil), item.ScriptParams.SegmentTopics...),
		Segments:      append([]scriptpkg.ScriptSegment(nil), item.ScriptParams.Segments...),
		// RequireDriveLink is hardcoded to true as the canonical
		// fail-closed default (godlike/07 NO-FAKE-AVAILABILITY). A
		// future source-resolution migration may introduce a non-
		// deprecated OutputSpec.SourceRequireDriveLink field to
		// restore caller override capability (OUT OF SCOPE per
		// AGENTS.md).
		RequireDriveLink:  true,
		RequireLocalMedia: item.Output.GenerateTimeline,
	}
}
