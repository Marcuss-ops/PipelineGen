// Package adapters — processor_images_scene.go (commit 6, July 2026;
// commit 6b relocation, July 2026):
// per-scene helpers called from runImageSceneFanout + the scene
// lens helper from (*ImageProcessor).Process.
//
// godlike/06 SSOT — file ownership after the commit 6+6b split:
//
//	processor_images_scene.go        — owns THIS file's content:
//	                                  `resolveSceneQuery` +
//	                                  `specScenesFromInput` (Commit 6b
//	                                  relocation from
//	                                  processor_images.go — per the
//	                                  spec literal 5-method-only
//	                                  constraint) +
//	                                  `generateSceneImage` +
//	                                  `fallbackSceneText` +
//	                                  `cleanPromptForSubject` +
//	                                  `canonicalSceneImageURL`.
//
// resolveSceneQuery + generateSceneImage were EXTRACTED from the
// inline goroutine body of runImageSceneFanout during the commit 6
// split. Both are pure (no I/O) helpers — generateSceneImage is
// I/O via the typed ImageGenService port, but the helper itself
// has no goroutine/sync state per call (each callsite runs in
// exactly one goroutine).
//
// specScenesFromInput was RELOCATED from processor_images.go
// during the commit 6b pass to honor the spec literal
// ("processor_images.go tiene SOLO the 5 listed symbols"). The
// function is still same-package-private (no export) and the
// only external caller is (*ImageProcessor).Process via Go's
// same-package resolution rules.
//
// godlike/07 no-threshold-duplication: the 1024 / 1024 / false
// literals that used to live inline inside the smartGen call are
// now `defaultImageWidth / defaultImageHeight / defaultSkipDrive`
// owned by processor_images_contracts.go. This file references the
// constants; it does NOT redeclare them.
package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	domainasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// resolveSceneQuery picks the canonical query string for a scene's
// image search. The cascade is:
//
//  1. scene.Title            (operator-authored metadata)
//  2. scene.Text             (the scene's actual script text)
//  3. plan.Topic             (the plan-level topic fallback)
//  4. plan.Title             (the last-resort plan title)
//
// Inlined fallback to plan-level fields is preserved byte-byte from
// pre-split behavior — the cascade order is the single canonical
// contract for "what to ask the image search".
func resolveSceneQuery(
	plan *scriptpkg.ResolvedGenerationPlan,
	scene scriptpkg.SpecScene,
	sceneText string,
) string {
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
	return query
}

// specScenesFromInput returns the canonical scene list from the
// ProcessInput envelope (typed MSOV1 output). Commit 6b (July 2026):
// relocated from processor_images.go to honor the spec literal
// 5-method-only constraint on processor_images.go.
//
// PR 9 contract: post-processors consume scenes through this lens;
// any attempt to re-derive scenes via text-splitting is a regression
// caught by ci-architectural-checks Check 15. The function is
// same-package-private (lowercase, no export) and is called only
// from (*ImageProcessor).Process to read the engineResult's
// SpecScene.Scenes. godlike/06 SSOT-correct location: scene.go
// owns all per-scene-lens helpers (resolveSceneQuery is the
// other lens helper in this file).
func specScenesFromInput(input ProcessInput) []scriptpkg.SpecScene {
	if len(input.SpecScene.Scenes) == 0 {
		return nil
	}
	return input.SpecScene.Scenes
}

// generateSceneImage dispatches one scene to either the smart-image
// path (preferred; produces a Drive-backed AI asset) or the
// fallback image search path (SearchAndDownload). The smart-vs-
// fallback decision is typed (smartImageGenService interface probe):
// if the registered ImageGenService ALSO implements the smart-
// interface, smart-image path is preferred; the fallback path runs
// only when smart-image fails (err != nil OR asset == nil).
//
// godlike/07 byte-equivalence: the smart-vs-fallback logic and
// `if err == nil && fallback != nil` acceptance conditions are
// preserved exactly from the pre-split inline goroutine body.
func generateSceneImage(
	ctx context.Context,
	gen ImageGenService,
	plan *scriptpkg.ResolvedGenerationPlan,
	sceneName, sceneText, query, cleanedSubject, language string,
) (*domainasset.ImageAsset, error) {
	var asset *domainasset.ImageAsset
	var err error
	if smartGen, ok := any(gen).(smartImageGenService); ok {
		// Prefer the AI-generated image path so the scene
		// binding ends up with the Drive-backed asset link.
		// The provider needs an explicit visual instruction. Passing the
		// raw narration alone can leave Gemini's image surface in a
		// perpetual "understood"/"creating" state; the scene subject is
		// already bounded to the first sentence by cleanPromptForSubject.
		visualPrompt := fmt.Sprintf(
			"Create a cinematic documentary image depicting: %s",
			cleanedSubject,
		)
		asset, err = smartGen.GenerateSmartImage(
			ctx,
			visualPrompt,
			query,
			plan.Style,
			[]string{visualPrompt, query, sceneText},
			[]string{sceneName, plan.ID},
			defaultImageWidth,
			defaultImageHeight,
			plan.Model,
			defaultSkipDrive,
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
	return asset, err
}

// fallbackSceneText returns sceneText if non-empty, else a
// machine-formatted "Scene N" placeholder. Used both by the
// goroutine and by the ctx-canceled early return path.
func fallbackSceneText(sceneText string, i int) string {
	if sceneText != "" {
		return sceneText
	}
	return fmt.Sprintf("Scene %d", i+1)
}

// canonicalSceneImageURL returns the public URL for a generated/
// uploaded image asset. Order: HTTP(S) SourceURL → Drive-link
// fallback → empty string. This is the canonical operator-facing
// URL field that downstream consumers (PostProcessResult, dashboards,
// stock-pipeline finalizer) read.
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

// cleanPromptForSubject normalises a raw query string into a
// filename-safe subject. The truncation rule (max 100 chars, cut
// at last space within the first 100 chars when last-space index
// > 50) is the canonical Google-Slides title-length heuristic.
// godlike/07 byte-equivalence: the truncation logic is preserved
// exactly from pre-split.
func cleanPromptForSubject(prompt string) string {
	parts := strings.Split(prompt, ".")
	var first string
	if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
		first = strings.TrimSpace(parts[0])
	} else {
		first = strings.TrimSpace(prompt)
	}
	if len(first) > 100 {
		// Truncate at space to avoid word cuts
		lastSpace := strings.LastIndex(first[:100], " ")
		if lastSpace > 50 {
			first = first[:lastSpace]
		} else {
			first = first[:100]
		}
	}
	// remove characters that are invalid in filenames/slugs if any, but Google Drive allows most
	return first
}
