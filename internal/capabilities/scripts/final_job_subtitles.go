package scriptgeneration

// final_job_subtitles.go owns the last render step of the Producer lane:
// before a final job is handed to the remote Master, every protected fixed-media
// clip that must carry burned-in subtitles is rendered by the existing localized
// clip renderer. The Master then receives the certified fragment, never the
// unmodified library clip, and the final assembly stays on the remote worker.
//
// The regular localized-render fan-out remains sealed (see
// sceneRenderDispatchSealed); this is deliberately a final-job-only seam. It is
// triggered by the same subtitle policy the fixed-media fan-out used to apply
// (Render.Subtitles.Enabled plus a non-empty display caption), so a run that
// asks for subtitles cannot silently deliver an un-subtitled intro/outro.

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

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
	if req.Render.Subtitles == nil || !req.Render.Subtitles.Enabled {
		return nil
	}
	needsRender := false
	for i := range result.Scenes {
		scene := result.Scenes[i]
		if !fixedMediaNeedsLocalizedRender(req, scene) || localizedRenderCaptionText(req, scene) == "" {
			continue
		}
		if len(RenderUnitsForScene(scene)) > 0 {
			needsRender = true
			break
		}
	}
	if !needsRender {
		return nil
	}
	language := ""
	if existing, err := finalJobRenderLanguage(req, result); err != nil {
		return fmt.Errorf("final_job subtitles: resolve render language: %w", err)
	} else {
		language = existing
	}
	if language == "" {
		language = strings.TrimSpace(string(req.SourceLanguage))
	}
	if language == "" {
		for _, scene := range result.Scenes {
			if !fixedMediaNeedsLocalizedRender(req, scene) {
				continue
			}
			for lang := range scene.Text {
				if strings.TrimSpace(scene.Text[lang]) != "" {
					language = strings.TrimSpace(string(lang))
					break
				}
			}
			if language != "" {
				break
			}
		}
	}
	if language == "" {
		return nil
	}

	for i := range result.Scenes {
		scene := result.Scenes[i]
		if !fixedMediaNeedsLocalizedRender(req, scene) || localizedRenderCaptionText(req, scene) == "" {
			continue
		}
		for _, unit := range RenderUnitsForScene(scene) {
			if unit.Clip == nil || strings.TrimSpace(unit.Clip.ID) == "" {
				continue
			}
			if _, ok := certifiedLocalizedRenderForClip(result, scene.ID, unit.Clip.ID, language); ok {
				continue
			}
			clipID, clipAssetID, clipSHA256, clipDurationMS := localizedRenderUnitClipFields(unit)
			text := fixedCaptionText(scene, req.SourceLanguage, Language(language))
			input := LocalizedRenderInput{
				RunID:          runID,
				JobID:          runID,
				SceneID:        scene.ID,
				SceneIndex:     scene.Index,
				Language:       Language(language),
				Text:           text,
				SourceLanguage: req.SourceLanguage,
				SourceText:     strings.TrimSpace(scene.Text[req.SourceLanguage]),
				ClipID:         clipID,
				ClipAssetID:    clipAssetID,
				ClipSHA256:     clipSHA256,
				ClipDurationMS: clipDurationMS,
				Render:         sceneRenderSpec(req, scene),
			}
			input.OnRendered = func(rendered LocalizedRenderResult) error {
				return r.recordFinalJobFixedMediaRender(ctx, runID, result, rendered)
			}
			if staged := r.stagedLocalizedRender(result, scene.ID, Language(language), clipID); staged != nil {
				recovery, ok := r.localizedRenderEnqueuer.(LocalizedRenderRecoveryEnqueuer)
				if !ok {
					return fmt.Errorf("final_job subtitles: staged render for scene %q clip %q is not publishable (recovery enqueuer is not wired)", scene.ID, clipID)
				}
				input.ResumeFrom = staged
				if err := recovery.UploadRendered(ctx, input, *staged); err != nil {
					return fmt.Errorf("final_job subtitles: publish staged render for scene %q clip %q: %w", scene.ID, clipID, err)
				}
			} else {
				if r.localizedRenderEnqueuer == nil {
					return fmt.Errorf("final_job subtitles: localized renderer is not wired for scene %q clip %q", scene.ID, clipID)
				}
				if err := r.localizedRenderEnqueuer.EnqueueLocalizedRender(ctx, input); err != nil {
					return fmt.Errorf("final_job subtitles: render scene %q clip %q language %q: %w", scene.ID, clipID, language, err)
				}
			}
			if _, ok := certifiedLocalizedRenderForClip(result, scene.ID, clipID, language); !ok {
				return fmt.Errorf("final_job subtitles: scene %q clip %q language %q produced no certified render", scene.ID, clipID, language)
			}
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
	if r.repo != nil {
		if err := r.repo.SavePartialResult(ctx, runID, &snapshot); err != nil {
			return fmt.Errorf("final_job subtitles: checkpoint render for scene %q clip %q: %w", rendered.SceneID, rendered.ClipID, err)
		}
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
