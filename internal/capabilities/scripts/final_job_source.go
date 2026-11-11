package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// Source preparation for the final job: the local preflight and the durable
// receipt the runner reads back. Stock bytes never enter this path; the Master
// downloads and processes the Drive source.

// enforceFinalJobPreflight is the deterministic pre-enqueue gate: predictable
// remote scene rejection classes fail locally before the Master PREPARE exists.
func enforceFinalJobPreflight(scenes []map[string]any, certifiedAudioMS int64) error {
	if certifiedAudioMS <= 0 {
		return fmt.Errorf("final_job preflight: certified audio duration must be positive")
	}
	for i, scene := range scenes {
		seconds, ok := scene["duration_seconds"].(float64)
		if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
			return fmt.Errorf("final_job preflight: scene %d has an invalid duration %v", i, seconds)
		}
		if finalJobSceneMediaLocator(scene) == "" {
			id, _ := scene["scene_id"].(string)
			return fmt.Errorf("final_job preflight: scene %q carries no resolvable media locator (predicted remote prefetch rejection)", id)
		}
	}
	return nil
}

// finalJobSceneMediaLocator extracts the resolvable Drive locator from a
// remote scene's media slot ("stock" for both stock and caller-clip kinds).
func finalJobSceneMediaLocator(scene map[string]any) string {
	slot, ok := scene["stock"].(map[string]any)
	if !ok {
		return ""
	}
	if link := strings.TrimSpace(fmt.Sprint(slot["url"])); link != "" && link != "<nil>" {
		return link
	}
	if driveID := strings.TrimSpace(fmt.Sprint(slot["drive_file_id"])); driveID != "" && driveID != "<nil>" {
		return driveID
	}
	return ""
}

// restoreFinalJobFixedMedia keeps the canonical media-kind marker from the
// original generated scenes after the audio-only projection rewrites fixed
// intro/outro audio to silence or narration. The remote payload still needs to
// resolve those clips from Drive as fixed media, not as generated body clips.
func restoreFinalJobFixedMedia(result *GenerateResult, timeline *capabilityaudio.CanonicalTimeline) {
	if result == nil || timeline == nil {
		return
	}
	fixed := make(map[string]bool, len(result.Scenes))
	for _, scene := range result.Scenes {
		fixed[scene.ID] = scene.ExecutionMode.IsFixedMedia()
	}
	for i := range timeline.Segments {
		if isFixed, ok := fixed[timeline.Segments[i].ID]; ok {
			timeline.Segments[i].FixedMedia = isFixed
		}
	}
}

// finalJobReceiptSucceeded reports whether the durable receipt already records
// a COMPLETED remote render.
func finalJobReceiptSucceeded(receipt *RemoteFinalJobResult) bool {
	return receipt != nil && strings.EqualFold(strings.TrimSpace(receipt.Status), "SUCCEEDED")
}

func finalJobReceiptTerminal(receipt *RemoteFinalJobResult) bool {
	if receipt == nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(receipt.Status)) {
	case "FAILED", "CANCELLED", "CANCELED", "REJECTED":
		return true
	default:
		return false
	}
}

// finalJobPendingID returns the Master job id a previous attempt left in
// flight, or "" when there is nothing to resume.
func finalJobPendingID(receipt *RemoteFinalJobResult) string {
	if receipt == nil || finalJobReceiptSucceeded(receipt) || finalJobReceiptTerminal(receipt) {
		return ""
	}
	return strings.TrimSpace(receipt.JobID)
}

// finalJobDurableReceipt reads the receipt the RUN has persisted, which is the
// authoritative record of a remote render this run already started. The
// in-memory result only carries it when the attempt ADOPTED the checkpoint (see
// executionRun.start); a replay that rebuilt the result — or a fresh process
// resuming the run — must not lose the handle, because losing it means asking
// the Master for a second render of work it is already doing.
func (r *Runner) finalJobDurableReceipt(ctx context.Context, runID string) *RemoteFinalJobResult {
	if r.repo == nil {
		return nil
	}
	run, err := r.repo.Get(ctx, runID)
	if err != nil || run == nil || run.Result == nil {
		return nil
	}
	return run.Result.RemoteFinalJob
}

