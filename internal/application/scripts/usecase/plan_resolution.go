// Package usecase — plan_resolution.go: usecase struct + plan-phase helpers.
//
// Extracted from generate_one_usecase.go + generate_one_usecase_plan.go (July 2026).
// Owns: GenerateOneUseCase struct, NewGenerateOneUseCase, SetVoiceoverRouting,
// buildResolutionContext.
package usecase

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/adapters"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
) // GenerateOneUseCase orchestrates the unified pipeline for a single
// generation item. All dependencies are typed — no any on
// the public surface.
type GenerateOneUseCase struct {
	cfg             adapters.NormalizationConfig
	preparer        *GenerationPreparer
	engineRunner    *GenerationEngineRunner
	postprocessor   *GenerationPostprocessor
	log             *zap.Logger
	voGroupResolver scriptports.VoiceoverGroupResolver
	voRootID        string
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
	return &GenerateOneUseCase{
		cfg:           cfg,
		preparer:      preparer,
		engineRunner:  engineRunner,
		postprocessor: postprocessor,
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
	uc.voGroupResolver = resolver
	uc.voRootID = parentID
	uc.preparer.SetVoiceoverRouting(resolver, parentID)
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
		RequireDriveLink: true,
	}
}
