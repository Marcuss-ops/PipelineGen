// Package scripts — processor_images.go generates AI images for
// each scene. Enabled as "images" in the plan's Postprocessors list.
//
// The processor iterates over the plan's ClipEvidence to derive scene
// count and context, generates an image for each scene via
// ImageGenService, and returns SceneImage results with preserved
// indexes. Partial failures (one scene fails) are collected — the
// processor does NOT abort on first error.
//
// No-op when plan has no ClipEvidence (text-only generation).
package scripts

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ImageProcessor generates scene images via ImageGenService.
// Uses plan.ClipEvidence to derive scene count and context.
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

func (p *ImageProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, script string) (*PostProcessResult, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: image processor: ImageGenService not configured", scriptpkg.ErrPostprocessFailed)
	}

	sceneCount := sceneCountFromPlan(plan)
	if sceneCount == 0 {
		if p.log != nil {
			p.log.Debug("image processor: no scenes (no clip evidence)",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessResult{}, nil
	}

	if script == "" {
		return &PostProcessResult{}, nil
	}

	segments := splitScriptIntoSegments(script, sceneCount)
	language := plan.Language
	if language == "" {
		language = "en"
	}

	images := make([]SceneImage, 0, sceneCount)
	var warnings []string

	for i := 0; i < sceneCount; i++ {
		sceneText := ""
		sceneName := fmt.Sprintf("scene-%d", i)
		if i < len(segments) {
			sceneText = segments[i]
		}
		if sceneText == "" {
			sceneText = fmt.Sprintf("Scene %d", i+1)
		}

		asset, err := p.gen.SearchAndDownload(ctx, sceneName, sceneText, sceneText, language, nil)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("image generation failed for scene %d: %v", i, err))
			images = append(images, SceneImage{Index: i, Text: sceneText})
			continue
		}

		url := ""
		if asset != nil {
			url = asset.SourceURL
		}

		images = append(images, SceneImage{
			Index: i,
			Text:  sceneText,
			URL:   url,
		})
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("image processor: partial failures",
			zap.Int("total", sceneCount),
			zap.Int("failed", len(warnings)),
			zap.Int("succeeded", len(images)-len(warnings)),
			zap.Strings("warnings", warnings))
	}

	return &PostProcessResult{SceneImages: images}, nil
}

// sceneCountFromPlan returns the number of scenes derived from clip evidence.
func sceneCountFromPlan(plan *scriptpkg.ResolvedGenerationPlan) int {
	if plan == nil || plan.ClipEvidence == nil {
		return 0
	}
	return plan.ClipEvidence.ClipCount
}

// splitScriptIntoSegments divides script text into roughly equal segments.
func splitScriptIntoSegments(script string, count int) []string {
	if count <= 0 {
		return nil
	}
	script = strings.TrimSpace(script)
	if count == 1 || script == "" {
		seg := make([]string, count)
		seg[0] = script
		return seg
	}
	paragraphs := strings.Split(script, "\n\n")
	segments := make([]string, count)
	if len(paragraphs) <= count {
		for i, p := range paragraphs {
			segments[i] = p
		}
		return segments
	}
	perSegment := len(paragraphs) / count
	remainder := len(paragraphs) % count
	idx := 0
	for i := 0; i < count && idx < len(paragraphs); i++ {
		n := perSegment
		if i < remainder {
			n++
		}
		end := idx + n
		if end > len(paragraphs) {
			end = len(paragraphs)
		}
		segments[i] = strings.Join(paragraphs[idx:end], "\n\n")
		idx = end
	}
	return segments
}