// submitFinalJob drives the optional remote render handoff. It is attach-first
// on purpose: an attempt that finds a pending receipt resumes THAT job, because
// a second PREPARE asks the Master for a second render of work it is already
// doing. Only a run with no receipt submits, and a run whose receipt is already
// SUCCEEDED is skipped entirely (a later phase failed the run after the render
// finished).
func (r *Runner) submitFinalJob(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) bool {
	if r.finalJobSubmitter == nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("final_job=true but remote final-job submitter is not configured"))
		return false
	}
	r.auditFinalJobArtifactReuse(runID, result)
	receipt := (*RemoteFinalJobResult)(nil)
	if result != nil {
		receipt = result.RemoteFinalJob
	}
	if receipt == nil {
		receipt = r.finalJobDurableReceipt(ctx, runID)
		if receipt != nil && result != nil {
			result.RemoteFinalJob = receipt
		}
	}
	if finalJobReceiptSucceeded(receipt) {
		recordFinalJobReport(ctx, receipt)
		if r.log != nil {
			r.log.Info("remote final job already completed by an earlier attempt; reusing its receipt",
				zap.String("run_id", runID), zap.String("remote_job_id", receipt.JobID))
		}
		return true
	}
	attempt := 1
	if receipt != nil {
		attempt = receipt.Attempt
		if attempt < 1 {
			attempt = 1
		}
		if finalJobReceiptTerminal(receipt) {
			if result != nil {
				result.RemoteFinalJobHistory = append(result.RemoteFinalJobHistory, *receipt)
			}
			attempt++
		}
		// A receipt without a job id is a durable PREPARE intent. Reuse its
		// attempt key so a lost PREPARE response reconciles to the same lease.
	}
	if jobID := finalJobPendingID(receipt); jobID != "" {
		attacher, ok := r.finalJobSubmitter.(FinalJobAttacher)
		if !ok {
			// Fail closed: the wired submitter cannot wait on the job this run
			// already started, and submitting again would duplicate a render the
			// Master is already performing.
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf(
				"remote final job %s is still in flight but the wired submitter cannot resume it (%T does not implement FinalJobAttacher)", jobID, r.finalJobSubmitter))
			return false
		}
		if r.log != nil {
			r.log.Info("resuming remote final job",
				zap.String("run_id", runID), zap.String("remote_job_id", jobID),
				zap.Int("yields", receipt.Yields), zap.Bool("wait_to_completion", receipt.Yields > 0))
		}
		remote, err := attacher.AttachFinalJob(ctx, jobID, receipt.Yields > 0)
		remote.Attempt = receipt.Attempt
		// The durable receipt already carries the locally measured handoff (and,
		// on a resumed job, possibly earlier Master phases); keep them when the
		// attach result only reports its own side.
		remote.Timing = receipt.Timing.Merge(remote.Timing)
		return r.settleFinalJob(ctx, runID, result, remote, receipt.Yields, err)
	}
	if starter, ok := r.finalJobSubmitter.(FinalJobStarter); ok {
		if result == nil {
			return r.settleFinalJob(ctx, runID, result, RemoteFinalJobResult{}, 0, fmt.Errorf("remote final job has no result to checkpoint before PREPARE"))
		}
		intent := RemoteFinalJobResult{Status: "PREPARE_INTENT", Attempt: attempt}
		result.RemoteFinalJob = &intent
		if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
			return r.settleFinalJob(ctx, runID, result, intent, 0, fmt.Errorf("%w: checkpoint PREPARE intent: %w", ErrFinalJobReceiptCheckpoint, err))
		}
		remote, err := starter.StartFinalJob(ctx, runID, req, result)
		remote.Attempt = attempt
		if err != nil {
			return r.settleFinalJob(ctx, runID, result, remote, 0, err)
		}
		if strings.TrimSpace(remote.JobID) == "" {
			return r.settleFinalJob(ctx, runID, result, remote, 0, fmt.Errorf("remote final job starter returned no job id"))
		}
		remote.Yields = 0
		result.RemoteFinalJob = &remote
		if err := r.repo.SavePartialResult(ctx, runID, result); err != nil {
			return r.settleFinalJob(ctx, runID, result, remote, 0, fmt.Errorf("checkpoint remote final job %s before attach: %w", remote.JobID, err))
		}
		attacher, ok := r.finalJobSubmitter.(FinalJobAttacher)
		if !ok {
			return r.settleFinalJob(ctx, runID, result, remote, 0, fmt.Errorf("remote final job starter cannot attach to job %s", remote.JobID))
		}
		attached, err := attacher.AttachFinalJob(ctx, remote.JobID, false)
		attached.Attempt = attempt
		// StartFinalJob measured the local handoff; the attach result knows the
		// Master phases. The persisted receipt must carry both.
		attached.Timing = remote.Timing.Merge(attached.Timing)
		return r.settleFinalJob(ctx, runID, result, attached, 0, err)
	}
	remote, err := r.finalJobSubmitter.SubmitFinalJob(ctx, runID, req, result)
	return r.settleFinalJob(ctx, runID, result, remote, 0, err)
}

