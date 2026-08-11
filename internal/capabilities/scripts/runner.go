// Package scriptgeneration — runner.go implements the durable
// stage-based execution of the script generation workflow. Each
// stage is executed in order, with checkpoint updates after every
// successful stage. A retry resumes from the last failed stage.
//
// Verdetto contract:
//
//	ScriptGenerationRunner
//	  ├─ Normalize
//	  ├─ GenerateSceneText
//	  ├─ TranslateScenes
//	  ├─ GenerateVoiceovers
//	  ├─ UpsertDocuments
//	  ├─ BuildRenderPayload
//	  └─ EnqueueRender
//
// The runner is NOT an abstract phase registry or plugin system.
// It is a single struct with a linear, readable Execute method.
// Resume-from-checkpoint: on retry, Execute reads the run from
// the repo and skips stages that are already checkpointed.
package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"time"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"go.uber.org/zap"
)

// Runner executes the durable script generation stages.
// Each stage is checkpointed so a retry resumes from the last
// failed stage.
type Runner struct {
	repo                  RunRepository
	textGen               TextGenerator
	translator            Translator
	voiceoverGen          VoiceoverGenerator
	docPublisher          DocumentPublisher
	renderEnqueuer        RenderEnqueuer
	combinedAudioRenderer CombinedAudioRenderer
	log                   *zap.Logger
}

// NewRunner constructs the Runner with all required ports.
func NewRunner(
	repo RunRepository,
	textGen TextGenerator,
	translator Translator,
	voiceoverGen VoiceoverGenerator,
	docPublisher DocumentPublisher,
	renderEnqueuer RenderEnqueuer,
) *Runner {
	if repo == nil {
		panic("scriptgeneration: RunRepository is required for Runner")
	}
	return &Runner{
		repo:           repo,
		textGen:        textGen,
		translator:     translator,
		voiceoverGen:   voiceoverGen,
		docPublisher:   docPublisher,
		renderEnqueuer: renderEnqueuer,
		log:            zap.NewNop(),
	}
}

// SetLogger sets the runner's logger. Nil-safe (no-op on nil).
func (r *Runner) SetLogger(log *zap.Logger) {
	if log != nil {
		r.log = log
	}
}

func (r *Runner) SetCombinedAudioRenderer(renderer CombinedAudioRenderer) {
	r.combinedAudioRenderer = renderer
}

