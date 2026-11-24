package scriptgeneration

// final_job_subtitles.go is the root adapter for the fixed-media subtitle
// workflow. The policy (which scene/clip/language needs a render, and with
// which caption) lives in scripts/runner/subtitles behind a narrow port; this
// file owns only the run-state effects: checkpointing, result mutation and the
// configured renderer.

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/runner/subtitles"
	"go.uber.org/zap"
)

// finalJobSubtitlePort is the narrow read side of the subtitles policy.
type finalJobSubtitlePort struct {
	result *GenerateResult
}

func (p finalJobSubtitlePort) FixedMediaNeedsRender(req contract.GenerateRequest, scene contract.Scene) bool {
	return fixedMediaNeedsLocalizedRender(req, scene)
}

func (p finalJobSubtitlePort) CaptionText(req contract.GenerateRequest, scene contract.Scene) string {
	return localizedRenderCaptionText(req, scene)
}

func (p finalJobSubtitlePort) CertifiedLocalRender(sceneID, clipID, language string) bool {
	_, ok := certifiedLocalizedRenderForClip(p.result, sceneID, clipID, language)
	return ok
}

// ensureFinalJobFixedMediaSubtitles renders the fixed-media fragments that the
// final job will use when subtitles are enabled. It is idempotent across
// retries: a clip already covered by a certified render is skipped, and a
// locally staged RENDERED artifact is published instead of rendered again.
//
// It must run before the remote PREPARE/FINALIZE pair. A failure is returned to
// the caller, which fails the run instead of submitting a plan that silently
// falls back to the unmodified library clip.
func (r *Runner) ensureFinalJobFixedMediaSubtitles(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) error {
	if r == nil || result == nil {
		return nil
	}
	jobs, err := subtitles.BuildPlan(req, subtitles.Source{
		Scenes:           result.Scenes,
		LocalizedRenders: result.LocalizedRenders,
		Subtitles:        req.Render.Subtitles,
	}, finalJobSubtitlePort{result: result})
	if err != nil {
		return fmt.Errorf("final_job subtitles: %w", err)
	}
	for _, job := range jobs {
		input := LocalizedRenderInput{
			RunID:          runID,
			JobID:          runID,
			SceneID:        job.Scene.ID,
			SceneIndex:     job.Scene.Index,
			Language:       job.Language,
			Text:           job.Text,
			SourceLanguage: req.SourceLanguage,
			SourceText:     job.SourceText,
			ClipID:         job.ClipID,
			ClipAssetID:    job.ClipAssetID,
			ClipSHA256:     job.ClipSHA256,
			ClipDurationMS: job.ClipDurationMS,
			Render:         sceneRenderSpec(req, job.Scene),
		}
		input.OnRendered = func(rendered LocalizedRenderResult) error {
			return r.recordFinalJobFixedMediaRender(ctx, runID, result, rendered)
		}
		// The in-memory envelope wins; the checkpoint is the fallback. Both
		// paths receive the v2 input fingerprint before publishing.
		if staged := r.stagedLocalizedRender(result, job.Scene.ID, job.Language, job.ClipID); staged != nil {
			input.ResumeFrom = staged
		}
		prepared, err := r.prepareLocalizedRenderInput(ctx, input, true)
		if err != nil {
			return fmt.Errorf("final_job subtitles: prepare resume for scene %q clip %q: %w", job.Scene.ID, job.ClipID, err)
		}
		input = prepared
		if input.ResumeFrom != nil {
			recovery, ok := r.localizedRenderEnqueuer.(LocalizedRenderRecoveryEnqueuer)
			if !ok {
				return fmt.Errorf("final_job subtitles: staged render for scene %q clip %q is not publishable (recovery enqueuer is not wired)", job.Scene.ID, job.ClipID)
			}
			if err := recovery.UploadRendered(ctx, input, *input.ResumeFrom); err != nil {
				return fmt.Errorf("final_job subtitles: publish staged render for scene %q clip %q: %w", job.Scene.ID, job.ClipID, err)
			}
		} else {
			if r.localizedRenderEnqueuer == nil {
				return fmt.Errorf("final_job subtitles: localized renderer is not wired for scene %q clip %q", job.Scene.ID, job.ClipID)
			}
			if err := r.localizedRenderEnqueuer.EnqueueLocalizedRender(ctx, input); err != nil {
				return fmt.Errorf("final_job subtitles: render scene %q clip %q language %q: %w", job.Scene.ID, job.ClipID, job.Language, err)
			}
		}
		if _, ok := certifiedLocalizedRenderForClip(result, job.Scene.ID, job.ClipID, string(job.Language)); !ok {
			return fmt.Errorf("final_job subtitles: scene %q clip %q language %q produced no certified render", job.Scene.ID, job.ClipID, job.Language)
		}
	}
	return nil
}

// recordFinalJobFixedMediaRender appends one certified fragment and checkpoints
// it immediately. The final-job handoff is often retried after a process
// restart, so a successfully rendered fragment must survive independently of
// the other fixed clips.
func (r *Runner) recordFinalJobFixedMediaRender(ctx context.Context, runID string, result *GenerateResult, rendered LocalizedRenderResult) error {
	if result == nil {
		return nil
	}
	r.localizedRenderMu.Lock()
	for _, existing := range result.LocalizedRenders {
		if existing.SceneID == rendered.SceneID && existing.ClipID == rendered.ClipID && existing.Language == rendered.Language {
			r.localizedRenderMu.Unlock()
			return nil
		}
	}
	result.LocalizedRenders = append(result.LocalizedRenders, rendered)
	accumulateLocalizedRenderMetrics(result, rendered)
	recordRenderStageProgress(result, rendered)
	snapshot := *result
	r.localizedRenderMu.Unlock()
	if err := r.saveLocalizedRenderArtifactCheckpoint(ctx, runID, 0, rendered); err != nil {
		return fmt.Errorf("final_job subtitles: checkpoint render for scene %q clip %q: %w", rendered.SceneID, rendered.ClipID, err)
	}
	if err := r.saveLocalizedRenderUnitEnvelope(ctx, runID, &snapshot); err != nil {
		return fmt.Errorf("final_job subtitles: checkpoint render for scene %q clip %q: %w", rendered.SceneID, rendered.ClipID, err)
	}
	if r.log != nil {
		r.log.Info("final_job subtitle fragment rendered",
			zap.String("run_id", runID),
			zap.String("scene_id", rendered.SceneID),
			zap.String("clip_id", rendered.ClipID),
			zap.String("language", string(rendered.Language)),
			zap.String("asset_id", rendered.AssetID))
	}
	return nil
}