// A clip-only run has no stock folder to chunk: its video IS the certified
// localized render of each scene. These helpers select that render and project
// it into the runtime asset reference. The source clip in the media library is
// never sent: it still carries the unmodified picture that this pipeline
// replaces (background, watermark, burnt subtitles).
//
// Moved verbatim from final_job_payload.go (2026-09-28, 731 → 581) to satisfy
// the strict 600-LOC forward-prevention gate (godlike/08) without changing
// behaviour. The payload BUILDER stays in final_job_payload.go; the certified
// render-selection family is one cohesive unit and lives next to the remote
// final-job lifecycle that consumes it.

// finalJobRenderLanguage resolves the single language whose certified renders
// feed the remote video. An empty language means the run produced no certified
// render at all (a stock-only run), which is not an error by itself. A run with
// renders in several languages and none in the source language is ambiguous and
// reports an error instead of picking one.

// --- stale remote receipt classification ---

// ErrFinalJobStaleReceipt marks a remote final-job identity the Master will
// never accept another FINALIZE for: a canceled preparation, or a lease held
// elsewhere (`job_already_leased`). The identity lives on the Master, so
// clearing the local receipt does not make it reusable. The receipt carrying
// it is archived as TERMINAL (see settleFinalJob), and the next attempt rotates
// the idempotency key (see finalJobAttemptIdempotencyKey) to prepare a fresh
// remote job instead of replaying the dead one.
var ErrFinalJobStaleReceipt = errors.New("remote final job identity is stale and must be replaced")

// auditFinalJobArtifactReuse makes the retry path's artifact reuse auditable.
// A final-job-only retry must carry the certified script, audio and overlay
// artifacts earlier attempts produced instead of regenerating them; these are
// the exact counts the handoff reuses, so an operator can tell a reuse from a
// regeneration (and a stuck run from a slow one) from the run log alone.
func (r *Runner) auditFinalJobArtifactReuse(runID string, result *GenerateResult) {
	if r == nil || r.log == nil || result == nil {
		return
	}
	scenes := len(result.Scenes)
	localizedRenders := len(result.LocalizedRenders)
	overlayItems, overlayRenders := 0, 0
	if result.OverlayPlan != nil {
		overlayItems = len(result.OverlayPlan.Items)
	}
	if result.OverlayRender != nil {
		overlayRenders = len(result.OverlayRender.Items) + len(result.OverlayRender.Batches)
	}
	finalAudioMS := int64(0)
	finalAudioID := ""
	if result.FinalAudio != nil {
		finalAudioMS = result.FinalAudio.DurationMS
		finalAudioID = strings.TrimSpace(result.FinalAudio.AssetID)
	}
	r.log.Info("final_job artifact reuse",
		zap.String("run_id", runID),
		zap.Int("scenes", scenes),
		zap.String("final_audio_asset_id", finalAudioID),
		zap.Int64("final_audio_ms", finalAudioMS),
		zap.Int("overlay_items", overlayItems),
		zap.Int("overlay_renders", overlayRenders),
		zap.Int("localized_renders", localizedRenders))
}