// Execute runs the complete generation workflow for the given run.
// It reads the run from the repository and resumes from the last
// checkpointed stage, skipping already-completed stages.
//
// Resume flow:
//  1. Read GenerationRun from repo (or use provided req for new runs)
//  2. Determine resume stage via ResumeFrom()
//  3. Skip stages before the resume stage
//  4. Execute from resume stage onward with checkpoint after each
//  5. On failure: persist error, return
//  6. On completion: persist final result, mark COMPLETED
//
// The handler must NOT wait for Execute to complete. This method
// is intended to be launched as a goroutine.
func (r *Runner) Execute(ctx context.Context, runID string, req GenerateRequest) {
	r.log.Info("scriptgeneration: starting execution",
		zap.String("run_id", runID),
		zap.String("source_type", string(req.Source.Type)),
	)

	// Determine resume stage from existing run (if any).
	run, err := r.repo.Get(ctx, runID)
	resumeIdx := -1 // -1 means start from beginning
	if err == nil && run != nil {
		resumeStage := ResumeFrom(run)
		if resumeStage == StageCompleted {
			r.log.Info("run already completed", zap.String("run_id", runID))
			return
		}
		resumeIdx = StageIndex(resumeStage)
		r.log.Info("resuming from checkpoint",
			zap.String("run_id", runID),
			zap.String("resume_stage", string(resumeStage)),
			zap.Int("attempt", run.AttemptCount+1),
		)
	} else {
		// New run — set RUNNING.
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageNormalizing); err != nil {
			r.failRunWithRetry(ctx, runID, StageNormalizing, err)
			return
		}
	}

	// Helper: skipIfCompleted returns true when the stage is before
	// the resume index (already completed in a previous attempt).
	skipIfCompleted := func(stage Stage) bool {
		return resumeIdx >= 0 && StageIndex(stage) < resumeIdx
	}

	// ── Stage 1: Normalize ──────────────────────────────────────
	if skipIfCompleted(StageNormalizing) {
		r.log.Info("skipping completed stage", zap.String("stage", string(StageNormalizing)))
	} else {
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageNormalizing)))
	}

	// ── Stage 2: Generate Scene Text ─────────────────────────────
	var result *GenerateResult
	if !skipIfCompleted(StageGeneratingSceneText) {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageGeneratingSceneText); err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, err)
			return
		}
		scenes, err := r.textGen.GenerateSceneText(ctx, req)
		if err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, fmt.Errorf("generate scene text failed: %w", err))
			return
		}
		if len(scenes) == 0 {
			r.failRunWithRetry(ctx, runID, StageGeneratingSceneText, fmt.Errorf("generate scene text returned zero scenes"))
			return
		}
		result = &GenerateResult{Scenes: scenes, Title: req.Title, OutputName: req.OutputName}
		r.checkpoint(ctx, runID, result)
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingSceneText)))
	} else {
		r.log.Info("skipping completed stage", zap.String("stage", string(StageGeneratingSceneText)))
		// Load result from repo if available.
		if run != nil && run.Result != nil {
			result = run.Result
		}
	}

	// Nil guard: result must be non-nil before downstream stages.
	if result == nil {
		result = &GenerateResult{Scenes: []Scene{}, Title: req.Title, OutputName: req.OutputName}
	}

	// ── Stage 3: Translate Scenes (scene-level idempotent) ───────
	// On retry, scenes that already have translated text for a target
	// language are skipped. The checkpoint after each scene ensures
	// partial progress is preserved.
	if !skipIfCompleted(StageTranslatingScenes) && len(req.Languages) > 0 {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageTranslatingScenes); err != nil {
			r.failRunWithRetry(ctx, runID, StageTranslatingScenes, err)
			return
		}
		for _, lang := range req.Languages {
			if lang == req.SourceLanguage {
				continue
			}
			for i := range result.Scenes {
				// Scene-level idempotency: skip if already translated.
				if result.Scenes[i].Text[lang] != "" {
					r.log.Debug("skipping already translated scene",
						zap.String("scene_id", result.Scenes[i].ID),
						zap.String("language", string(lang)),
					)
					continue
				}
				sourceText := result.Scenes[i].Text[req.SourceLanguage]
				if sourceText == "" {
					continue
				}
				translated, err := r.translator.Translate(ctx, TranslationInput{
					SceneID:        result.Scenes[i].ID,
					SourceLanguage: req.SourceLanguage,
					TargetLanguage: lang,
					SourceText:     sourceText,
				})
				if err != nil {
					r.failRunWithRetry(ctx, runID, StageTranslatingScenes,
						fmt.Errorf("translate scene %s to %s failed: %w", result.Scenes[i].ID, lang, err))
					return
				}
				if result.Scenes[i].Text == nil {
					result.Scenes[i].Text = make(map[Language]string)
				}
				result.Scenes[i].Text[lang] = translated
				// Checkpoint after each translated scene preserves progress.
				r.checkpoint(ctx, runID, result)
			}
		}
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageTranslatingScenes)))
	}

	// ── Stage 4: Generate Voiceovers (scene-level idempotent) ───
	// On retry, scenes that already have a voiceover for a language
	// are skipped. The Upsert-style DocumentPublisher ensures docs
	// are not duplicated either.
	if !skipIfCompleted(StageGeneratingVoiceovers) && r.voiceoverGen != nil {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageGeneratingVoiceovers); err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
			return
		}
		ttsStarted := time.Now()
		ttsCalls := 0
		if result.AudioMetrics == nil {
			result.AudioMetrics = &AudioPipelineMetrics{}
		}
		for i := range result.Scenes {
			scene := &result.Scenes[i]
			for lang, text := range scene.Text {
				if text == "" {
					continue
				}
				// Scene-level idempotency: skip if voiceover already exists.
				if existing, ok := scene.Voiceover[lang]; ok && existing.ID != "" {
					r.log.Debug("skipping already generated voiceover",
						zap.String("scene_id", scene.ID),
						zap.String("language", string(lang)),
					)
					continue
				}
				sceneTTSStarted := time.Now()
				audioRef, err := r.voiceoverGen.Generate(ctx, VoiceoverInput{
					SceneID:  scene.ID,
					Language: lang,
					Text:     text,
				})
				if err != nil {
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers,
						fmt.Errorf("voiceover generation for scene %s lang %s failed: %w", scene.ID, lang, err))
					return
				}
				ttsCalls++
				result.AudioMetrics.TTSScenes = append(result.AudioMetrics.TTSScenes, TTSSSceneMetric{SceneID: scene.ID, Language: lang, DurationMS: time.Since(sceneTTSStarted).Milliseconds(), Characters: len([]rune(text)), Words: len(strings.Fields(text)), OutputDurationMS: time.Duration(audioRef.Duration * float64(time.Second)).Milliseconds()})
				if scene.Voiceover == nil {
					scene.Voiceover = make(map[Language]AudioReference)
				}
				scene.Voiceover[lang] = audioRef
			}
			r.checkpoint(ctx, runID, result)
		}
		result.AudioMetrics.TTSMS += time.Since(ttsStarted).Milliseconds()
		result.AudioMetrics.TTSCalls += ttsCalls
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingVoiceovers)))
	}

	// ── Stage 5: Publish Documents ──────────────────────────────
	// Verdetto: docs.enabled must be explicitly true. One document per
	// language is created (not one bilingual doc). The identity is
	// deterministic (run_id + language) for idempotent Upsert.
	docsEnabled, docsLangs, docsFolderID := req.ResolveDocsConfig()

	if !skipIfCompleted(StagePublishingDocuments) && r.docPublisher != nil && docsEnabled && len(docsLangs) > 0 {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StagePublishingDocuments); err != nil {
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
			return
		}
		docs := make(map[Language]DocumentReference)
		for _, lang := range docsLangs {
			content := buildDocumentContent(result.Scenes, lang)
			title := req.Title
			if title == "" {
				title = "Script"
			}
			docRef, err := r.docPublisher.UpsertDocument(ctx, DocumentInput{
				RunID:    runID,
				Language: lang,
				Title:    title + "_" + string(lang),
				Content:  content,
				FolderID: docsFolderID,
			})
			if err != nil {
				r.failRunWithRetry(ctx, runID, StagePublishingDocuments,
					fmt.Errorf("upsert document for language %s failed: %w", lang, err))
				return
			}
			docs[lang] = docRef
		}
		result.Documents = docs
		r.checkpoint(ctx, runID, result)
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StagePublishingDocuments)))
	}

	// ── Stage 6: Build Render Payload ───────────────────────────
	if !skipIfCompleted(StageBuildingRenderPayload) && req.RenderVideo {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageBuildingRenderPayload); err != nil {
			r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
			return
		}
		// Audio mode is an explicit request-level choice. The presence of
		// generated scenes (or voiceover assets) is never a mode selector.
		mode, err := capabilityaudio.ResolveAudioMode(req.Audio, false, req.RenderVideo)
		if err != nil {
			r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
			return
		}
		result.AudioMode = mode
		if mode != capabilityaudio.AudioModeCombinedTimeline && result.FinalAudio != nil {
			r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, fmt.Errorf("%s must not carry final audio", mode))
			return
		}
		if mode == capabilityaudio.AudioModeCombinedTimeline {
			if r.combinedAudioRenderer == nil {
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, fmt.Errorf("COMBINED_TIMELINE requires a CombinedAudioRenderer"))
				return
			}
			started := time.Now()
			_, audioPlan, audioAssets, err := CompileCanonicalAudioPlan(*result, req.SourceLanguage, capabilityaudio.DefaultAudioProfile())
			if err != nil {
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, fmt.Errorf("compile canonical audio plan failed: %w", err))
				return
			}
			var finalAudio FinalAudioReference
			var metrics AudioPipelineMetrics
			if result.FinalAudio != nil && ValidateFinalAudioReference(*result.FinalAudio, audioPlan) == nil {
				// A checkpointed certified artifact is the idempotency boundary.
				// Do not invoke TTS/mix/encode again on a retry.
				finalAudio = *result.FinalAudio
				if result.AudioMetrics != nil {
					metrics = *result.AudioMetrics
				}
			} else {
				finalAudio, metrics, err = r.combinedAudioRenderer.Render(ctx, audioPlan, audioAssets)
				if err != nil {
					r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, fmt.Errorf("combined audio render failed: %w", err))
					return
				}
				if err := ValidateFinalAudioReference(finalAudio, audioPlan); err != nil {
					r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, fmt.Errorf("final audio certification failed: %w", err))
					return
				}
			}
			if result.AudioMetrics != nil {
				metrics.TTSMS += result.AudioMetrics.TTSMS
				metrics.TTSCalls += result.AudioMetrics.TTSCalls
				metrics.TTSScenes = append(metrics.TTSScenes, result.AudioMetrics.TTSScenes...)
			}
			metrics.TotalMS = time.Since(started).Milliseconds()
			if metrics.AudioDurationMS > 0 && metrics.TotalMS > 0 {
				metrics.AudioRTF = float64(metrics.TotalMS) / float64(metrics.AudioDurationMS)
				metrics.AudioSpeed = 1 / metrics.AudioRTF
			}
			result.FinalAudio = &finalAudio
			result.AudioStrategy = capabilityaudio.FinalAudioCopy
			result.AudioMetrics = &metrics
			r.checkpoint(ctx, runID, result)
		} else if mode == capabilityaudio.AudioModeChunkedVoiceover {
			if err := ValidateChunkedVoiceovers(*result); err != nil {
				r.failRunWithRetry(ctx, runID, StageBuildingRenderPayload, err)
				return
			}
			result.AudioStrategy = capabilityaudio.TimelineMix
		}
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageBuildingRenderPayload)), zap.String("audio_mode", string(mode)))
	}

	// ── Stage 7: Enqueue Render ─────────────────────────────────
	if !skipIfCompleted(StageEnqueuingRender) && req.RenderVideo && r.renderEnqueuer != nil {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageEnqueuingRender); err != nil {
			r.failRunWithRetry(ctx, runID, StageEnqueuingRender, err)
			return
		}
		renderRef, err := r.renderEnqueuer.Enqueue(ctx, *result)
		if err != nil {
			r.failRunWithRetry(ctx, runID, StageEnqueuingRender,
				fmt.Errorf("enqueue render failed: %w", err))
			return
		}
		renderRef.Status = "QUEUED"
		result.RenderJob = &renderRef
		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageEnqueuingRender)))
	}

	// ── Complete ────────────────────────────────────────────────
	r.completeRun(ctx, runID, result)
}

