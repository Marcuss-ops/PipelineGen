package scriptgeneration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernelaudio "github.com/Marcuss-ops/PipelineGen/internal/kernel/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// FinalJobSubmitter hands a completed local media plan to the configured
// remote render master. It is intentionally absent from runs with
// final_job=false.
//
// The handoff is a three-phase job (PREPARE → FINALIZE → poll). The submitter
// owns the first two phases and the FIRST bounded wait; a wait that outlives
// that budget is reported with ErrFinalJobPending, and the durable receipt it
// returns carries the Master's job id so a later attempt resumes from it (see
// FinalJobAttacher) instead of asking the Master for a second render.
type FinalJobSubmitter interface {
	SubmitFinalJob(context.Context, string, GenerateRequest, *GenerateResult) (RemoteFinalJobResult, error)
}

// ErrFinalJobPending is the capability-owned sentinel for a remote render that
// EXISTS on the Master and is still running. It is a sentinel so the runner can
// branch on it without importing the transport package, and it always travels
// with the receipt that carries the job id — the handle an attempt must persist
// before handing the wait back.
var ErrFinalJobPending = errors.New("remote final job is still rendering")

// FinalJobAttacher is the OPTIONAL half of the final-job contract: waiting on a
// job that a PREVIOUS attempt already submitted, with no second PREPARE. A
// submitter that cannot resume one is still valid (it is what every non-final
// run wires), but a run that already has a pending receipt fails closed rather
// than submit a duplicate render.
type FinalJobAttacher interface {
	// waitToCompletion=false waits only up to the submitter's attach budget and
	// reports ErrFinalJobPending when it expires; true waits with the
	// submitter's full poll timeout, which is the pre-split blocking contract.
	AttachFinalJob(ctx context.Context, jobID string, waitToCompletion bool) (RemoteFinalJobResult, error)
}

