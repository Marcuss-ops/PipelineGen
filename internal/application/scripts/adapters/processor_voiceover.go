// Package scripts — processor_voiceover.go generates voiceovers for
// each scene. Enabled as "voiceover" in the plan's Postprocessors list.
//
// PR 9 (June 2026): the legacy scene-splitters
// (splitScriptIntoSegments / sceneCountFromPlan) were REMOVED.
// The processor now reads scenes directly from
// engineResult.Output.SpecScene.Scenes — the canonical structured
// output from PR 1, validated by PR 6's ValidateAndEnrichSpecScene.
// Each generated voiceover maps to a model-defined scene with
// stable indexes.
//
// P0-#3 final closure (July 2026): the local `VoiceoverService`
// interface (Generate + GenerateWithDestination, positional signature)
// is RETIRED. The processor now depends on the canonical
// `voiceover.VoiceoverItemExecutor` port (single Execute method with a
// typed *voiceover.GenerateVoiceoverItemCommand). The same per-item
// pipeline the voiceover.generate_item child job and the
// promoVoiceoverAdapter already route through. Composition root wires
// *voiceover.ProcessVoiceoverItemUseCase (the concrete
// VoiceoverItemExecutor implementation) via
// `internal/app/wire_script_postprocess.go::registerScriptPostProcessors`.
//
// Partial failures are collected — the processor does NOT abort on
// first error. No-op when plan has no ClipEvidence or when the model
// output has zero scenes.
package adapters

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/corid"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	"go.uber.org/zap"
)

// VoiceoverProcessor generates scene voiceovers via the canonical
// voiceover.VoiceoverItemExecutor port. Uses
// engineResult.Output.SpecScene.Scenes to drive per-scene voiceover
// generation (PR 9 contract).
//
// P0-#3 final closure (July 2026): the `gen` field is now
// `voiceover.VoiceoverItemExecutor` (the narrow typed port). The
// production concrete wired at composition time is
// *voiceover.ProcessVoiceoverItemUseCase (see
// `internal/app/build_bundles_voiceover.go::buildVoiceoverService`).
// Test doubles inject stubs that record invocations.
type VoiceoverProcessor struct {
	gen voiceover.VoiceoverItemExecutor
	log *zap.Logger
}

// NewVoiceoverProcessor creates a VoiceoverProcessor.
// gen must be non-nil (enforced at registration time by wire_script.go).
//
// P0-#3 final closure (July 2026): the `gen` parameter is now
// `voiceover.VoiceoverItemExecutor` (the canonical narrow port). The
// previous `VoiceoverService` interface (Generate + GenerateWithDestination)
// is RETIRED. Production wiring passes
// `root.Domains.VoiceoverProcessItem` (the *ProcessVoiceoverItemUseCase
// from the composition root). Test stubs implement the single
// `Execute(ctx, *GenerateVoiceoverItemCommand) (*VoiceoverItemResult, error)`
// method.
func NewVoiceoverProcessor(gen voiceover.VoiceoverItemExecutor, log *zap.Logger) *VoiceoverProcessor {
	return &VoiceoverProcessor{gen: gen, log: log}
}

func (p *VoiceoverProcessor) Name() ProcessorName { return ProcessorVoiceover }

