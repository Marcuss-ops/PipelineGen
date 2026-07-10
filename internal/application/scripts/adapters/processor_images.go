// Package scripts — processor_images.go generates AI images for
// each scene. Enabled as "images" in the plan's Postprocessors list.
//
// PR 9 (June 2026): the legacy scene-splitters
// (splitScriptIntoSegments / sceneCountFromPlan) were REMOVED.
// The processor now reads scenes directly from
// engineResult.Output.SpecScene.Scenes — the canonical structured
// output from PR 1, validated by PR 6's ValidateAndEnrichSpecScene.
// This eliminates the pre-V1 paragraph-splitting anti-pattern and
// ensures each generated image maps to a model-defined scene.
//
// Partial failures (one scene fails) are collected — the processor
// does NOT abort on first error. No-op when plan has no ClipEvidence
// or when the model output has zero scenes.
package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"

	domainasset "github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/defaults"

	"go.uber.org/zap"
)

// ── Typed port (PR 9 / PR 3) ─────────────────────────────────────────────

// ImageResult is the per-scene image generation outcome surfaced
// from ImageGenService.SearchAndDownload. The single SourceURL field
// is the public URL of the generated/uploaded asset.
type ImageResult struct {
	SourceURL   string
	DriveFileID string
}

// ImageGenService is the canonical port for image generation.
// Production implementations live in internal/application/images/
// (concrete *images.Service); stub implementations live in adapters/.
type ImageGenService interface {
	SearchAndDownload(ctx context.Context, sceneName, sceneText, altText, language string) (*ImageResult, error)
}

// imagePrewarmer is the optional warmup seam used by production image
// services that can pre-initialize their browser/session pool before
// the parallel scene fan-out starts.
type imagePrewarmer interface {
	TriggerPrewarm(ctx context.Context, jobID string, count int)
}

type smartImageGenService interface {
	GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*domainasset.ImageAsset, error)
}

// ImageProcessor generates scene images via ImageGenService.
// Uses engineResult.Output.SpecScene.Scenes to drive per-scene
// image generation (PR 9 contract).
type ImageProcessor struct {
	gen ImageGenService
	log *zap.Logger
}

// NewImageProcessor creates an ImageProcessor.
// gen must be non-nil (enforced at registration time by wire_script.go).
func NewImageProcessor(gen ImageGenService, log *zap.Logger) *ImageProcessor {
	return &ImageProcessor{gen: gen, log: log}
}

func (p *ImageProcessor) Name() ProcessorName { return ProcessorImages }

