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
package processor

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// specScenesFromInput returns the canonical scene list from the
// adapters.ProcessInput envelope (typed MSOV1 output). Commit 6b (July 2026):
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
func specScenesFromInput(input adapters.ProcessInput) []scriptpkg.SpecScene {
	if len(input.SpecScene.Scenes) == 0 {
		return nil
	}
	return input.SpecScene.Scenes
}