// finalJobAudioInput projects the canonical mix onto the remote final-job
// handoff: TTS, BGM and SFX pass through, and the original clip audio is
// restored at FinalJobRestoredClipGainDB instead of being dropped. History:
// this projection used to remove every AudioClip intent, which delivered
// videos with silent clip audio (2026-09-30 Milton incident) — the source
// speech was audible only in the local lane while the remote master mixed
// narration + music alone. The restored track ducks like any clip track:
// applyMixPolicy only deepens non-protected events toward the active duck
// gain while speech plays.
func finalJobAudioInput(result GenerateResult, language Language) GenerateResult {
	result.Scenes = append([]Scene(nil), result.Scenes...)
	alignFinalJobStockSceneDurations(&result)
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		// Fixed-media scenes normally require their source clip's original
		// audio. The remote final-job path deliberately hands that clip to 51,
		// so the local audio projection treats the slot as generated silence
		// or narration while retaining its explicit duration.
		if scene.ExecutionMode.IsFixedMedia() {
			scene.ExecutionMode = scriptpkg.SceneExecutionGenerated
		}
		intents := scene.AudioIntents
		if len(intents) == 0 && scene.Audio.Mode != "" {
			intents = []capabilityaudio.AudioIntent{scene.Audio}
		}
		// Ordinary scenes bound to an editorial clip must contribute that
		// clip's source audio to the same canonical master as their narration.
		// Historically only fixed intro/outro sections carried an AudioClip
		// intent, so intermediate clip scenes could reach 51 with narration
		// alone (or with an inconsistent source track inherited downstream).
		// Stock-selected scenes remain visual-only, EXCEPT for clips the caller
		// explicitly marked as used-as-stock: those keep the clip audio at full
		// original volume because the caller asked for that clip to be HEARD.
		stockClipIDs := sceneStockClipIDs(*scene)
		if !scene.ExecutionMode.IsFixedMedia() && scene.Stock == nil && scene.Clip != nil && !hasClipAudioIntent(intents, scene.Clip.ID) {
			if sourceInUS, sourceDurationUS, ok := finalJobClipAudioWindow(scene.Clip); ok {
				intents = append(intents, capabilityaudio.AudioIntent{
					Mode: capabilityaudio.AudioClip, ClipAssetID: scene.Clip.ID,
					SourceInUS: sourceInUS, SourceDurationUS: sourceDurationUS,
					TimelineOffsetUS: 0, TimelineDurationUS: sourceDurationUS,
					UseOriginalAudio: true, GainDB: kernelaudio.FinalJobRestoredClipGainDB,
				})
			}
		}
		// A stock-marked clip is an explicit editorial request to hear that
		// clip: its original audio joins the master at full volume even when
		// the scene's visual comes from a stock folder and even when the run's
		// global policy is VOICEOVER_ONLY.
		for _, clip := range sceneClips(*scene) {
			if !clip.usedAsStock() || hasClipAudioIntent(intents, clip.ID) {
				continue
			}
			if sourceInUS, sourceDurationUS, ok := finalJobClipAudioWindow(clip); ok {
				intents = append(intents, capabilityaudio.AudioIntent{
					Mode: capabilityaudio.AudioClip, ClipAssetID: clip.ID,
					SourceInUS: sourceInUS, SourceDurationUS: sourceDurationUS,
					TimelineOffsetUS: 0, TimelineDurationUS: sourceDurationUS,
					UseOriginalAudio: true, StockOriginalAudio: true, GainDB: 0,
				})
			}
		}
		filtered := make([]capabilityaudio.AudioIntent, 0, len(intents)+1)
		hasVoiceover := false
		for _, intent := range intents {
			if intent.Mode == capabilityaudio.AudioClip {
				// The clip's original audio reaches the remote master: keep the
				// intent and stamp the restored-mix gain. The mix policy still
				// ducks it under speech; the compiler never touches an explicit
				// non-zero GainDB. A stock-marked clip is the exception: the
				// caller asked for it AS STOCK, so it stays at full original
				// volume and keeps its protection against VO-only removal and
				// ducking.
				restored := intent
				if _, isStock := stockClipIDs[intent.ClipAssetID]; isStock {
					restored.StockOriginalAudio = true
					restored.GainDB = 0
				} else {
					restored.GainDB = kernelaudio.FinalJobRestoredClipGainDB
				}
				filtered = append(filtered, restored)
				continue
			}
			if intent.Mode == capabilityaudio.AudioVoiceover {
				hasVoiceover = true
			}
			filtered = append(filtered, intent)
		}
		if !hasVoiceover {
			if voiceover, ok := scene.Voiceover[language]; ok && voiceover.ID != "" {
				filtered = append(filtered, capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioVoiceover, VoiceoverAssetID: voiceover.ID})
			}
		}
		if len(filtered) == 0 {
			filtered = append(filtered, capabilityaudio.AudioIntent{Mode: capabilityaudio.AudioSilence})
		}
		scene.AudioIntents = filtered
		scene.Audio = filtered[0]
	}
	return result
}

