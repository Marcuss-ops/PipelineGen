package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// FinalJobSubmitter hands a completed local media plan to the configured
// remote render master. It is intentionally absent from runs with
// final_job=false.
type FinalJobSubmitter interface {
	SubmitFinalJob(context.Context, string, GenerateRequest, *GenerateResult) (RemoteFinalJobResult, error)
}

// RemoteFinalJobResult is the durable receipt for the two-stage remote job.
type RemoteFinalJobResult struct {
	JobID       string `json:"job_id"`
	Status      string `json:"status"`
	WorkerID    string `json:"worker_id,omitempty"`
	ArtifactURL string `json:"artifact_url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

// finalJobAudioInput keeps the generated voice track but removes source-video
// audio from the 77-side mix. The remote worker owns stock-video downloads;
// this host only compiles the published TTS, BGM and SFX assets for handoff.
func finalJobAudioInput(result GenerateResult, language Language) GenerateResult {
	result.Scenes = append([]Scene(nil), result.Scenes...)
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
		filtered := make([]capabilityaudio.AudioIntent, 0, len(intents)+1)
		hasVoiceover := false
		for _, intent := range intents {
			if intent.Mode == capabilityaudio.AudioClip {
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

// normalizeFinalJobVideoBackground makes producer-local stock inputs safe for
// Chronon's native frame-zero decoder. Drive clips can have their first video
// sample at a small positive PTS (for example 21 ms); a composite starts at
// zero and the native decoder correctly rejects a request before that sample.
// Re-encode a silent, timestamp-zero copy for the intermediate composite only.
// The remote final job still receives the certified composite and its original
// separately compiled final audio.
func normalizeFinalJobVideoBackground(ctx context.Context, plan capoverlay.OverlayPlan) (capoverlay.OverlayPlan, func(), error) {
	if plan.Source == nil {
		return plan, nil, nil
	}
	source := *plan.Source
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return plan, nil, fmt.Errorf("ffmpeg is required to normalize stock timestamps: %w", err)
	}
	workDir, err := os.MkdirTemp("", "pipelinegen-final-composite-")
	if err != nil {
		return plan, nil, fmt.Errorf("create normalization workspace: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(workDir) }
	input := strings.TrimSpace(source.LocalPath)
	if input == "" {
		cleanup()
		return plan, nil, fmt.Errorf("source asset %q has no materialized local path", source.AssetID)
	}
	output := filepath.Join(workDir, "source.mp4")
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y", "-fflags", "+genpts",
		"-i", input, "-map", "0:v:0", "-an", "-vf", "setpts=PTS-STARTPTS",
		"-vsync", "0", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "20",
		"-pix_fmt", "yuv420p", "-avoid_negative_ts", "make_zero", "-movflags", "+faststart", output,
	)
	if combined, runErr := cmd.CombinedOutput(); runErr != nil {
		cleanup()
		return plan, nil, fmt.Errorf("normalize source asset %q: %w: %s", source.AssetID, runErr, strings.TrimSpace(string(combined)))
	}
	hash, size, hashErr := digest.SHA256File(output)
	if hashErr != nil {
		cleanup()
		return plan, nil, fmt.Errorf("hash normalized source asset %q: %w", source.AssetID, hashErr)
	}
	if size == 0 {
		cleanup()
		return plan, nil, fmt.Errorf("normalized source asset %q is empty", source.AssetID)
	}
	plan.Source = &capoverlay.OverlaySource{
		AssetID:   "finaljob-video-" + hash[:16],
		Path:      "assets/semantic/finaljob-video-" + hash + ".mp4",
		LocalPath: output, SHA256: hash,
	}
	return plan, cleanup, nil
}

func (r *Runner) submitFinalJob(ctx context.Context, runID string, req GenerateRequest, result *GenerateResult) bool {
	if r.finalJobSubmitter == nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("final_job=true but remote final-job submitter is not configured"))
		return false
	}
	remote, err := r.finalJobSubmitter.SubmitFinalJob(ctx, runID, req, result)
	if err != nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job: %w", err))
		return false
	}
	if strings.TrimSpace(remote.JobID) == "" || !strings.EqualFold(strings.TrimSpace(remote.Status), "SUCCEEDED") {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job returned incomplete/non-success result (job_id=%q status=%q)", remote.JobID, remote.Status))
		return false
	}
	result.RemoteFinalJob = &remote
	r.checkpoint(ctx, runID, result)
	if r.log != nil {
		r.log.Info("remote final job completed", zap.String("run_id", runID), zap.String("remote_job_id", remote.JobID), zap.String("worker_id", remote.WorkerID))
	}
	return true
}