// Policy classifies voiceover as ProcessorBestEffort: a missing
// voiceover executor (typed adapter nil at composition time) or a
// runtime TTS failure degrades into a Warning, not a hard failure.
// Voiceover is an auxiliary deliverable; per PR 2 spec: "voiceover =
// configurabile" (best-effort is the safe default). The plan arg is
// accepted for interface uniformity but ignored.
func (p *VoiceoverProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process generates per-scene voiceovers. PR 9 contract: scenes come
// directly from engineResult.Output.SpecScene.Scenes (validated by
// ValidateAndEnrichSpecScene); no paragraph-splitting helper is
// used.
//
// P0-#3 final closure (July 2026): the per-item Execute call is the
// canonical narrow-port surface — the SAME pipeline the
// voiceover.generate_item child job and the promoVoiceoverAdapter
// route through. Real failures surface as typed Go errors (no
// Result{OK:false} masking).
func (p *VoiceoverProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: voiceover processor: VoiceoverService not configured", scriptpkg.ErrPostprocessFailed)
	}

	if val, ok := ctx.Value("script_job_id").(string); !ok || val == "" {
		if parentJobID := corid.FromContext(ctx); parentJobID != "" {
			ctx = context.WithValue(ctx, "script_job_id", parentJobID)
		}
	}

	// PR 9: scenes sourced from canonical typed MSOV1.
	scenes := specScenesFromInput(input)
	if len(scenes) == 0 {
		if p.log != nil {
			p.log.Debug("voiceover processor: no scenes to render (no specscene scenes)",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessResult{}, nil
	}

	if input.Text == "" {
		return &PostProcessResult{}, nil
	}

	language := plan.TranslateTo
	if language == "" {
		language = plan.Language
	}
	if language == "" {
		language = defaults.DefaultScriptConfig().DefaultLanguage
	}

	items := make([]VoiceoverSceneInput, 0, len(scenes))
	for i, scene := range scenes {
		sceneText := scene.Text
		if sceneText == "" {
			sceneText = fmt.Sprintf("Scene %d", i+1)
		}

		// Sanitize the title for use in a filename, then build a
		// scene-stable filename: {title}_{scene_id}_{lang}.mp3.
		// VoiceoverProcessor used a local character-replacer (no .mp3,
		// no path-traversal guard); now delegates to the canonical
		// voiceover.SanitizeBasename which rejects path separators and
		// normalises unsafe characters via textutil.SanitizeFilename.
		sceneID := scene.ID
		if sceneID == "" {
			sceneID = fmt.Sprintf("%d", i+1)
		}
		// Sanitize sceneID too — it comes from model output and must
		// not contain path separators or unsafe filename characters.
		safeSceneID, serr2 := voiceover.SanitizeBasename(sceneID)
		if serr2 != nil {
			safeSceneID = fmt.Sprintf("s%d", i+1)
		}
		safeTitle, serr := voiceover.SanitizeBasename(plan.Title)
		if serr != nil {
			safeTitle = "scene"
		}
		filename := fmt.Sprintf("%s_%s_%s.mp3", safeTitle, safeSceneID, language)
		dest := &voiceover.DestinationRequest{Project: safeTitle}
		if plan.VoiceoverFolderID != "" {
			dest.FolderID = plan.VoiceoverFolderID
		} else if plan.VoiceoverGroup != "" && p.log != nil {
			p.log.Warn("voiceover processor: voiceover_group set but not resolved to folder_id — falling back to default folder",
				zap.String("voiceover_group", plan.VoiceoverGroup))
		}
		items = append(items, VoiceoverSceneInput{
			SceneIndex:  i,
			Text:        sceneText,
			Filename:    filename,
			Destination: dest,
		})
	}

	// P0-#3 final closure (July 2026): the fanout now takes the
	// canonical voiceover.VoiceoverItemExecutor port (the field
	// `p.gen`); real failures surface as typed Go errors per scene.
	outcomes := RunVoiceoverSceneFanout(ctx, p.gen, language, items, 4)
	voiceovers := make([]SceneVoiceover, 0, len(outcomes))
	var warnings []string
	for _, out := range outcomes {
		voiceovers = append(voiceovers, SceneVoiceover{
			SceneIndex: out.SceneIndex,
			Status:     out.Status,
			Link:       out.Link,
			LocalPath:  out.LocalPath,
		})
		if out.Status == "failed" {
			warnings = append(warnings, fmt.Sprintf("voiceover failed for scene %d: %s", out.SceneIndex, out.Error))
		}
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("voiceover processor: partial failures",
			zap.Int("total", len(items)),
			zap.Int("failed", len(warnings)),
			zap.Int("succeeded", CountCompletedSceneOutcomes(outcomes)),
			zap.Strings("warnings", warnings))
	}

	// fix/voiceover-propagate-warnings (June 2026): propagate per-scene
	// failures to PostProcessResult.Warnings so the canonical
	// GenerationResult.Warnings envelope in the API response reports
	// them. The zap log above stays — it serves operator tail/grep,
	// while the envelope is the client-facing canonical surface.
	return &PostProcessResult{
		Voiceovers: voiceovers,
		Warnings:   warnings,
	}, nil
}

// Compile-time assertion (AGENTS.md Pattern 0, June 2026): the
// canonical production concrete *voiceover.ProcessVoiceoverItemUseCase
// must structurally satisfy voiceover.VoiceoverItemExecutor. Drift
// between the production concrete's Execute signature and the port
// contract triggers a compile error here, not a silent runtime
// dispatch to a different surface. The same assertion is also pinned
// in process_voiceover_item.go (where the use case is defined); this
// is a redundant consumer-site drift detector so a future change to
// either side surfaces a build failure at the call site, not deep in
// the use case package.
var _ voiceover.VoiceoverItemExecutor = (*voiceover.ProcessVoiceoverItemUseCase)(nil)