// Policy classifies images as ProcessorBestEffort: a missing image
// service (typed adapter nil at composition time) or a runtime failure
// degrades gracefully into a Warning + empty result, not a hard
// failure. Operators who need hard-failure semantics can flip the
// registered policy via a future PR (per PR 2 spec: "images =
// configurabile"). The plan arg is accepted for interface uniformity
// but ignored — images are unconditionally best-effort for now.
func (p *ImageProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

// Process generates per-scene images. PR 9 contract: scenes come
// directly from engineResult.Output.SpecScene.Scenes (validated by
// ValidateAndEnrichSpecScene); no paragraph-splitting helper is
// used.
func (p *ImageProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if p.gen == nil {
		return nil, fmt.Errorf("%w: image processor: ImageGenService not configured", scriptpkg.ErrPostprocessFailed)
	}

	scenes := specScenesFromInput(input)
	if len(scenes) == 0 {
		if p.log != nil {
			p.log.Debug("image processor: no scenes to render (no specscene scenes)",
				zap.String("item_id", plan.ID))
		}
		return &PostProcessResult{}, nil
	}

	if input.Text == "" {
		return &PostProcessResult{}, nil
	}

	language := plan.Language
	if language == "" {
		language = defaults.DefaultScriptConfig().DefaultLanguage
	}

	if prewarmer, ok := p.gen.(imagePrewarmer); ok {
		prewarmCount := imageFanoutConcurrency(len(scenes))
		prewarmer.TriggerPrewarm(ctx, plan.ID, prewarmCount)
	}

	outcomes := runImageSceneFanout(ctx, p.gen, plan, scenes, language)
	images := make([]SceneImage, 0, len(outcomes))
	var warnings []string
	for _, out := range outcomes {
		images = append(images, out.image)
		if out.warning != "" {
			warnings = append(warnings, out.warning)
		}
	}

	if len(warnings) > 0 && p.log != nil {
		p.log.Warn("image processor: partial failures",
			zap.Int("total", len(outcomes)),
			zap.Int("failed", len(warnings)),
			zap.Int("succeeded", len(images)-len(warnings)),
			zap.Strings("warnings", warnings))
	}

	return &PostProcessResult{SceneImages: images}, nil
}

// specScenesFromInput returns the canonical scene list from the
// ProcessInput envelope (typed MSOV1 output). PR 9 contract: post-
// processors consume scenes through this lens; any attempt to
// re-derive scenes via text-splitting is a regression caught by
// ci-architectural-checks Check 15.
func specScenesFromInput(input ProcessInput) []scriptpkg.SpecScene {
	if len(input.SpecScene.Scenes) == 0 {
		return nil
	}
	return input.SpecScene.Scenes
}

type imageSceneOutcome struct {
	image   SceneImage
	warning string
}

const defaultImageSceneConcurrency = 4

func imageFanoutConcurrency(sceneCount int) int {
	if sceneCount < 1 {
		return 1
	}
	if sceneCount < defaultImageSceneConcurrency {
		return sceneCount
	}
	return defaultImageSceneConcurrency
}

func runImageSceneFanout(
	ctx context.Context,
	gen ImageGenService,
	plan *scriptpkg.ResolvedGenerationPlan,
	scenes []scriptpkg.SpecScene,
	language string,
) []imageSceneOutcome {
	if len(scenes) == 0 {
		return nil
	}

	concurrency := imageFanoutConcurrency(len(scenes))
	outcomes := make([]imageSceneOutcome, len(scenes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)

	for i, scene := range scenes {
		i, scene := i, scene
		wg.Add(1)
		go func() {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				outcomes[i] = imageSceneOutcome{
					image:   SceneImage{Index: i, Text: fallbackSceneText(scene.Text, i)},
					warning: fmt.Sprintf("image generation failed for scene %d: %v", i, ctx.Err()),
				}
				return
			}
			defer func() { <-sem }()

			sceneText := fallbackSceneText(scene.Text, i)
			sceneName := fmt.Sprintf("scene-%d", i)
			if scene.ID != "" {
				sceneName = scene.ID
			}

			query := scene.Title
			if query == "" {
				query = sceneText
			}
			if query == "" {
				query = plan.Topic
			}
			if query == "" {
				query = plan.Title
			}

			out := imageSceneOutcome{
				image: SceneImage{
					Index: i,
					Text:  sceneText,
				},
			}
			defer func() {
				if r := recover(); r != nil {
					out.warning = fmt.Sprintf("image generation failed for scene %d: panic", i)
					outcomes[i] = out
				}
			}()

			var (
				asset *domainasset.ImageAsset
				err   error
			)
			if smartGen, ok := any(gen).(smartImageGenService); ok {
				// Prefer the AI-generated image path so the scene
				// binding ends up with the Drive-backed asset link.
				asset, err = smartGen.GenerateSmartImage(
					ctx,
					sceneName,
					query,
					plan.Style,
					[]string{sceneText, query},
					[]string{sceneName, plan.ID},
					1024,
					1024,
					plan.Model,
					false,
				)
				if err != nil || asset == nil {
					var fallback *ImageResult
					fallback, err = gen.SearchAndDownload(ctx, sceneName, sceneText, query, language)
					if err == nil && fallback != nil {
						asset = &domainasset.ImageAsset{SourceURL: fallback.SourceURL, DriveFileID: fallback.DriveFileID}
					} else {
						asset = nil
					}
				}
			} else {
				var fallback *ImageResult
				fallback, err = gen.SearchAndDownload(ctx, sceneName, sceneText, query, language)
				if err == nil && fallback != nil {
					asset = &domainasset.ImageAsset{SourceURL: fallback.SourceURL, DriveFileID: fallback.DriveFileID}
				}
			}
			if err != nil {
				out.warning = fmt.Sprintf("image generation failed for scene %d: %v", i, err)
				outcomes[i] = out
				return
			}
			if asset != nil {
				out.image.URL = canonicalSceneImageURL(asset)
				out.image.DriveFileID = asset.DriveFileID
			}
			outcomes[i] = out
		}()
	}

	wg.Wait()
	return outcomes
}

func fallbackSceneText(sceneText string, i int) string {
	if sceneText != "" {
		return sceneText
	}
	return fmt.Sprintf("Scene %d", i+1)
}

func canonicalSceneImageURL(asset *domainasset.ImageAsset) string {
	if asset == nil {
		return ""
	}
	url := strings.TrimSpace(asset.SourceURL)
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return url
	}
	if asset.DriveFileID != "" {
		return fmt.Sprintf("https://drive.google.com/file/d/%s/view", asset.DriveFileID)
	}
	return url
}
