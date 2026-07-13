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
)

// GenerateOneUseCase orchestrates the unified pipeline for a single
// generation item. All dependencies are typed — no any on
// the public surface.
type GenerateOneUseCase struct {
	cfg             adapters.NormalizationConfig
	registry        *adapters.SourceRegistry
	engine          *Engine
	ppReg           *adapters.PostProcessorRegistry
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
	return &GenerateOneUseCase{
		cfg:      cfg,
		registry: registry,
		engine:   engine,
		ppReg:    ppReg,
		log:      log,
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
		// P0 #3 (June 2026): DriveLink is only required when the
		// caller wants document or scene images. For text-only
		// generation, clips without Drive links are still usable.
		//
		// DRIFT-FIX SCOPE NOTE (July 2026, user directive
		// "nessun campo documentato come deprecato può essere
		// ancora materialmente rispettato"): the user's directive
		// applies to the script.generate pipeline post-Fase 2 per
		// the canonical deprecation manifest in
		// architecture/deprecations.yaml (records
		// OUTPUT_SPEC_{VOICEOVER,IMAGES,DOCUMENT}_FLAG each note:
		// "Runtime: setting the flag has no effect on SCIPT.GENERATE
		// pipeline"). The SOURCE RESOLUTION phase (driven from this
		// SourceResolutionContext, consumed by
		// source_resolver_{clips,catalog,search,curate}.go and by
		// clip_source_builder.go) is OUTSIDE the script.generate
		// pipeline. The 4 source resolvers gate on RequireDriveLink
		// to fail closed (godlike/07 NO-FAKE-AVAILABILITY) when a
		// clip candidate lacks a Drive link in pipelines that the
		// downstream rendering chain MUST drain. To preserve that
		// fail-closed contract — a regression source-resolver test
		// surface in clip_resolution_p0c_test.go +
		// clip_source_builder_fase4_strict_test.go would break
		// otherwise — the material-read on the
		// deprecation-registered flags is RETAINED here. The
		// migration of source-resolution consumers OFF the
		// deprecation-registered flags is a SEPARATE follow-up
		// migration (tracked in the wave-tracker; the cleanest
		// follow-up is INTRODUCING a non-deprecated
		// OutputSpec.SourceRequireDriveLink field, OUT OF SCOPE
		// for this drift-fix commit per AGENTS.md "do not add
		// features to production code unless the user explicitly
		// requested them").
		RequireDriveLink: item.Output.GenerateDocument.AsBool() || item.Output.GenerateSceneImages.AsBool(),
	}
}
