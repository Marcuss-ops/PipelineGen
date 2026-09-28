package scriptgeneration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"

	"sort"
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
// The remote final job still receives the certified composite and its original
// separately compiled final audio.
//
// The timestamp fix is done with a stream-copy remux first: `-copyts
// -start_at_zero` shifts the first timestamp to 0 without decoding or
// re-encoding, so a 3-minute 1080p source costs seconds instead of the 30-60s a
// full libx264 pass costs on the critical path. The copy is only
// attempted when the source video is already H.264/yuv420p (the intermediate
// composite's expected format) AND the remuxed first packet actually lands on
// zero; every other case falls back to the pre-existing re-encode, so the
// correctness contract is unchanged.
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
	// Fast path: stream-copy remux. It is taken only when the source video is
	// already H.264/yuv420p (so the copy stays byte-compatible with the
	// intermediate composite) and the remuxed first packet lands on zero; any
	// doubt falls through to the re-encode below.
	if ffprobe, probeErr := exec.LookPath("ffprobe"); probeErr == nil {
		if sourceVideoIsH264YUV420P(ctx, ffprobe, input) {
			if copyErr := remuxFinalJobSourceTimestamps(ctx, ffmpeg, input, output); copyErr == nil {
				if firstVideoPTSIsZero(ctx, ffprobe, output) {
					return commitNormalizedFinalJobSource(plan, source.AssetID, output, cleanup)
				}
			}
			_ = os.Remove(output)
		}
	}
	// Fallback: full re-encode (pre-existing behaviour) for a source that is
	// not already H.264/yuv420p, or whose stream-copied first timestamp did not
	// land on zero.
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
	return commitNormalizedFinalJobSource(plan, source.AssetID, output, cleanup)
}

// commitNormalizedFinalJobSource hashes the normalized artifact, rejects an
// empty result, and rewrites the plan's source to point at it. Both the remux
// fast path and the re-encode fallback end here so the two cannot drift.
func commitNormalizedFinalJobSource(plan capoverlay.OverlayPlan, sourceAssetID, output string, cleanup func()) (capoverlay.OverlayPlan, func(), error) {
	hash, size, hashErr := digest.SHA256File(output)
	if hashErr != nil {
		cleanup()
		return plan, nil, fmt.Errorf("hash normalized source asset %q: %w", sourceAssetID, hashErr)
	}
	if size == 0 {
		cleanup()
		return plan, nil, fmt.Errorf("normalized source asset %q is empty", sourceAssetID)
	}
	plan.Source = &capoverlay.OverlaySource{
		AssetID:   "finaljob-video-" + hash[:16],
		Path:      "assets/semantic/finaljob-video-" + hash + ".mp4",
		LocalPath: output, SHA256: hash,
	}
	return plan, cleanup, nil
}

// remuxFinalJobSourceTimestamps stream-copies the source video into a
// faststart MP4 whose first timestamp is shifted to zero. `-copyts` preserves
// the input timestamps and `-start_at_zero` shifts the output so the first one
// lands on zero — the only stream-copy combination that zeroes a POSITIVE first
// PTS (`-avoid_negative_ts make_zero` fixes negative timestamps only and leaves
// a positive start untouched). No frame is decoded or re-encoded.
func remuxFinalJobSourceTimestamps(ctx context.Context, ffmpeg, input, output string) error {
	cmd := exec.CommandContext(ctx, ffmpeg,
		"-hide_banner", "-loglevel", "error", "-y", "-fflags", "+genpts",
		"-i", input, "-map", "0:v:0", "-an",
		"-c", "copy", "-copyts", "-start_at_zero", "-movflags", "+faststart", output,
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(combined)))
	}
	return nil
}

