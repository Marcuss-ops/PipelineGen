// Package scripts — processor_images.go generates AI images for
// each scene. Enabled as "images" in the plan's Postprocessors list.
//
// PR 3 (June 2026): the processor now walks model.SpecScene.Scenes by
// reference and writes back into scene.Bindings.Image directly. The
// pre-PR-3 splitScriptIntoSegments + sceneCountFromPlan helpers are
// gone: the model is the single source of truth for scene count and
// scene narration text. Bindings.Image.URL / Prompt / Status are
// stamped onto each scene by-index in a single loop.
//
// Partial failures (one scene fails) are collected — the processor
// does NOT abort on first error.
package scripts

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ImageProcessor generates scene images via ImageGenService.
// Walks model.SpecScene.Scenes by reference and mutates
// scene.Bindings.Image.
type ImageProcessor struct {
	gen ImageGenService
	log *zap.Logger
}

// NewImageProcessor creates an ImageProcessor.
// gen must be non-nil (enforced at registration time by wire_script.go).
func NewImageProcessor(gen ImageGenService, log *zap.Logger) *ImageProcessor {
	return &ImageProcessor{gen: gen, log: log}
}

func (p *ImageProcessor) Name() string { return "images" }

// Process walks model.SpecScene.Scenes by index. For each scene it
// generates an image via ImageGenService.SearchAndDownload and stamps
// scene.Bindings.Image = &ImageBinding{...} with the resulting URL.
// Returns an empty *PostProcessArtifact — the image generation is a
// side effect on model.SpecScene.Scenes; the aggregate's other fields
// are not touched by this processor.
//
// PR 3 (June 2026): the second argument is the canonical typed
// *ModelScriptOutputV1. Iterators call SearchAndDownload with
// scene.Text as the prompt. Pre-PR-3 helpers
// (splitScriptIntoSegments, sceneCountFromPlan) are removed.
func (p *ImageProcessor) Process(
	ctx context.Context,
	plan *scriptpkg.ResolvedGenerationPlan,
	model *scriptpkg.ModelScriptOutputV1,
	_ *PostProcessArtifact,
) (*PostProcessArtifact, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: image processor: ImageGenService not configured", scriptpkg.ErrPostprocessFailed)
	}
	if model == nil || plan == nil {
		return &PostProcessArtifact{}, nil
	}
	scenes := model.SpecScene.Scenes
	if len(scenes) == 0 {
		if p.log != nil {
			p.log.Debug("image processor: no scenes (empty specscene)",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessArtifact{}, nil
	}

	language := plan.Language
	if language == "" {
		language = "en"
	}

	var warnings []string
	succeeded := 0

	for i := range scenes {
		scene := &scenes[i]
		sceneText := strings.TrimSpace(scene.Text)
		if sceneText == "" {
			sceneText = fmt.Sprintf("Scene %d", i+1)
		}

		sceneName := strings.TrimSpace(scene.ID)
		if sceneName == "" {
			sceneName = fmt.Sprintf("scene-%d", i)
		}

		status := "generated"
		var url string
		asset, err := p.gen.SearchAndDownload(ctx, sceneName, sceneText, sceneText, language, nil)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("image generation failed for scene %d: %v", i, err))
			status = "failed"
		} else if asset != nil {
			url = asset.SourceURL
		} else {
			status = "empty_result"
		}

		// Stamp the binding onto the scene by reference. New or replace
		// the existing binding — the model emits the canonical shape, the
		// application layer fills in the asset URL.
		if scene.Bindings.Image == nil {
			scene.Bindings.Image = &scriptpkg.ImageBinding{}
		}
		scene.Bindings.Image.Prompt = sceneText
		scene.Bindings.Image.URL = url
		scene.Bindings.Image.Status = status

		if status == "generated" {
			succeeded++
		}
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("image processor: partial failures",
			zap.Int("total", len(scenes)),
			zap.Int("failed", len(warnings)),
			zap.Int("succeeded", succeeded),
			zap.Strings("warnings", warnings))
	}

	return &PostProcessArtifact{}, nil
}
