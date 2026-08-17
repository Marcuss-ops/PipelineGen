package scriptgeneration

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
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
// are skipped). Language keys are sorted so the fan-out order — and therefore
// the output-asset lineage ordinals — is deterministic.
func buildVoiceoverWork(scenes []Scene) []voiceoverWork {
	work := make([]voiceoverWork, 0)
	for i := range scenes {
		scene := &scenes[i]
		langs := make([]Language, 0, len(scene.Text))
		for lang := range scene.Text {
			langs = append(langs, lang)
		}
		sort.Slice(langs, func(a, b int) bool { return langs[a] < langs[b] })
		for _, lang := range langs {
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
		// ── TTS worker pool ───────────────────────────────────────────
		// The voiceover branch fans out scene×language synthesis through a
		// bounded worker pool (r.ttsConcurrency). TTS depends only on the
		// final scene text — never on entities/phrases/words — so it runs in
		// parallel with SceneAnalysis from the SceneTextReady boundary. Each
		// item is independent; results are applied in canonical order below.
		work := buildVoiceoverWork(result.Scenes)
		if len(work) > 0 {
			ttsStarted := time.Now()
			results, err := concurrent.Map(ctx, work, r.ttsConcurrency, func(opCtx context.Context, idx int, item voiceoverWork) (voiceoverResult, error) {
				sceneTTSStarted := time.Now()
				audioRef, err := r.voiceoverGen.Generate(opCtx, VoiceoverInput{
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
				if err != nil {
					return voiceoverResult{}, fmt.Errorf("scene %s lang %s: %w", item.sceneID, item.lang, err)
				}
				return voiceoverResult{
					audioRef: audioRef,
					metric: TTSSSceneMetric{
						SceneID:          item.sceneID,
						Language:         item.lang,
						DurationMS:       time.Since(sceneTTSStarted).Milliseconds(),
						Characters:       len([]rune(item.text)),
						Words:            len(strings.Fields(item.text)),
						OutputDurationMS: time.Duration(audioRef.Duration * float64(time.Second)).Milliseconds(),
					},
				}, nil
			})
			if err != nil {
				cause := fmt.Errorf("voiceover generation failed: %w", err)
				r.failExecutionStep(ctx, exec, voiceoverStep, cause)
				r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, cause)
				return false
			}
			// Apply results in canonical (work-item) order: voiceover
			// references, metrics, output-asset lineage, and narration-driven
			// durations. TTSMS records the fan-out wall clock; the per-scene
			// DurationMS fields carry the accumulated per-call work.
			for i, item := range work {
				res := results[i]
				if item.scene.Voiceover == nil {
					item.scene.Voiceover = make(map[Language]AudioReference)
				}
				item.scene.Voiceover[item.lang] = res.audioRef
				result.AudioMetrics.TTSScenes = append(result.AudioMetrics.TTSScenes, res.metric)
				if err := r.attachOutputAsset(ctx, exec, voiceoverStep.StepID, res.audioRef.ID, i); err != nil {
					r.failExecutionStep(ctx, exec, voiceoverStep, err)
					r.failRunWithRetry(ctx, runID, StageGeneratingVoiceovers, err)
					return false
				}
				// Audio-only scenes are narration-driven even when a clip is
				// attached as evidence. The clip total/used duration remains
				// metadata; it must never stretch the audio master.
				if (mode == capabilityaudio.AudioModeCombinedTimeline || item.scene.Clip == nil) && res.audioRef.Duration > 0 {
					item.scene.DurationMS = int64(res.audioRef.Duration*1000 + 0.5)
					item.scene.DurationUS = int64(res.audioRef.Duration*1_000_000 + 0.5)
				}
				// No mid-flight checkpoint here: the voiceover phase runs
				// concurrently with the VidRush join + overlay.prepare branch,
				// which writes the entity/overlay surfaces of the same result.
				// The caller checkpoints once after both branches join so the
				// two writers never serialize a half-projected result.
			}
			result.AudioMetrics.TTSMS += time.Since(ttsStarted).Milliseconds()
			result.AudioMetrics.TTSCalls += len(work)
		}
		r.recordVoiceoverOperation(ctx, result.AudioMetrics.TTSMS)
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
