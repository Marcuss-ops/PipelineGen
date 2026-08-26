// Package adapters — processor_images_fanout.go (commit 6, July 2026):
// parallel scene-image fan-out for processors/images.
//
// godlike/06 SSOT — file ownership after the commit 6 split:
//
//	processor_images_fanout.go       — owns this file's content:
//	                                  `defaultImageSceneConcurrency` +
//	                                  `imageFanoutConcurrency` +
//	                                  `runImageSceneFanout` (the
//	                                  goroutine + semaphore dispatch).
//
// Concurrency threshold (defaultImageSceneConcurrency = 4) lives
// here, NOT in contracts.go, because it's intrinsic to the dispatch
// surface (semaphore capacity), not the smart-image contract (which
// is the contracts.go concern). godlike/06 SSOT: one canonical owner
// per threshold.
//
// Commit 6 extraction summary (was inline in runImageSceneFanout's
// goroutine body pre-split):
//   - resolveSceneQuery(plan, scene, sceneText)    → processor_images_scene.go
//   - generateSceneImage(ctx, gen, ...)            → processor_images_scene.go
//
// godlike/07 byte-equivalence: the inline defer-recover pattern
// that protects against goroutine panics is preserved VERBATIM
// here (NOT inside generateSceneImage) because the recovery is
// fan-out-orchestration concern, not a per-image-call concern.
// Extracting panic-recovery would change the buffer-allocation
// semantics from pre-split behavior.
package adapters

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"fmt"
	"sync"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// defaultImageSceneConcurrency caps the in-flight parallel image-
// generation goroutines. 4 chosen as the empirically-safe limit
// for slide-image generation (higher thrashes the browser pool;
// lower underutilizes the fan-out). Python measures (operator
// dashboard reports), Go decides (this constant).
const defaultImageSceneConcurrency = 4

// imageFanoutConcurrency returns the semaphore cap for the fan-out
// dispatcher. sceneCount < defaultImageSceneConcurrency returns
// sceneCount (1-by-1 is wasteful but correct); sceneCount >= 4
// returns 4 (the cap).
func imageFanoutConcurrency(sceneCount int) int {
	if sceneCount < 1 {
		return 1
	}
	if sceneCount < defaultImageSceneConcurrency {
		return sceneCount
	}
	return defaultImageSceneConcurrency
}

// runImageSceneFanout generates images for each scene in parallel
// (bounded by imageFanoutConcurrency). Partial failures are
// collected — the function does NOT abort on first error. Each
// goroutine writes its result to outcomes[i] (a pre-sized slice);
// a per-goroutine defer-recover keeps a panic from corrupting the
// slice (the recovered goroutine still writes a warning entry so
// sibling outcomes are preserved).
//
// The returned slice is the lowercase imageSceneOutcome buffer
// (Commit 6 spec — internal). Process() in processor_images.go
// iterates it; external callers wanting the typed semantic
// (SceneImageOutcome) should migrate to a future commit that
// wires the exported type into the fanout return signature.
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

			query := resolveSceneQuery(plan, scene, sceneText)

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

			cleanedSubject := cleanPromptForSubject(query)
			asset, err := generateSceneImage(ctx, gen, plan, sceneName, sceneText, query, cleanedSubject, language)
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