// alignFinalJobStockSceneDurations rounds every stock-bound scene UP to the
// next whole multiple of the stock file duration the binding declares
// (start_ms/end_ms → StockBinding.DurationMs). The remote Master accepts only
// whole-file stock windows (r56), so a narration that ends mid-file can never
// be composed when the folder holds uniform files (r59b: 240×5000 ms vs a
// 4624 ms tail). Extending the scene window is the Producer-side fix that keeps
// stock remote: the certified voiceover keeps its own source duration, which
// the canonical plan places at the start of the longer window; the renderer
// fills the remainder with silence. No stock byte is downloaded, cut or
// re-encoded here.
//
// A binding without a positive duration declares no whole-file quantum and is
// left untouched; the planner keeps failing fast instead of guessing. The
// function copies ResolvedScenes before touching them, so the caller's result
// is never mutated.
func alignFinalJobStockSceneDurations(result *GenerateResult) {
	if result == nil {
		return
	}
	if len(result.ResolvedScenes) > 0 {
		result.ResolvedScenes = append([]ResolvedScene(nil), result.ResolvedScenes...)
	}
	alignedByScene := make(map[string]int64, len(result.Scenes))
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		if scene.Stock == nil || scene.Stock.DurationMs <= 0 {
			continue
		}
		quantumUS := scene.Stock.DurationMs * 1000
		durationUS := scene.DurationUS
		if durationUS <= 0 {
			durationUS = scene.DurationMS * 1000
		}
		if durationUS <= 0 {
			durationUS = quantumUS
		}
		alignedUS := ((durationUS + quantumUS - 1) / quantumUS) * quantumUS
		scene.DurationUS = alignedUS
		scene.DurationMS = alignedUS / 1000
		alignedByScene[scene.ID] = alignedUS
	}
	for i := range result.ResolvedScenes {
		if alignedUS, ok := alignedByScene[result.ResolvedScenes[i].ID]; ok {
			result.ResolvedScenes[i].DurationUS = alignedUS
		}
	}
}

func hasClipAudioIntent(intents []capabilityaudio.AudioIntent, clipID string) bool {
	for _, intent := range intents {
		if intent.Mode == capabilityaudio.AudioClip && intent.ClipAssetID == clipID {
			return true
		}
	}
	return false
}

func finalJobClipAudioWindow(clip *ClipReference) (sourceInUS, durationUS int64, ok bool) {
	if clip == nil || strings.TrimSpace(clip.ID) == "" || clip.SourceInMS < 0 {
		return 0, 0, false
	}
	sourceInUS = clip.SourceInMS * 1000
	if clip.SourceOutMS > clip.SourceInMS {
		durationUS = (clip.SourceOutMS - clip.SourceInMS) * 1000
	} else if total := clip.AssetDuration().DurationUS; total > sourceInUS {
		durationUS = total - sourceInUS
	}
	return sourceInUS, durationUS, durationUS > 0
}
