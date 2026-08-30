package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// voiceoverWork is one independent TTS work item: a (scene, language, text)
// triple whose final scene text is immutable. The TTS branch depends only on
// this text — never on entities/phrases/words — so it fans out from the
// SceneTextReady boundary in parallel with SceneAnalysis.
type voiceoverWork struct {
	scene   *Scene
	sceneID string
	lang    Language
	text    string
}

// voiceoverResult is the per-item outcome of one TTS synthesis call.
type voiceoverResult struct {
	audioRef AudioReference
	metric   TTSSSceneMetric
}

// buildVoiceoverWork flattens the scene×language grid into the ordered work
// items that need a fresh voiceover (empty text and already-generated scenes
// are skipped). Languages are ordered by dispatch priority — (scene_index,
// language_priority): source language first, then targets in caller order — so
// the fan-out order and the output-asset lineage ordinals are deterministic.
func buildVoiceoverWork(scenes []Scene, sourceLanguage Language, targetLanguages []Language) []voiceoverWork {
	work := make([]voiceoverWork, 0)
	for i := range scenes {
		scene := &scenes[i]
		for _, lang := range orderedSceneLanguages(scene.Text, sourceLanguage, targetLanguages) {
			text := scene.Text[lang]
			if text == "" {
				continue
			}
			if existing, ok := scene.Voiceover[lang]; ok && existing.ID != "" {
				continue
			}
			work = append(work, voiceoverWork{scene: scene, sceneID: scene.ID, lang: lang, text: text})
		}
	}
	return work
}

