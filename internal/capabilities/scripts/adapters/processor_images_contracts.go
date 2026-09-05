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
package adapters

import (
	"context"

	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

// defaultImageWidth is the canonical smart-image width (Commit 6
// SSOT — replaces inline literal `1024` previously repeated inside
// runImageSceneFanout's GenerateSmartImage call). Python measures;
// Go decides. 1024 chosen for parity with the SlideWorker pipeline's
// slide-image surface (also 1024×1024).
const defaultImageWidth = 1024

// defaultImageHeight mirrors defaultImageWidth — 1024×1024 is the
// canonical smart-image sizing for scene fan-out.
const defaultImageHeight = 1024

// defaultSkipDrive is the canonical "do NOT upload to Drive during
// fanout" flag. The async Drive upload cycle is handled by the
// finalizer pipeline (not the synchronous image processor) so the
// per-scene inline call into GenerateSmartImage MUST skip Drive.
const defaultSkipDrive = false

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

// imagePrewarmer is the optional warmup seam used by production image
// services that can pre-initialize their browser/session pool before
// the parallel scene fan-out starts.
type imagePrewarmer interface {
	TriggerPrewarm(ctx context.Context, jobID string, count int)
}

// smartImageGenService is the optional AI-image-generation seam.
// When ImageGenService implements it, the fanout prefers the
// Drive-backed AI path over the fallback SearchAndDownload.
type smartImageGenService interface {
	GenerateSmartImage(ctx context.Context, subject, topic, style string, prompts, tags []string, width, height int, model string, skipDrive bool) (*detail.ImageAsset, error)
}

// imageSceneOutcome is the INTERNAL goroutine-buffer outcome used by
// runImageSceneFanout. Single field `warning` collects error-detail
// strings; Process() iterates and maps to PostProcessResult.StageWarnings.
//
// Kept LOWERCASE for minimum surface to external callers. External
// consumers should read SceneImageOutcome (exported) for the
// status-typed API. godlike/07 fail-closed: the lowercase type
// stays scoped to the fanout goroutine + Process(); do not leak it
// outside the package.
type imageSceneOutcome struct {
	image   SceneImage
	warning string
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
// postprocessor pipeline's metrics adapter, the artlist search-
// aggregator) can read directly without unmarshaling warning strings.
//
// Fields:
//
//	SceneIndex — 0-based scene index matching the SpecScene order.
//	Status     — SceneImageSucceeded/Failed/Skipped typed enum.
//	Image      — the generated image (URL + DriveFileID when status
//	             is Succeeded; zero-valued SceneImage otherwise).
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
	Image      SceneImage
	ErrorCode  string
	Error      string
}