// sourceVideoIsH264YUV420P reports whether the source's first video stream is
// already the format the intermediate composite expects, so a stream copy is
// byte-compatible and no re-encode is needed.
func sourceVideoIsH264YUV420P(ctx context.Context, ffprobe, input string) bool {
	fields := ffprobeKeyValues(ctx, ffprobe,
		"-select_streams", "v:0", "-show_entries", "stream=codec_name,pix_fmt", "-of", "default=noprint_wrappers=1", input)
	return strings.EqualFold(fields["codec_name"], "h264") &&
		strings.HasPrefix(strings.ToLower(fields["pix_fmt"]), "yuv420p")
}

// firstVideoPTSIsZero reports whether the output's first video packet starts at
// (or essentially at) timestamp zero. A non-zero first PTS is exactly the bug
// this normalization exists to prevent, so the remux is rejected when it does
// not hold and the caller falls back to the re-encode.
func firstVideoPTSIsZero(ctx context.Context, ffprobe, path string) bool {
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "packet=pts_time", "-of", "csv=p=0", "-read_intervals", "%+#1", path)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "N/A" {
			continue
		}
		value, convErr := strconv.ParseFloat(line, 64)
		if convErr != nil {
			continue
		}
		return value >= -0.001 && value <= 0.001
	}
	return false
}

// ffprobeKeyValues runs ffprobe with the given arguments and returns its
// `<key>=<value>` output lines as a map. A probe failure yields an empty map,
// which every caller treats as "cannot prove the copy is safe".
func ffprobeKeyValues(ctx context.Context, ffprobe string, args ...string) map[string]string {
	cmd := exec.CommandContext(ctx, ffprobe, append([]string{"-v", "error"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return map[string]string{}
	}
	fields := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return fields
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

// scheduleFinalJobSceneImage moves a contextual scene image to the first
// available interval after its planned start. The Master rejects intersecting
// replacement windows, while local semantic cards can occupy the same opening
// beat. Preserve every certified overlay and move only the generic scene image
// within its own scene window.
func scheduleFinalJobSceneImage(result *GenerateResult, image capoverlay.OverlayItem, frameGuardUS int64) (int64, int64, error) {
	start := image.StartUSValue()
	end := image.EndUSValue()
	duration := end - start
	if duration <= 0 {
		return 0, 0, fmt.Errorf("final_job scene image %q has an empty timing window", image.ID)
	}
	sceneStart, sceneEnd := int64(0), int64(0)
	if result != nil {
		if result.CanonicalTimeline != nil {
			for _, scene := range result.CanonicalTimeline.Segments {
				if scene.ID == image.SceneID {
					sceneStart, sceneEnd = scene.TimelineStartUS, scene.TimelineStartUS+scene.DurationUS
					break
				}
			}
		}
		for _, scene := range result.ResolvedScenes {
			if scene.ID == image.SceneID {
				sceneStart, sceneEnd = scene.TimelineStartUS, scene.TimelineStartUS+scene.DurationUS
				break
			}
		}
	}
	if sceneEnd > sceneStart {
		if start < sceneStart {
			start = sceneStart
		}
		if end > sceneEnd {
			end = sceneEnd
			duration = end - start
		}
	}
	if result == nil || result.OverlayPlan == nil {
		return start, start + duration, nil
	}
	occupied := make([][2]int64, 0)
	for _, other := range result.OverlayPlan.Items {
		if other.ID == image.ID || other.SceneID != image.SceneID {
			continue
		}
		otherStart, otherEnd := other.StartUSValue(), other.EndUSValue()
		if otherEnd > otherStart {
			occupied = append(occupied, [2]int64{otherStart, otherEnd})
		}
	}
	sort.Slice(occupied, func(i, j int) bool { return occupied[i][0] < occupied[j][0] })
	candidate := start
	for {
		moved := false
		for _, window := range occupied {
			if candidate < window[1] && window[0] < candidate+duration {
				candidate = window[1] + frameGuardUS
				moved = true
				break
			}
		}
		if !moved {
			break
		}
	}
	if sceneEnd > sceneStart && candidate+duration > sceneEnd {
		return 0, 0, fmt.Errorf("final_job scene image %q cannot fit a non-overlapping %dµs window in scene %q", image.ID, duration, image.SceneID)
	}
	return candidate, candidate + duration, nil
}