func (r *Runner) runVoiceoverPhase(ctx context.Context, runID string, req GenerateRequest, routing kernelscript.ArtifactRoutingContext, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	// ── Stage 4: Generate Voiceovers (scene-level idempotent) ───
	voiceoverStep, startErr := r.startExecutionStep(ctx, exec, "VOICEOVER", "audio")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, startErr)
		return false
	}
	// Voiceover generation is gated on the resolved audio mode: only
	// CHUNKED_VOICEOVER and COMBINED_TIMELINE produce voiceover assets.
	// audio.mode NONE (or omitted → NONE) must not trigger TTS nor
	// stage/publish voiceover artifacts — a timeline-only run stays
	// metadata-only and a plain script run pays no unrequested TTS cost.
	mode, modeErr := capabilityaudio.ResolveAudioMode(req.Audio, false)
	if modeErr != nil {
		// Envelope validation and the builder reject invalid audio-mode
		// combinations earlier; fail closed here for direct-runner callers.
		cause := fmt.Errorf("voiceover phase: resolve audio mode: %w", modeErr)
		r.failExecutionStep(ctx, exec, voiceoverStep, cause)
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
		return false
	}
	needsVoiceover := mode == capabilityaudio.AudioModeChunkedVoiceover || mode == capabilityaudio.AudioModeCombinedTimeline
	// godlike/07 NO-FAKE-AVAILABILITY: fail BEFORE the first TTS call when a
	// voiceover-producing mode is active but no Project was resolved. The
	// publisher already fail-closes on an empty Project
	// (ErrVoiceoverPublishProjectRequired); this gate moves that failure to
	// the start of the phase so no TTS work is wasted and no "scene"
	// namespace is silently invented.
	if needsVoiceover && routing.Project == "" {
		cause := fmt.Errorf("%w: voiceover publishing requires a resolved Project", ErrProjectRequired)
		r.failExecutionStep(ctx, exec, voiceoverStep, cause)
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
		return false
	}
	// On retry, scenes that already have a voiceover for a language
	// are skipped. The Upsert-style DocumentPublisher ensures docs
	// are not duplicated either.
	voiceoverSkipped := stageSkipped(resumeIdx, StageGeneratingVoiceovers) || r.voiceoverGen == nil || !needsVoiceover
	if !voiceoverSkipped {
		if err := r.updateStage(ctx, runID, RunStatusRunning, StageGeneratingVoiceovers); err != nil {
			r.failExecutionStep(ctx, exec, voiceoverStep, err)
			r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
			return false
		}
		if result.AudioMetrics == nil {
			result.AudioMetrics = &AudioPipelineMetrics{}
		}
		// A streaming SceneReady coordinator may have completed TTS before
		// this stage opened. Register those already-materialized assets here;
		// buildVoiceoverWork will correctly skip synthesis for them.
		existingOrdinal := 0
		for _, scene := range result.Scenes {
			for _, ref := range scene.Voiceover {
				if ref.ID == "" {
					continue
				}
				if err := r.attachOutputAsset(ctx, exec, voiceoverStep.StepID, ref.ID, existingOrdinal); err != nil {
					r.failExecutionStep(ctx, exec, voiceoverStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
					return false
				}
				existingOrdinal++
			}
		}
		// ── TTS worker pool ───────────────────────────────────────────
		// The voiceover branch fans out scene×language synthesis through a
		// bounded worker pool (r.ttsConcurrency). TTS depends only on the
		// final scene text — never on entities/phrases/words — so it runs in
		// parallel with SceneAnalysis from the SceneTextReady boundary. Each
		// item is independent; results are applied in canonical order below.
		work := buildVoiceoverWork(result.Scenes, req.SourceLanguage, req.Languages)
		// Record the dispatch plan before fan-out. VoiceoverReused is
		// scene-projection reuse only (a reference already attached before
		// this phase); DB fingerprint hits are counted per item AFTER
		// dispatch and reported in VoiceoverDBCacheHits.
		requested, reused := 0, 0
		for _, scene := range result.Scenes {
			for _, lang := range orderedSceneLanguages(scene.Text, req.SourceLanguage, req.Languages) {
				if strings.TrimSpace(scene.Text[lang]) == "" {
					continue
				}
				requested++
				if ref, ok := scene.Voiceover[lang]; ok && ref.ID != "" {
					reused++
				}
			}
		}
		result.AudioMetrics.VoiceoverRequested = requested
		result.AudioMetrics.VoiceoverReused = reused
		result.AudioMetrics.VoiceoverGenerated = len(work)
		r.log.Info("voiceover dispatch audit",
			zap.String("run_id", runID),
			zap.Int("requested", requested),
			zap.Int("scene_projection_reused", reused),
			zap.Int("generated", len(work)),
		)
		var dbCacheHits int
		if len(work) > 0 {
			// applyMu serializes per-unit result mutation + checkpoint so a
			// crash mid-phase (kill -9) preserves already-completed scenes.
			var applyMu sync.Mutex
			var ttsStartedOnce, renderStartedOnce sync.Once
			results, err := concurrent.Map(ctx, work, r.ttsConcurrency, func(opCtx context.Context, idx int, item voiceoverWork) (voiceoverResult, error) {
				// ── Pipeline KPI: first TTS dispatch ───────────────
				ttsStartedOnce.Do(func() {
					if run := kernobs.FromContext(opCtx); run != nil {
						kernobs.RecordKPIMilestone(opCtx, "tts_first_started_ms", run.ElapsedMs())
					}
				})
				var audioRef AudioReference
				ttsErr := kernobs.MeasureOperation(opCtx, kernobs.OperationInfo{
					Stage: "voiceover", Component: kernobs.ComponentTTS, Operation: kernobs.OperationSynthesize,
					Provider: string(item.lang), MetadataJSON: fmt.Sprintf("{\"scene_id\":%q,\"language\":%q}", item.sceneID, item.lang),
				}, func(measureCtx context.Context) error {
					var err error
					audioRef, err = r.voiceoverGen.Generate(measureCtx, VoiceoverInput{
						SceneID:  item.sceneID,
						Language: item.lang,
						Text:     item.text,
						// Project is the canonical semantic project namespace
						// resolved ONCE by resolveArtifactRoutingContext at
						// generation start and propagated verbatim to the
						// per-item pipeline so the voiceover publish satisfies
						// the semantic publish contract
						// (PR-VOICEOVER-DRIVE-DRIFT: Project is required). It is
						// guaranteed non-empty here by the phase-level fail-fast
						// gate above.
						Project: routing.Project,
						// VoiceoverFolderID is the caller-explicit Drive folder for
						// voiceover artifacts, resolved ONCE by
						// resolveArtifactRoutingContext (output.voiceover_folder_id;
						// empty falls back to the configured default). Forwarded
						// verbatim so the per-scene TTS command never replaces a
						// caller-explicit destination with the default folder.
						VoiceoverFolderID: routing.VoiceoverFolderID,
						// Forward the request-level timing policy so the per-item
						// pipeline can honour required/best-effort fail-closed
						// semantics (missing/invalid timing fails the job instead of
						// producing plausible-but-wrong timestamps).
						Timing: req.Timing,
					})
					return err
				})
				if ttsErr != nil {
					return voiceoverResult{}, fmt.Errorf("scene %s lang %s: %w", item.sceneID, item.lang, ttsErr)
				}
				metric := TTSSSceneMetric{
					SceneID:          item.sceneID,
					Language:         item.lang,
					DurationMS:       0,
					Characters:       len([]rune(item.text)),
					Words:            len(strings.Fields(item.text)),
					OutputDurationMS: time.Duration(audioRef.Duration * float64(time.Second)).Milliseconds(),
				}
				// Apply + checkpoint per unit (guarded): the completed voiceover
				// is durable before the worker returns, so a crash mid-phase
				// preserves it and the restart REUSEs it.
				applyMu.Lock()
				if item.scene.Voiceover == nil {
					item.scene.Voiceover = make(map[Language]AudioReference)
				}
				item.scene.Voiceover[item.lang] = audioRef
				if (mode == capabilityaudio.AudioModeCombinedTimeline || item.scene.Clip == nil) && audioRef.Duration > 0 {
					item.scene.DurationMS = int64(audioRef.Duration*1000 + 0.5)
					item.scene.DurationUS = int64(audioRef.Duration*1_000_000 + 0.5)
				}
				// Snapshot the render facts under the lock: the Voiceover map
				// is shared across a scene's language workers, so its reads
				// must be fenced by applyMu.
				renderText := item.scene.Text[item.lang]
				if strings.TrimSpace(renderText) == "" {
					renderText = item.scene.Text[req.SourceLanguage]
				}
				sourceText := item.scene.Text[req.SourceLanguage]
				if strings.TrimSpace(sourceText) == "" {
					sourceText = renderText
				}
				if strings.TrimSpace(sourceText) == "" {
					sourceText = req.Source.SourceText
					renderText = sourceText
				}
				r.checkpoint(ctx, runID, result)
				applyMu.Unlock()

				// Localized render fan-out: fire the render in a separate
				// goroutine the moment this language's TTS is final, so the
				// TTS worker slot is freed immediately instead of being held
				// for the entire render duration. The renderGate inside the
				// adapter already bounds render concurrency; OnRendered /
				// OnFailed capture the certified result asynchronously.
				clipID, clipAssetID, clipSHA256, clipDurationMS := localizedRenderClipFields(*item.scene)
				// ── Pipeline KPI: first render enqueue ───────────
				renderStartedOnce.Do(func() {
					if run := kernobs.FromContext(ctx); run != nil {
						kernobs.RecordKPIMilestone(ctx, "render_first_started_ms", run.ElapsedMs())
					}
				})
				go func(item voiceoverWork, audioRef AudioReference) {
					if err := r.enqueueLocalizedRender(ctx, LocalizedRenderInput{
						RunID:          runID,
						ParentJobID:    exec.JobID,
						SceneID:        item.sceneID,
						SceneIndex:     item.scene.Index,
						Language:       item.lang,
						Text:           renderText,
						Voiceover:      audioRef,
						SourceLanguage: req.SourceLanguage,
						SourceText:     sourceText,
						ClipID:         clipID,
						ClipAssetID:    clipAssetID,
						ClipSHA256:     clipSHA256,
						ClipDurationMS: clipDurationMS,
						Render:         req.Render,
						OnRendered: func(rendered LocalizedRenderResult) error {
							return r.recordLocalizedRender(ctx, exec, result, rendered)
						},
						OnFailed: func(failure LocalizedRenderFailure) error {
							r.localizedRenderMu.Lock()
							result.LocalizedRenderFailures = append(result.LocalizedRenderFailures, failure)
							r.localizedRenderMu.Unlock()
							return nil
						},
					}); err != nil {
						r.log.Warn("async localized render enqueue failed",
							zap.String("scene_id", item.sceneID),
							zap.String("language", string(item.lang)),
							zap.String("clip_id", clipID),
							zap.Error(err))
					}
				}(item, audioRef)
				return voiceoverResult{audioRef: audioRef, metric: metric}, nil
			})
			if err != nil {
				cause := fmt.Errorf("voiceover generation failed: %w", err)
				r.failExecutionStep(ctx, exec, voiceoverStep, cause)
				r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
				return false
			}
			// ── Cross-run DB cache accounting ─────────────────────────
			// Each result carries the Cached flag surfaced by the per-item
			// pipeline: true means the SQLite fingerprint lookup hit and the
			// entire TTS + upload + finalize work was skipped for the item.
			dbCacheHits = 0
			for i := range results {
				if results[i].audioRef.Cached {
					dbCacheHits++
				}
			}
			result.AudioMetrics.VoiceoverDBCacheHits = dbCacheHits
			r.log.Info("voiceover db cache audit",
				zap.String("run_id", runID),
				zap.Int("requested", requested),
				zap.Int("generated", len(work)),
				zap.Int("db_cache_hits", dbCacheHits),
				zap.Int("db_cache_misses", len(work)-dbCacheHits),
				zap.String("db_cache_status", "consulted"),
			)
			// Voiceover references and narration-driven durations were applied
			// per unit inside the workers (durable per-unit checkpoint). This
			// loop only projects the canonical-order observability: per-scene
			// TTS metrics and output-asset lineage ordinals stay deterministic.
			for i := range work {
				res := results[i]
				result.AudioMetrics.TTSScenes = append(result.AudioMetrics.TTSScenes, res.metric)
				if err := r.attachOutputAsset(ctx, exec, voiceoverStep.StepID, res.audioRef.ID, i); err != nil {
					r.failExecutionStep(ctx, exec, voiceoverStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
					return false
				}
				// Per-(scene, language) TTS correlation: record the produced
				// voiceover asset so translation → TTS → render → Drive is
				// joinable on (scene_id, language, asset_id).
				if err := r.recordArtifactOperation(ctx, exec, ArtifactOperation{
					OperationID: artifactOperationID(exec.Attempt, OperationTTS, work[i].sceneID, string(work[i].lang)),
					Kind:        OperationTTS,
					SceneID:     work[i].sceneID,
					Language:    work[i].lang,
					AssetID:     res.audioRef.ID,
					Status:      "COMPLETED",
				}); err != nil {
					r.failExecutionStep(ctx, exec, voiceoverStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
					return false
				}
			}
		}
		// Project the TTS count from the canonical voiceover operations —
		// never a local timer. Cache-hit acquisitions still emit a synthesize
		// observation (the MeasureOperation wraps the whole Generate call),
		// but they never reach the TTS provider: subtract them so TTSCalls
		// counts real provider synthesis calls. Without a bound Run
		// (test / dry-run) the fresh-work count is the fallback; the
		// authoritative wall time stays zero.
		var tts kernobs.OperationSummary
		if run := kernobs.FromContext(ctx); run != nil {
			tts = kernobs.SummarizeOperations(run.Report(), "voiceover", "synthesize")
			if fresh := tts.Calls - int64(dbCacheHits); fresh > 0 {
				tts.Calls = fresh
			} else {
				tts.Calls = int64(len(work) - dbCacheHits)
			}
		} else {
			tts.Calls = int64(len(work) - dbCacheHits)
		}
		result.AudioMetrics.TTSMS = tts.TotalMs
		result.AudioMetrics.TTSCalls = int(tts.Calls)
		if run := kernobs.FromContext(ctx); run != nil {
			kernobs.RecordOperation(ctx, kernobs.OperationInfo{
				Stage: kernobs.StageName(voiceoverStage), Component: kernobs.ComponentTTS,
				Operation: kernobs.OperationName("tts_publish_drain"),
				Items:     int64(result.AudioMetrics.VoiceoverGenerated),
			}, 0)
		}

		// P0.4: drain the async voiceover publish pool. All TTS synthesis
		// goroutines have returned, but Drive uploads + timing publishes +
		// SQLite commits may still be in-flight. Waiting here ensures
		// DriveFileID/DriveLink/TimingBundle are hydrated on the DB before
		// audio compile and docs stages read the results.
		if r.voiceoverPublishDrainer != nil {
			publishDrainStarted := time.Now()
			r.voiceoverPublishDrainer.Wait()
			kernobs.RecordStage(ctx, kernobs.StageInfo{Stage: "publish_pool_drain", ItemsInput: int64(result.AudioMetrics.VoiceoverGenerated)}, publishDrainStarted, time.Now(), nil)
			r.log.Info("voiceover publish pool drained",
				zap.String("run_id", runID),
				zap.Int("generated", result.AudioMetrics.VoiceoverGenerated))
		}

		r.log.Info("stage complete", zap.String("run_id", runID), zap.String("stage", string(StageGeneratingVoiceovers)))
	}
	if voiceoverSkipped {
		if err := r.skipExecutionStep(ctx, exec, voiceoverStep); err != nil {
			r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
			return false
		}
	} else if err := r.completeExecutionStep(ctx, exec, voiceoverStep); err != nil {
		r.failExecutionStep(ctx, exec, voiceoverStep, err)
		r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
		return false
	}

	return true
}