// deriveErrorCode extracts a stable machine-readable error code from
// the error chain and the failing stage. Returns a canonical string
// suitable for persisting as GenerationRun.ErrorCode.
//
// P0 verdetto: error codes must be stable so clients (retry bots,
// dashboards, monitoring) can branch on them reliably.
func deriveErrorCode(err error, stage Stage) string {
	if err == nil {
		return string(stage) + "_FAILED"
	}
	errStr := err.Error()

	// Check for known error patterns in the error message.
	// This is a lightweight heuristic; a future improvement could
	// use typed error interfaces (e.g. RetryableError, TransientError).
	switch {
	case containsAny(errStr, "timeout", "deadline exceeded", "context deadline"):
		return "PROVIDER_TIMEOUT"
	case containsAny(errStr, "unavailable", "not configured", "not initialized", "not found", "connection refused"):
		return "PROVIDER_UNAVAILABLE"
	case containsAny(errStr, "invalid response", "malformed", "decode failed", "parse error"):
		return "PROVIDER_BAD_RESPONSE"
	case containsAny(errStr, "empty", "zero", "no scenes", "no results"):
		return "EMPTY_RESULT"
	case containsAny(errStr, "generate scene text failed"):
		return "TEXT_GENERATION_FAILED"
	case containsAny(errStr, "translate"):
		return "TRANSLATION_FAILED"
	case containsAny(errStr, "voiceover"):
		return "VOICEOVER_FAILED"
	case containsAny(errStr, "document", "upsert", "google doc"):
		return "DOCUMENT_FAILED"
	case containsAny(errStr, "enqueue", "render", "worker"):
		return "ENQUEUE_FAILED"
	default:
		return string(stage) + "_FAILED"
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// buildDocumentContent assembles the content string for a Google Doc
// from the scenes in the given language.
func buildDocumentContent(scenes []Scene, lang Language) string {
	var content string
	for _, scene := range scenes {
		text, ok := scene.Text[lang]
		if !ok || text == "" {
			continue
		}
		if content != "" {
			content += "\n\n"
		}
		content += fmt.Sprintf("Scene %d\n%s", scene.Index+1, text)
	}
	return content
}

// ── Internal helpers ────────────────────────────────────────────────

// updateStage persists the stage transition.
func (r *Runner) updateStage(ctx context.Context, runID string, status RunStatus, stage Stage) error {
	return r.repo.UpdateStage(ctx, runID, status, stage)
}

// checkpoint saves the partial result to the repository.
// Errors are logged but not propagated (best-effort checkpoint).
func (r *Runner) checkpoint(ctx context.Context, runID string, result *GenerateResult) {
	if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
		r.log.Warn("checkpoint save failed",
			zap.String("run_id", runID),
			zap.Error(err),
		)
	}
}

// failRunWithRetry marks the run as FAILED and persists all failure
// metadata (error_code, failed_stage, attempt_count, next_retry_at)
// via the repository's FailRun method.
//
// P0 verdetto contract: every failure persists:
//   - error_code   — stable machine-readable code
//   - failed_stage — which stage failed
//   - attempt_count — incremented retry count
//   - next_retry_at — exponential backoff window (nil when exhausted)
func (r *Runner) failRunWithRetry(ctx context.Context, runID string, failedStage Stage, err error) {
	r.log.Error("scriptgeneration: stage failed",
		zap.String("run_id", runID),
		zap.String("failed_stage", string(failedStage)),
		zap.Error(err),
	)

	// Derive a stable error code from the error chain.
	errorCode := deriveErrorCode(err, failedStage)

	// Read current run to get attempt count.
	run, readErr := r.repo.Get(ctx, runID)
	attempt := 0
	if readErr == nil && run != nil {
		attempt = run.AttemptCount
	}

	// Compute the next retry attempt (1-based for display, 0-based for storage).
	nextAttempt := attempt + 1
	var nextRetryAt *time.Time
	if nextAttempt <= MaxRetries {
		delay := RetryDelay(attempt)
		now := time.Now().UTC()
		t := now.Add(delay)
		nextRetryAt = &t
		r.log.Info("retry scheduled",
			zap.String("run_id", runID),
			zap.Int("attempt", nextAttempt),
			zap.Duration("delay", delay),
			zap.Time("next_retry_at", t),
		)
	} else {
		r.log.Warn("max retries exhausted",
			zap.String("run_id", runID),
			zap.Int("attempts", attempt),
		)
	}

	// Persist all failure metadata atomically via FailRun.
	// AttemptCount is incremented to reflect this failed attempt.
	if failErr := r.repo.FailRun(ctx, FailRunInput{
		RunID:        runID,
		FailedStage:  failedStage,
		ErrorCode:    errorCode,
		ErrorMessage: err.Error(),
		AttemptCount: attempt + 1,
		NextRetryAt:  nextRetryAt,
	}); failErr != nil {
		r.log.Error("failed to persist run failure",
			zap.String("run_id", runID),
			zap.Error(failErr),
		)
	}
}

// completeRun marks the run as COMPLETED and saves the final result.
func (r *Runner) completeRun(ctx context.Context, runID string, result *GenerateResult) {
	r.log.Info("scriptgeneration: run completed",
		zap.String("run_id", runID),
		zap.Int("scene_count", len(result.Scenes)),
	)
	r.checkpoint(ctx, runID, result)
	if updateErr := r.repo.UpdateStage(ctx, runID, RunStatusCompleted, StageCompleted); updateErr != nil {
		r.log.Error("failed to persist run completion",
			zap.String("run_id", runID),
			zap.Error(updateErr),
		)
	}
}