// RemoteFinalJobResult is the durable receipt for the two-stage remote job.
type RemoteFinalJobResult struct {
	JobID       string `json:"job_id"`
	Status      string `json:"status"`
	WorkerID    string `json:"worker_id,omitempty"`
	ArtifactURL string `json:"artifact_url,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	// Yields counts how many attempts have already handed this job's wait back
	// to a later attempt. It is persisted with the receipt because it is what
	// bounds the churn: the FIRST wait is bounded (the worker is freed), and
	// every wait after it runs to completion, so a render longer than the whole
	// retry window still finishes instead of dying on an exhausted attempt
	// budget. Only meaningful while the job is pending.
	Yields int `json:"yields,omitempty"`
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
				// The clip's original audio reaches the remote master: keep the
				// intent and stamp the restored-mix gain. The mix policy still
				// ducks it under speech; the compiler never touches an explicit
				// non-zero GainDB.
				restored := intent
				restored.GainDB = kernelaudio.FinalJobRestoredClipGainDB
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

// finalJobReceiptSucceeded reports whether the durable receipt already records
// a COMPLETED remote render.
func finalJobReceiptSucceeded(receipt *RemoteFinalJobResult) bool {
	return receipt != nil && strings.EqualFold(strings.TrimSpace(receipt.Status), "SUCCEEDED")
}

// finalJobPendingID returns the Master job id a previous attempt left in
// flight, or "" when there is nothing to resume.
func finalJobPendingID(receipt *RemoteFinalJobResult) string {
	if receipt == nil || finalJobReceiptSucceeded(receipt) {
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
		if r.log != nil {
			r.log.Info("remote final job already completed by an earlier attempt; reusing its receipt",
				zap.String("run_id", runID), zap.String("remote_job_id", receipt.JobID))
		}
		return true
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
		return r.settleFinalJob(ctx, runID, result, remote, receipt.Yields, err)
	}
	remote, err := r.finalJobSubmitter.SubmitFinalJob(ctx, runID, req, result)
	return r.settleFinalJob(ctx, runID, result, remote, 0, err)
}

// settleFinalJob is the SINGLE interpretation of a remote final-job outcome.
// Both entries (submit and attach) end here so they cannot disagree about what
// a pending wait, a terminal failure or a completed render means.
//
// yields is how many attempts already handed the wait back before this one.
func (r *Runner) settleFinalJob(ctx context.Context, runID string, result *GenerateResult, remote RemoteFinalJobResult, yields int, err error) bool {
	if err != nil {
		if errors.Is(err, ErrFinalJobPending) && result != nil {
			receipt := remote
			if strings.TrimSpace(receipt.JobID) == "" && result.RemoteFinalJob != nil {
				receipt.JobID = strings.TrimSpace(result.RemoteFinalJob.JobID)
			}
			receipt.Yields = yields + 1
			result.RemoteFinalJob = &receipt
			// Persist the handle BEFORE ending the attempt: the Master keeps
			// rendering, and this receipt is the only address of that work. The
			// checkpoint is the same durable partial-result write the success path
			// uses (not the debounced one), so the next attempt cannot miss it.
			r.checkpoint(ctx, runID, result)
			if r.log != nil {
				r.log.Info("remote final job still rendering; handing the wait back to a later attempt",
					zap.String("run_id", runID), zap.String("remote_job_id", receipt.JobID),
					zap.String("remote_status", receipt.Status), zap.Int("yields", receipt.Yields))
			}
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf(
				"remote final job %s is still %s: %w", receipt.JobID, receipt.Status, ErrFinalJobPending))
			return false
		}
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job: %w", err))
		return false
	}
	if strings.TrimSpace(remote.JobID) == "" || !finalJobReceiptSucceeded(&remote) {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job returned incomplete/non-success result (job_id=%q status=%q)", remote.JobID, remote.Status))
		return false
	}
	if result == nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, fmt.Errorf("remote final job %s succeeded but the run has no result to record it in", remote.JobID))
		return false
	}
	result.RemoteFinalJob = &remote
	r.checkpoint(ctx, runID, result)
	if r.log != nil {
		r.log.Info("remote final job completed", zap.String("run_id", runID), zap.String("remote_job_id", remote.JobID), zap.String("worker_id", remote.WorkerID))
	}
	return true
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
func finalJobRenderLanguage(req GenerateRequest, result *GenerateResult) (string, error) {
	if result == nil || len(result.LocalizedRenders) == 0 {
		return "", nil
	}
	preferred := strings.TrimSpace(string(req.SourceLanguage))
	if preferred != "" {
		for _, rendered := range result.LocalizedRenders {
			if strings.EqualFold(strings.TrimSpace(string(rendered.Language)), preferred) {
				return strings.TrimSpace(string(rendered.Language)), nil
			}
		}
	}
	languages := make(map[string]struct{}, len(result.LocalizedRenders))
	for _, rendered := range result.LocalizedRenders {
		if language := strings.TrimSpace(string(rendered.Language)); language != "" {
			languages[language] = struct{}{}
		}
	}
	if len(languages) == 1 {
		for language := range languages {
			return language, nil
		}
	}
	available := make([]string, 0, len(languages))
	for language := range languages {
		available = append(available, language)
	}
	sort.Strings(available)
	return "", fmt.Errorf("no certified render for source language %q and the run produced %d render languages (%s)", preferred, len(available), strings.Join(available, ", "))
}

// certifiedLocalizedRendersForScene returns the certified renders of one scene
// for one language. A render is certified only when its published MP4 identity
// is complete (drive file id + 64-char sha256 + positive duration): the runtime
// copies those bytes by identity and never re-renders them here.
func certifiedLocalizedRendersForScene(result *GenerateResult, sceneID, language string) []LocalizedRenderResult {
	if result == nil {
		return nil
	}
	var out []LocalizedRenderResult
	for _, rendered := range result.LocalizedRenders {
		if !strings.EqualFold(strings.TrimSpace(rendered.SceneID), sceneID) {
			continue
		}
		if language != "" && !strings.EqualFold(strings.TrimSpace(string(rendered.Language)), language) {
			continue
		}
		if !localizedRenderIsCertifiedClip(rendered) {
			continue
		}
		out = append(out, rendered)
	}
	return out
}

// certifiedLocalizedRenderForClip is the fixed-media projection: one certified
// render for the exact (scene, clip, language) unit, or none.
func certifiedLocalizedRenderForClip(result *GenerateResult, sceneID, clipID, language string) (LocalizedRenderResult, bool) {
	if result == nil || strings.TrimSpace(clipID) == "" {
		return LocalizedRenderResult{}, false
	}
	for _, rendered := range certifiedLocalizedRendersForScene(result, sceneID, language) {
		if strings.EqualFold(strings.TrimSpace(rendered.ClipID), strings.TrimSpace(clipID)) {
			return rendered, true
		}
	}
	return LocalizedRenderResult{}, false
}

func localizedRenderIsCertifiedClip(rendered LocalizedRenderResult) bool {
	return strings.TrimSpace(rendered.DriveFileID) != "" && len(strings.TrimSpace(rendered.SHA256)) == 64 && rendered.DurationMS > 0
}

// clipSceneRuntimeAsset builds the runtime asset reference of a clip-only scene
// from its certified localized render. A scene without a certified render — or
// a scene that produced more than one — fails closed: the clip-only contract is
// "send the clip this pipeline produced", so there is no fallback to the
// unmodified source clip. The payload builder may repeat this certified visual
// to fill the longer canonical narration duration.
func clipSceneRuntimeAsset(ctx context.Context, resolver FinalJobAssetResolver, result *GenerateResult, renderLanguage string, renderLanguageErr error, sceneID string) (map[string]any, int64, error) {
	if renderLanguageErr != nil {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q: %w", sceneID, renderLanguageErr)
	}
	rendered := certifiedLocalizedRendersForScene(result, sceneID, renderLanguage)
	if len(rendered) == 0 {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q has no certified rendered clip for language %q: refusing to hand the unmodified source clip to the runtime", sceneID, renderLanguage)
	}
	if len(rendered) > 1 {
		return nil, 0, fmt.Errorf("final_job clip-only scene %q has %d certified rendered clips for language %q: a clip-only final job sends exactly one rendered clip per scene", sceneID, len(rendered), renderLanguage)
	}
	return renderedClipAssetRef(ctx, resolver, rendered[0], sceneID)
}

// resolveFinalJobFixedMediaAsset prefers the certified render of one fixed
// (intro/outro) clip over the unmodified library clip, and keeps the library
// asset as the fallback: a fixed section may legitimately be declared without a
// render lane, which is the pre-existing behaviour for protected intros.
func resolveFinalJobFixedMediaAsset(ctx context.Context, resolver FinalJobAssetResolver, result *GenerateResult, sceneID, clipID, renderLanguage string) (map[string]any, error) {
	if renderLanguage != "" {
		if rendered, ok := certifiedLocalizedRenderForClip(result, sceneID, clipID, renderLanguage); ok {
			asset, _, err := renderedClipAssetRef(ctx, resolver, rendered, sceneID)
			return asset, err
		}
	}
	return resolver.ResolveFinalJobAsset(ctx, clipID)
}

// renderedClipAssetRef projects a certified localized render into the runtime
// asset reference the remote scene copies. The key set matches the reference
// ResolveFinalJobAsset produces, so the remote sees one asset shape regardless
// of whether the bytes came from the media library or from this render lane.
func renderedClipAssetRef(ctx context.Context, resolver FinalJobAssetResolver, rendered LocalizedRenderResult, sceneID string) (map[string]any, int64, error) {
	driveID := strings.TrimSpace(rendered.DriveFileID)
	sha := strings.TrimSpace(rendered.SHA256)
	if driveID == "" || len(sha) != 64 || rendered.DurationMS <= 0 {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %q is not a certified published MP4 (drive_file_id, 64-char sha256 and positive duration required)", sceneID, rendered.AssetID)
	}
	size, err := resolver.FinalJobPublishedFileSize(ctx, driveID)
	if err != nil {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %s: %w", sceneID, driveID, err)
	}
	if size <= 0 {
		return nil, 0, fmt.Errorf("final_job scene %q rendered clip %s has no published byte size", sceneID, driveID)
	}
	assetID := strings.TrimSpace(rendered.AssetID)
	if assetID == "" {
		assetID = driveID
	}
	return map[string]any{
		"asset_id": assetID, "drive_file_id": driveID, "url": driveFileWebLink(driveID),
		"sha256": sha, "size_bytes": size, "duration_ms": rendered.DurationMS,
	}, rendered.DurationMS, nil
}
