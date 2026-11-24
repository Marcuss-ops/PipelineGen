package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// fixedMediaRenderSink receives the outcome of one fixed-media render unit.
// The two call sites collect into different owners (the streaming
// coordinator's own slices, the voiceover phase's GenerateResult), so the sink
// is injected instead of the fan-out owning a collector.
type fixedMediaRenderSink struct {
	OnRendered func(contract.LocalizedRenderResult) error
	OnFailed   func(contract.LocalizedRenderFailure) error
}

// launchFixedMediaRenders dispatches the localized render matrix of ONE fixed
// (intro/outro) scene: one render per bound clip per render language — the
// exact matrix expectedRenderUnits counts. This is the ONLY place that matrix
// is spawned, because a fixed section carries no voiceover work item and has
// therefore no TTS completion event that could trigger a render. A render-
// enabled path that dispatches only its voiceover work items finishes with
// successful < expected and is failed closed as INCOMPLETE_RENDER_SET
// (observed live on the voiceover path, 2026-09-22).
//
// enqueueErrs may be nil: a caller that already records every failure through
// the sink (the streaming coordinator) passes nil, while the voiceover phase
// forwards the enqueue error so the phase fails instead of reporting a
// partially rendered set.
func (r *Runner) launchFixedMediaRenders(
	ctx context.Context,
	runID string,
	req contract.GenerateRequest,
	routing kernelscript.ArtifactRoutingContext,
	exec ExecutionContext,
	scene contract.Scene,
	wg *sync.WaitGroup,
	enqueueErrs chan<- error,
	sink fixedMediaRenderSink,
) {
	if r == nil || wg == nil || !req.Render.Enabled {
		return
	}
	if !fixedMediaNeedsLocalizedRender(req, scene) {
		return
	}
	for _, lang := range fixedRenderLanguages(req, scene) {
		lang := lang
		text := fixedCaptionText(scene, req.SourceLanguage, lang)
		sourceText := strings.TrimSpace(scene.Text[req.SourceLanguage])
		for _, unit := range RenderUnitsForScene(scene) {
			unit := unit
			clipID, clipAssetID, clipSHA256, clipDurationMS := localizedRenderUnitClipFields(unit)
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := r.enqueueLocalizedRender(ctx, LocalizedRenderInput{
					RunID: runID, ParentJobID: exec.JobID, FinalJob: req.FinalJob,
					JobID:   exec.JobID,
					SceneID: scene.ID, SceneIndex: scene.Index,
					Language: lang, Text: text,
					SourceLanguage: req.SourceLanguage, SourceText: sourceText,
					ClipID: clipID, ClipAssetID: clipAssetID, ClipSHA256: clipSHA256,
					ClipDurationMS: clipDurationMS, Render: sceneRenderSpec(req, unit.Scene),
					OnRendered: sink.OnRendered,
					OnFailed:   sink.OnFailed,
				}); err != nil {
					// A fixed-media unit that cannot even be enqueued is a failed
					// unit, never a silently missing render: report it through the
					// same sink the renderer would have used, then let a caller
					// that owns a phase failure channel fail the run.
					r.log.Error("fixed-media localized render enqueue failed",
						zap.String("run_id", runID), zap.String("scene_id", scene.ID),
						zap.String("clip_id", clipID), zap.String("language", string(lang)),
						zap.Error(err))
					if sink.OnFailed != nil {
						_ = sink.OnFailed(contract.LocalizedRenderFailure{
							SceneID: scene.ID, Language: lang, ClipID: clipID,
							ErrorCode: "LOCALIZED_RENDER_ENQUEUE_FAILED", Error: err.Error(),
						})
					}
					if enqueueErrs != nil {
						enqueueErrs <- fmt.Errorf("fixed-media localized render scene %s clip %s language %s failed: %w", scene.ID, clipID, lang, err)
					}
				}
			}()
		}
	}
}

// ── Parent-visible render stage ─────────────────────────────────────
//
// The localized render fan-out is the ONLY producer of the `render` workflow
// stage (job.StageRender): no postprocessor renders video, so the batch lane's
// postprocessor map cannot carry it. These helpers are the writers.

// recordRenderStageProgress projects one certified localized render onto the
// parent-visible `render` stage of the run result. ONE unit is one (scene,
// clip) rendered in one language, so a scene fanned out across N languages
// reports N observations and a parent can count the real fan-out instead of an
// assumed one.
//
// The observation is an UPSERT keyed on (stage, language, unit): the ready and
// the published path both see the same render, so a second record converges on
// the same entry rather than inflating the counters.
