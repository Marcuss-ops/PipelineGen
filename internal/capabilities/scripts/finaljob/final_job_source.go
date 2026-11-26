package finaljob

import (
	"errors"
	"fmt"
	"math"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func EnforceFinalJobPreflight(scenes []map[string]any, certifiedAudioMS int64) error {
	if certifiedAudioMS <= 0 {
		return fmt.Errorf("final_job preflight: certified audio duration must be positive")
	}
	for i, scene := range scenes {
		seconds, ok := scene["duration_seconds"].(float64)
		if !ok || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
			return fmt.Errorf("final_job preflight: scene %d has an invalid duration %v", i, seconds)
		}
		poolSlot, _ := scene["stock_pool_slot"].(bool)
		if !poolSlot && finalJobSceneMediaLocator(scene) == "" {
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

// RestoreFinalJobFixedMedia keeps the canonical media-kind marker from the
// original generated scenes after the audio-only projection rewrites fixed
// intro/outro audio to silence or narration. The remote payload still needs to
// resolve those clips from Drive as fixed media, not as generated body clips.
func RestoreFinalJobFixedMedia(result *GenerateResult, timeline *capabilityaudio.CanonicalTimeline) {
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

// FinalJobReceiptSucceeded reports whether the durable receipt already records
// a COMPLETED remote render.
func FinalJobReceiptSucceeded(receipt *RemoteFinalJobResult) bool {
	return receipt != nil && strings.EqualFold(strings.TrimSpace(receipt.Status), "SUCCEEDED")
}

func FinalJobReceiptTerminal(receipt *RemoteFinalJobResult) bool {
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

// FinalJobPendingID returns the Master job id a previous attempt left in
// flight, or "" when there is nothing to resume.
func FinalJobPendingID(receipt *RemoteFinalJobResult) string {
	if receipt == nil || FinalJobReceiptSucceeded(receipt) || FinalJobReceiptTerminal(receipt) {
		return ""
	}
	return strings.TrimSpace(receipt.JobID)
}

// FinalJobPreparedID returns the Master job id of a split handoff whose base
// PREPARE was accepted but whose overlay FINALIZE has not been sent yet. It is
// deliberately distinct from FinalJobPendingID: a PREPARED job is waiting for
// runtime assets, so ATTACHING would sit on it until the queue watchdog fires;
// the only correct next step is FINALIZE.
func FinalJobPreparedID(receipt *RemoteFinalJobResult) string {
	if receipt == nil || !strings.EqualFold(strings.TrimSpace(receipt.Status), "PREPARED") {
		return ""
	}
	return strings.TrimSpace(receipt.JobID)
}

// finalJobDurableReceipt reads the receipt the RUN has persisted, which is the
// authoritative record of a remote render this run already started. The
// in-memory result only carries it when the attempt ADOPTED the checkpoint (see
// executionRun.Start); a replay that rebuilt the result — or a fresh process
// resuming the run — must not lose the handle, because losing it means asking
// the Master for a second render of work it is already doing.
var ErrFinalJobStaleReceipt = errors.New("remote final job identity is stale and must be replaced")

// ErrFinalJobInvalidPlan marks a Master rejection of the PLAN itself (an
// unrenderable window, an unsupported media contract, a plan-level validation
// failure). It is terminal and distinct from ErrFinalJobStaleReceipt: the
// remote identity is fine, so rotating it would resubmit the same defective
// plan and reproduce the rejection. The fix is a regenerated plan.
var ErrFinalJobInvalidPlan = errors.New("remote final job rejected the overlay plan")

// ErrFinalJobCapabilityMissing marks a remote service that does not expose a
// capability required by an otherwise valid final-job plan (the r17b 422
// stock_window_preparation drift). The payload is not at fault; retrying cannot
// change the service version, so the run must fail as a configuration gap.
var ErrFinalJobCapabilityMissing = errors.New("remote final job is missing a required capability")

// auditFinalJobArtifactReuse makes the retry path's artifact reuse auditable.
// A final-job-only retry must carry the certified script, audio and overlay
// artifacts earlier attempts produced instead of regenerating them; these are
// the exact counts the handoff reuses, so an operator can tell a reuse from a
// regeneration (and a stuck run from a slow one) from the run log alone.
func FinalJobAudioInput(result GenerateResult, language Language) GenerateResult {
	result.Scenes = append([]Scene(nil), result.Scenes...)
	for i := range result.Scenes {
		scene := &result.Scenes[i]
		fixedMedia := scene.ExecutionMode.IsFixedMedia()
		// Fixed-media scenes normally require their source clip's original
		// audio. The remote final-job path deliberately hands that clip to 51,
		// so the local audio projection treats the slot as generated silence
		// or narration while retaining its explicit duration.
		if fixedMedia {
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
		if !fixedMedia && scene.Stock == nil && scene.Clip != nil && !hasClipAudioIntent(intents, scene.Clip.ID) {
			if SourceInUS, SourceDurationUS, ok := finalJobClipAudioWindow(scene.Clip); ok {
				intents = append(intents, capabilityaudio.AudioIntent{
					Mode: capabilityaudio.AudioClip, ClipAssetID: scene.Clip.ID,
					SourceInUS: SourceInUS, SourceDurationUS: SourceDurationUS,
					TimelineOffsetUS: 0, TimelineDurationUS: SourceDurationUS,
					UseOriginalAudio: true, GainDB: kernelaudio.FinalJobRestoredClipGainDB,
				})
			}
		}
		// A stock-marked clip is an explicit editorial request to hear that
		// clip: its original audio joins the master at full volume even when
		// the scene's visual comes from a stock folder and even when the run's
		// global policy is VOICEOVER_ONLY.
		for _, clip := range sceneClips(*scene) {
			if !clip.UsedAsStock() || hasClipAudioIntent(intents, clip.ID) {
				continue
			}
			if SourceInUS, SourceDurationUS, ok := finalJobClipAudioWindow(clip); ok {
				intents = append(intents, capabilityaudio.AudioIntent{
					Mode: capabilityaudio.AudioClip, ClipAssetID: clip.ID,
					SourceInUS: SourceInUS, SourceDurationUS: SourceDurationUS,
					TimelineOffsetUS: 0, TimelineDurationUS: SourceDurationUS,
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
				} else if intent.ProtectedOriginalAudio {
					// A protected fixed-media span is the editorial sound of that
					// cut (opening hooks, interstitials). The final-job handoff
					// must keep it at unity: stamping the restored-mix bed level
					// (-10 dB) made the hook read as missing beside narration.
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

func hasClipAudioIntent(intents []capabilityaudio.AudioIntent, clipID string) bool {
	for _, intent := range intents {
		if intent.Mode == capabilityaudio.AudioClip && intent.ClipAssetID == clipID {
			return true
		}
	}
	return false
}

func finalJobClipAudioWindow(clip *ClipReference) (SourceInUS, durationUS int64, ok bool) {
	if clip == nil || strings.TrimSpace(clip.ID) == "" || clip.SourceInMS < 0 {
		return 0, 0, false
	}
	SourceInUS = clip.SourceInMS * 1000
	if clip.SourceOutMS > clip.SourceInMS {
		durationUS = (clip.SourceOutMS - clip.SourceInMS) * 1000
	} else if total := clip.AssetDuration().DurationUS; total > SourceInUS {
		durationUS = total - SourceInUS
	}
	return SourceInUS, durationUS, durationUS > 0
}
