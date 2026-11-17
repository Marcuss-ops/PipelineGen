// Package adapters — processor_images_contracts.go (commit 6, July 2026):
// typed contracts + default thresholds for scene-image generation.
//
// godlike/06 SSOT — file ownership after the commit 6 split:
//
//	processor_images.go              — owns ONLY ImageProcessor type +
//	                                  NewImageProcessor + Name + Policy +
//	                                  Process + specScenesFromInput
//	processor_images_contracts.go    — owns ImageResult + ImageGenService +
//	                                  imagePrewarmer + smartImageGenService +
//	                                  imageSceneOutcome (internal buffer) +
//	                                  SceneImageOutcome (NEW exported) +
//	                                  SceneImageStatus + 3 status constants +
//	                                  defaultImageWidth/Height/SkipDrive
//	processor_images_fanout.go       — owns defaultImageSceneConcurrency +
//	                                  imageFanoutConcurrency +
//	                                  runImageSceneFanout (goroutine +
//	                                  semaphore dispatch)
//	processor_images_scene.go        — owns resolveSceneQuery +
//	                                  generateSceneImage + fallbackSceneText +
//	                                  cleanPromptForSubject +
//	                                  canonicalSceneImageURL
//
// godlike/07 honored: the 1024×1024 + skipDrive=false literals that
// used to be inline in runImageSceneFanout's smartGen.GenerateSmartImage
// call are extracted to named constants here. Single canonical owner
// per threshold (godlike/06 SSOT). Concurrency threshold (4)
// stays in fanout.go since it's intrinsic to the dispatch surface,
// not the smart-image contract.
//
// Niente duplicazione soglie (Commit 6 invariant): the literals
// 1024 / 1024 / false used in the smart-vs-fallback image-call
// appear once each here. Python measures; Go decides — but Go
// decides exactly ONCE per threshold.
package processor

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
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
// Production implementations live in internal/capabilities/images/
// (concrete *images.Service); stub implementations live in adapters/.
type ImageGenService interface {
	SearchAndDownload(ctx context.Context, sceneName, sceneText, altText, language string) (*ImageResult, error)
}

// ── NEW typed outcome (Commit 6) ─────────────────────────────────────────

// SceneImageStatus is the per-scene terminal-state enum surfaced
// from the fanout. Commit 6 introduction — written for external
// consumers (operators, dashboards, post-processor-facing reports)
// who need typed status semantics instead of the lowercase-buffer's
// free-form warning string.
type SceneImageStatus string

// SceneImageStatus constants — committed values for typed status
// comparisons (operators can branch on Status == SceneImageSucceeded
// without string-comparing magic strings).
const (
	SceneImageSucceeded SceneImageStatus = "succeeded"
	SceneImageFailed    SceneImageStatus = "failed"
	SceneImageSkipped   SceneImageStatus = "skipped"
)

// SceneImageOutcome is the EXPORTED typed outcome for one scene's
// image-generation terminal state. Commit 6 introduction — a typed
// surface that downstream consumers (operator dashboards, the
// aggregator) can read directly without unmarshaling warning strings.
//
// Fields:
//
//	SceneIndex — 0-based scene index matching the SpecScene order.
//	Status     — SceneImageSucceeded/Failed/Skipped typed enum.
//	Image      — the generated image (URL + DriveFileID when status
//	             is Succeeded; zero-valued adapters.SceneImage otherwise).
//	ErrorCode  — machine-readable error code (only set when Status
//	             is Failed). Reserved for dashboard-side failure
//	             taxonomy; current implementations leave empty.
//	Error      — human-readable error detail (only set when Status
//	             is Failed); equivalent to the lowercase-buffer's
//	             `warning` field semantics.
//
// godlike/07 no-fake-availability: ErrorCode is intentionally
// empty for now. When the failure-taxonomy mapping completes
// (a future PR), the field will be populated from a registry of
// typed-sentinel error codes.
type SceneImageOutcome struct {
	SceneIndex int
	Status     SceneImageStatus
	Image      adapters.SceneImage
	ErrorCode  string
	Error      string
}
