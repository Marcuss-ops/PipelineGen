package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func (r *Runner) projectVoiceoverMetrics(ctx context.Context, runID string, req contract.GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext, mode capabilityaudio.AudioMode, result *contract.GenerateResult, work []voiceoverWork, dbCacheHits int, renderWg *sync.WaitGroup, renderErrors chan error, voiceoverStep ExecutionStep) bool {
	// Project TTS provider calls only for work dispatched by this phase; the
	// streaming coordinator has already projected its own synthesis count.
	phaseCalls := int64(len(work) - dbCacheHits)
	if phaseCalls < 0 {
		phaseCalls = 0
	}
	var tts kernobs.OperationSummary
	if run := kernobs.FromContext(ctx); run != nil {
		tts = kernobs.SummarizeOperations(run.Report(), "voiceover", "synthesize")
	}
	result.AudioMetrics.TTSMS = tts.TotalMs
	result.AudioMetrics.TTSCalls += int(phaseCalls)
	if run := kernobs.FromContext(ctx); run != nil {
		kernobs.RecordOperation(ctx, kernobs.OperationInfo{
			Stage: kernobs.StageName(voiceoverStage), Component: kernobs.ComponentTTS,
			Operation: kernobs.OperationName("tts_publish_drain"), Items: int64(result.AudioMetrics.VoiceoverGenerated),
		}, 0)
	}
	// A reused voiceover is already complete TTS work, but its visual still
	// needs a localized render for this job. The normal render handoff happens
	// immediately after fresh TTS; without this branch, cache hits (or a
	// restored scene projection) silently bypass Chronon and completeRun later
	// fails with INCOMPLETE_RENDER_SET.
	dispatched := make(map[string]struct{}, len(work))
	for _, item := range work {
		dispatched[item.sceneID+"\x00"+string(item.lang)] = struct{}{}
	}
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		if scene.ExecutionMode.IsFixedMedia() || len(RenderUnitsForScene(*scene)) == 0 ||
			(req.MediaMode == kernelscript.MediaModeStockOnly && scene.Stock != nil) {
			continue
		}
		for _, lang := range renderLanguages(req, *scene) {
			if lang != req.SourceLanguage && (req.Render.Subtitles == nil || !req.Render.Subtitles.Enabled) {
				continue
			}
			if _, fresh := dispatched[scene.ID+"\x00"+string(lang)]; fresh {
				continue
			}
			audioRef, ok := scene.Voiceover[lang]
			if !ok || strings.TrimSpace(audioRef.ID) == "" {
				continue
			}
			for _, unit := range RenderUnitsForScene(*scene) {
				clipID, clipAssetID, clipSHA256, clipDurationMS := localizedRenderUnitClipFields(unit)
				if clipID == "" {
					continue
				}
				// ORCH-07: a unit already certified by another owner is not
				// this backfill's work. The streaming coordinator renders from
				// SceneTextReady and projects its videos into the result at the
				// script join, so by the time this loop runs the streamed scenes
				// are already produced; re-dispatching them pays Chronon twice
				// for one certified MP4 (and violated the exact render set:
				// successful > expected).
				if r.localizedRenderProduced(result, scene.ID, lang, clipID) {
					continue
				}
				text := strings.TrimSpace(scene.Text[lang])
				if text == "" {
					text = strings.TrimSpace(scene.Text[req.SourceLanguage])
				}
				sourceText := strings.TrimSpace(scene.Text[req.SourceLanguage])
				if sourceText == "" {
					sourceText = text
				}
				sceneIndex := scene.Index
				renderSpec := sceneRenderSpec(req, *scene)
				renderWg.Add(1)
				go func(sceneID string, lang contract.Language, text, sourceText string, audioRef contract.AudioReference, sceneIndex int, clipID, clipAssetID, clipSHA256 string, clipDurationMS int64, renderSpec kernelscript.VideoRenderSpec) {
					defer renderWg.Done()
					if err := r.enqueueLocalizedRender(ctx, LocalizedRenderInput{
						RunID: runID, ParentJobID: exec.JobID, JobID: exec.JobID, FinalJob: req.FinalJob,
						SceneID: sceneID, SceneIndex: sceneIndex, Language: lang, Text: text,
						Voiceover: audioRef, SourceLanguage: req.SourceLanguage, SourceText: sourceText,
						ClipID: clipID, ClipAssetID: clipAssetID, ClipSHA256: clipSHA256,
						ClipDurationMS: clipDurationMS, Render: renderSpec,
						ResumeFrom: r.stagedLocalizedRender(result, sceneID, lang, clipID),
						OnRenderReady: func(rendered contract.LocalizedRenderResult) error {
							return r.recordLocalizedRenderReady(ctx, runID, exec, result, rendered)
						},
						OnRendered: func(rendered contract.LocalizedRenderResult) error {
							return r.recordLocalizedRender(ctx, runID, exec, result, rendered)
						},
						OnFailed: func(failure contract.LocalizedRenderFailure) error {
							r.localizedRenderMu.Lock()
							result.LocalizedRenderFailures = append(result.LocalizedRenderFailures, failure)
							r.localizedRenderMu.Unlock()
							return nil
						},
					}); err != nil {
						renderErrors <- fmt.Errorf("localized render reused voiceover scene %s language %s failed: %w", sceneID, lang, err)
					}
				}(scene.ID, lang, text, sourceText, audioRef, sceneIndex, clipID, clipAssetID, clipSHA256, clipDurationMS, renderSpec)
			}
		}
	}

	// Fixed intro/outro sections have no TTS work item, so dispatch their
	// render matrix from this phase after caption translation is complete.
	for i := range result.Scenes {
		if !result.Scenes[i].ExecutionMode.IsFixedMedia() {
			continue
		}
		r.launchFixedMediaRenders(ctx, runID, req, routing, exec, result.Scenes[i], renderWg, renderErrors, fixedMediaRenderSink{
			OnRendered: func(rendered contract.LocalizedRenderResult) error {
				return r.recordLocalizedRender(ctx, runID, exec, result, rendered)
			},
			OnFailed: func(failure contract.LocalizedRenderFailure) error {
				r.localizedRenderMu.Lock()
				result.LocalizedRenderFailures = append(result.LocalizedRenderFailures, failure)
				r.localizedRenderMu.Unlock()
				return nil
			},
		})
	}

	// Join render callbacks before projecting links and making the final
	// durable snapshot. The scene/result graph has one writer after the join.
	renderDone := make(chan struct{})
	go func() {
		renderWg.Wait()
		close(renderErrors)
		close(renderDone)
	}()
	select {
	case <-renderDone:
		for renderErr := range renderErrors {
			classified := errors.Join(ErrLocalizedRenderFailure, renderErr)
			r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, classified)
			// Attribute the failure to the render fan-out, not to TTS. The
			// stage index deliberately aliases the voiceover index so a retry
			// still re-enters the phase that dispatches these renders.
			r.failRunWithRetry(ctx, runID, StageRenderingVideo, classified)
			return false
		}
		r.localizedRenderMu.Lock()
		for _, rendered := range result.LocalizedRenders {
			applyLocalizedRenderLinkLocked(result, rendered)
		}
		r.localizedRenderMu.Unlock()
		if snapshot, snapshotErr := detachCheckpointResult(result); snapshotErr != nil {
			r.log.Warn("voiceover final checkpoint snapshot failed", zap.String("run_id", runID), zap.Error(snapshotErr))
		} else if snapshot != nil {
			recordCheckpointFlush()
			r.checkpoint(ctx, runID, snapshot)
		}
	case <-ctx.Done():
		r.failExecutionStepBestEffort(ctx, exec, voiceoverStep, ctx.Err())
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, ctx.Err())
		return false
	}
	return true
}
