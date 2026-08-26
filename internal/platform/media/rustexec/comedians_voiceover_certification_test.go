package rustexec

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// dbToLinear mirrors the canonical dB→linear power conversion implemented on
// the Rust boundary (render_audio_execution.rs::linear_gain). It is the Go
// regression guard for the FFmpeg volume= filter contract: 0 dB must map to
// unity, never to volume=0.
func dbToLinear(gainDB float64) float64 {
	return math.Pow(10, gainDB/20)
}

func linearGain(gainDB float64) string { return fmt.Sprintf("%.6f", dbToLinear(gainDB)) }

func TestVoiceoverGainConversionZeroDBIsUnity(t *testing.T) {
	cases := []struct {
		db   float64
		want string
	}{
		{0, "1.000000"},
		{-6, "0.501187"},
		{-12, "0.251189"},
		{-18, "0.125893"},
		{-24, "0.063096"},
	}
	for _, tc := range cases {
		if got := linearGain(tc.db); got != tc.want {
			t.Fatalf("linearGain(%.0f) = %s, want %s", tc.db, got, tc.want)
		}
	}
	if dbToLinear(0) != 1.0 {
		t.Fatalf("0 dB must map to unity, got %v", dbToLinear(0))
	}
}

type voiceoverSceneSpec struct {
	index         int
	clipID        string
	voiceoverID   string
	timelineStart int64
	durationUS    int64
	voDurationUS  int64
	clipSourceUS  int64
}

func comediansTenScenes() []voiceoverSceneSpec {
	specs := make([]voiceoverSceneSpec, 0, 10)
	for i := 0; i < 10; i++ {
		specs = append(specs, voiceoverSceneSpec{
			index:         i,
			clipID:        fmt.Sprintf("clip-%02d", i),
			voiceoverID:   fmt.Sprintf("vo-%02d", i),
			timelineStart: int64(i) * 1_000_000,
			durationUS:    1_000_000,
			voDurationUS:  1_000_000,
			clipSourceUS:  1_000_000,
		})
	}
	return specs
}

func buildVoiceoverTimeline(specs []voiceoverSceneSpec) audio.CanonicalTimeline {
	segments := make([]audio.TimelineSegment, 0, len(specs))
	for _, spec := range specs {
		voiceover := audio.AudioIntent{
			Mode:               audio.AudioVoiceover,
			VoiceoverAssetID:   spec.voiceoverID,
			SourceInUS:         0,
			SourceDurationUS:   spec.voDurationUS,
			TimelineOffsetUS:   0,
			TimelineDurationUS: spec.voDurationUS,
		}
		clipAudio := audio.AudioIntent{
			Mode:             audio.AudioClip,
			ClipAssetID:      spec.clipID,
			SourceInUS:       0,
			SourceDurationUS: spec.clipSourceUS,
			UseOriginalAudio: true,
		}
		segments = append(segments, audio.TimelineSegment{
			ID:              spec.clipID,
			Index:           spec.index,
			TimelineStartUS: spec.timelineStart,
			DurationUS:      spec.durationUS,
			Video: audio.VideoSegment{
				AssetID:          spec.clipID,
				SourceInUS:       0,
				SourceDurationUS: spec.durationUS,
			},
			Audio:        voiceover,
			AudioIntents: []audio.AudioIntent{voiceover, clipAudio},
		})
	}
	var total int64
	for _, s := range segments {
		total += s.DurationUS
	}
	return audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: total, Segments: segments}
}

func compileDuckedVoiceoverPlan(t *testing.T, timeline audio.CanonicalTimeline) audio.CompiledAudioPlan {
	t.Helper()
	plan, err := audio.CompileWithMixPolicy(timeline, audio.DefaultAudioProfile(), audio.MixVoiceoverWithDuckedClip)
	if err != nil {
		t.Fatalf("compile ducked voiceover plan: %v", err)
	}
	return plan
}

func compileVoiceoverOnlyPlan(t *testing.T, timeline audio.CanonicalTimeline) audio.CompiledAudioPlan {
	t.Helper()
	plan, err := audio.CompileWithMixPolicy(timeline, audio.DefaultAudioProfile(), audio.MixVoiceoverOnly)
	if err != nil {
		t.Fatalf("compile voiceover-only plan: %v", err)
	}
	return plan
}

func TestVoiceoverExactlyOnePerScene(t *testing.T) {
	specs := comediansTenScenes()
	plan := compileDuckedVoiceoverPlan(t, buildVoiceoverTimeline(specs))

	voEvents := trackEvents(plan, audio.TrackVoiceover)
	clipEvents := trackEvents(plan, audio.TrackClipAudio)
	if len(voEvents) != len(specs) || len(clipEvents) != len(specs) {
		t.Fatalf("cardinality: vo=%d clip=%d scenes=%d", len(voEvents), len(clipEvents), len(specs))
	}
	seen := map[string]struct{}{}
	for _, ev := range voEvents {
		if ev.AssetID == "" {
			t.Fatalf("voiceover event without asset: %+v", ev)
		}
		if _, dup := seen[ev.AssetID]; dup {
			t.Fatalf("voiceover asset %q reused across scenes", ev.AssetID)
		}
		seen[ev.AssetID] = struct{}{}
	}
	if len(seen) != len(specs) {
		t.Fatalf("unique voiceover IDs = %d, want %d", len(seen), len(specs))
	}
}

func TestVoiceoverTimelinePlacement(t *testing.T) {
	specs := comediansTenScenes()
	timeline := buildVoiceoverTimeline(specs)
	for i, segment := range timeline.Segments {
		var vo *audio.AudioIntent
		for j := range segment.AudioIntents {
			if segment.AudioIntents[j].Mode == audio.AudioVoiceover {
				vo = &segment.AudioIntents[j]
				break
			}
		}
		if vo == nil {
			t.Fatalf("scene %d has no voiceover intent", i)
		}
		if vo.VoiceoverAssetID == "" || vo.TimelineOffsetUS != 0 || vo.SourceInUS != 0 || vo.SourceDurationUS <= 0 {
			t.Fatalf("scene %d voiceover intent placement drift: %+v", i, vo)
		}
	}
	plan := compileDuckedVoiceoverPlan(t, timeline)
	voEvents := trackEvents(plan, audio.TrackVoiceover)
	for i, spec := range specs {
		ev := voEvents[i]
		if ev.TimelineStartUS != spec.timelineStart || ev.SourceInUS != 0 || ev.SourceDurationUS != spec.voDurationUS {
			t.Fatalf("voiceover event[%d] placement drift: got=%+v want start=%d source_duration=%d", i, ev, spec.timelineStart, spec.voDurationUS)
		}
	}
}

func TestVoiceoverPlanCardinalityAndMixPolicy(t *testing.T) {
	specs := comediansTenScenes()
	plan := compileDuckedVoiceoverPlan(t, buildVoiceoverTimeline(specs))
	if plan.MixPolicy != audio.MixVoiceoverWithDuckedClip || plan.Version != audio.AudioPlanVersion || plan.PlanSHA256 == "" {
		t.Fatalf("plan is not a sealed ducked v2 plan: policy=%s version=%s hash=%q", plan.MixPolicy, plan.Version, plan.PlanSHA256)
	}
	for _, ev := range trackEvents(plan, audio.TrackClipAudio) {
		if !ev.UseOriginalAudio || ev.GainDB != audio.DuckClipBaseGainDB {
			t.Fatalf("clip event must carry original audio at ducked base gain: %+v", ev)
		}
	}
	for _, ev := range trackEvents(plan, audio.TrackVoiceover) {
		if ev.GainDB != 0 {
			t.Fatalf("voiceover must stay at unity under the ducked policy: %+v", ev)
		}
	}
	if len(plan.Automation) != len(specs) {
		t.Fatalf("ducking automation = %d, want %d", len(plan.Automation), len(specs))
	}
	for i, spec := range specs {
		a := plan.Automation[i]
		if a.TargetTrackID != "clip_audio" || a.GainDB != audio.DuckClipActiveGainDB || a.StartUS != spec.timelineStart || a.EndUS != spec.timelineStart+spec.durationUS || a.AttackUS != audio.DuckAttackUS || a.ReleaseUS != audio.DuckReleaseUS {
			t.Fatalf("ducking automation[%d] drift: %+v", i, a)
		}
	}
}

// TestVoiceoverDuckWindowFollowsVoiceoverDuration certifies that the ducking
// window ends where the real narration ends, not necessarily at the scene
// boundary. The clip stays under the voiceover only while speech is active and
// returns to its base gain once the (shorter) narration is over.
func TestVoiceoverDuckWindowFollowsVoiceoverDuration(t *testing.T) {
	specs := []voiceoverSceneSpec{{
		index: 0, clipID: "clip-long", voiceoverID: "vo-short",
		timelineStart: 0, durationUS: 10_000_000, voDurationUS: 6_000_000, clipSourceUS: 10_000_000,
	}}
	plan := compileDuckedVoiceoverPlan(t, buildVoiceoverTimeline(specs))

	vo := trackEvents(plan, audio.TrackVoiceover)[0]
	clip := trackEvents(plan, audio.TrackClipAudio)[0]
	if vo.DurationUS != 6_000_000 {
		t.Fatalf("voiceover window must follow the real narration duration, got %dus", vo.DurationUS)
	}
	if clip.GainDB != audio.DuckClipBaseGainDB {
		t.Fatalf("clip base gain = %v, want %.1f dB", clip.GainDB, audio.DuckClipBaseGainDB)
	}
	if len(plan.Automation) != 1 {
		t.Fatalf("automation = %d, want exactly one ducking entry", len(plan.Automation))
	}
	a := plan.Automation[0]
	if a.StartUS != 0 || a.EndUS != 6_000_000 || a.GainDB != audio.DuckClipActiveGainDB {
		t.Fatalf("duck window must follow the real VO duration (0..6s), got %+v", a)
	}
}

// ─── Real Rust/FFmpeg execution gates ────────────────────────────────────────

func resolveMusclesBinary() string {
	if fromEnv := os.Getenv("VELOX_RUST_MUSCLES_PATH"); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err == nil && info.Mode().IsRegular() {
			return fromEnv
		}
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	root := filepath.Join(filepath.Dir(source), "..", "..", "..", "..")
	for _, candidate := range []string{
		filepath.Join(root, "bin", "pipelinegen-muscles"),
		filepath.Join(root, "rust", "target", "release", "pipelinegen-muscles"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

func resolveFFmpegBinary() string {
	if fromEnv := os.Getenv("FFMPEG_PATH"); fromEnv != "" {
		if info, err := os.Stat(fromEnv); err == nil && info.Mode().IsRegular() {
			return fromEnv
		}
	}
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return ""
	}
	return path
}

func resolveFFprobeBinary(ffmpeg string) string {
	if candidate := filepath.Join(filepath.Dir(ffmpeg), "ffprobe"); candidate != filepath.Join("", "ffprobe") {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	path, err := exec.LookPath("ffprobe")
	if err != nil {
		return ""
	}
	return path
}

func generateToneAsset(t *testing.T, ffmpeg, path string, frequency, durationSec float64) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=sin(2*PI*%.0f*t):s=48000", frequency),
		"-t", fmt.Sprintf("%.3f", durationSec), "-ac", "1", "-c:a", "pcm_s16le", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate tone %s: %v: %s", path, err, out)
	}
}

func generateSilenceAsset(t *testing.T, ffmpeg, path string, durationSec float64) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono",
		"-t", fmt.Sprintf("%.3f", durationSec), "-ac", "1", "-c:a", "pcm_s16le", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate silence %s: %v: %s", path, err, out)
	}
}

func probeDurationUS(t *testing.T, ffprobe, path string) int64 {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe duration %s: %v: %s", path, err, out)
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || sec <= 0 {
		t.Fatalf("probe duration %s: invalid value %q", path, string(out))
	}
	return int64(math.Round(sec * 1_000_000))
}

func parseVolumeStat(t *testing.T, output []byte, key string) float64 {
	t.Helper()
	for _, raw := range strings.Split(string(output), "\n") {
		fields := strings.Fields(raw)
		for i, token := range fields {
			if token != key+":" {
				continue
			}
			if i+2 >= len(fields) || fields[i+2] != "dB" {
				t.Fatalf("volumedetect output malformed: %q", raw)
			}
			value, err := strconv.ParseFloat(fields[i+1], 64)
			if err != nil {
				t.Fatalf("volumedetect output malformed: %q", raw)
			}
			return value
		}
	}
	t.Fatalf("volumedetect produced no %s line: %s", key, output)
	return 0
}

func parseMaxVolume(t *testing.T, output []byte) float64 {
	t.Helper()
	return parseVolumeStat(t, output, "max_volume")
}

func parseMeanVolume(t *testing.T, output []byte) float64 {
	t.Helper()
	return parseVolumeStat(t, output, "mean_volume")
}

func runVolumeStat(t *testing.T, ffmpeg, path, filter, key string) float64 {
	t.Helper()
	if filter == "" {
		filter = "volumedetect"
	} else {
		filter += ",volumedetect"
	}
	cmd := exec.Command(ffmpeg, "-hide_banner", "-i", path, "-af", filter, "-f", "null", "-")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("volumedetect %s: %v: %s", path, err, out)
	}
	return parseVolumeStat(t, out, key)
}

func runVolumeDetect(t *testing.T, ffmpeg, path, filter string) float64 {
	t.Helper()
	return runVolumeStat(t, ffmpeg, path, filter, "max_volume")
}

func runMeanVolumeDetect(t *testing.T, ffmpeg, path, filter string) float64 {
	t.Helper()
	return runVolumeStat(t, ffmpeg, path, filter, "mean_volume")
}

// bandMaxVolumeDB measures the peak energy of the master inside a time window
// restricted to a frequency band, so a tone's presence (and absence) can be
// certified objectively instead of trusting that a file simply exists.
func bandMaxVolumeDB(t *testing.T, ffmpeg, path string, startSec, endSec, lowHz, highHz float64) float64 {
	t.Helper()
	filter := fmt.Sprintf("atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS,highpass=f=%.0f,lowpass=f=%.0f", startSec, endSec, lowHz, highHz)
	return runVolumeDetect(t, ffmpeg, path, filter)
}

// bandMeanVolumeDB measures the average energy of the master inside a time
// window restricted to a frequency band. Absence assertions must use the mean,
// not the peak: the highpass/lowpass biquads ring at the atrim boundary and
// their startup transient shows up in max_volume even when the band is
// genuinely silent, so max_volume would report a phantom "tone present".
func bandMeanVolumeDB(t *testing.T, ffmpeg, path string, startSec, endSec, lowHz, highHz float64) float64 {
	t.Helper()
	filter := fmt.Sprintf("atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS,highpass=f=%.0f,lowpass=f=%.0f", startSec, endSec, lowHz, highHz)
	return runMeanVolumeDetect(t, ffmpeg, path, filter)
}

func newRealExecutor(t *testing.T, musclesPath, ffmpegPath string) *Executor {
	t.Helper()
	executor := NewExecutor(musclesPath, ffmpegPath, nil)
	if runner, ok := executor.runner.(*persistentRustProcessRunner); ok {
		t.Cleanup(runner.reset)
	}
	return executor
}

func renderRealAudio(t *testing.T, musclesPath, ffmpegPath string, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets) string {
	t.Helper()
	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	processor := &VideoProcessor{client: NewClientWithExecutor(executor, nil)}
	combinedAudio, err := NewCombinedAudioRenderer(processor)
	if err != nil {
		t.Fatal(err)
	}
	finalAudio, metrics, err := combinedAudio.Render(context.Background(), plan, assets)
	if err != nil {
		t.Fatalf("real render_audio_plan failed: %v", err)
	}
	if metrics.AudioEncodePasses != 1 || metrics.MixMS <= 0 || metrics.AACEncodeMS <= 0 {
		t.Fatalf("real render must encode once with measured stage timings: %+v", metrics)
	}
	if err := audio.ValidateFinalAudio(audio.FinalAudioAsset{
		AssetID:              finalAudio.AssetID,
		AudioContractVersion: finalAudio.AudioContractVersion,
		AudioPlanVersion:     finalAudio.AudioPlanVersion,
		AudioPlanSHA256:      finalAudio.PlanSHA256,
		FinalAudioSHA256:     finalAudio.FinalAudioSHA256,
		Codec:                finalAudio.Codec,
		Profile:              finalAudio.Profile,
		SampleRate:           finalAudio.SampleRate,
		Channels:             finalAudio.Channels,
		ChannelLayout:        finalAudio.ChannelLayout,
		Bitrate:              finalAudio.Bitrate,
		DurationMS:           finalAudio.DurationMS,
		StartPTS:             finalAudio.StartPTS,
		SizeBytes:            finalAudio.SizeBytes,
		FinalMix:             finalAudio.FinalMix,
		CopyEligible:         finalAudio.CopyEligible,
	}, plan); err != nil {
		t.Fatalf("real final audio certification failed: %v", err)
	}
	return finalAudio.Path
}

// TestVoiceoverRealFFmpegMix runs the real Rust/FFmpeg render of one scene
// whose clip tone (440Hz) lasts 10s and whose voiceover tone (1000Hz) lasts
// only 6s. Frequency analysis on the mastered file proves the voiceover is
// genuinely inside final_audio.m4a while its narration is active and gone
// afterwards — not merely present in the plan JSON.
func TestVoiceoverRealFFmpegMix(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)
	if ffprobePath == "" {
		t.Skip("ffprobe binary not found")
	}

	assetsDir := t.TempDir()
	clipPath := filepath.Join(assetsDir, "clip-long.wav")
	voPath := filepath.Join(assetsDir, "vo-short.wav")
	generateToneAsset(t, ffmpegPath, clipPath, 440, 10)
	generateToneAsset(t, ffmpegPath, voPath, 1000, 6)
	clipDuration := probeDurationUS(t, ffprobePath, clipPath)
	voDuration := probeDurationUS(t, ffprobePath, voPath)
	if clipDuration < 9_900_000 || voDuration < 5_900_000 {
		t.Fatalf("unexpected generated asset durations: clip=%dus vo=%dus", clipDuration, voDuration)
	}

	specs := []voiceoverSceneSpec{{
		index: 0, clipID: "clip-long", voiceoverID: "vo-short",
		timelineStart: 0, durationUS: 10_000_000, voDurationUS: voDuration, clipSourceUS: clipDuration,
	}}
	plan := compileDuckedVoiceoverPlan(t, buildVoiceoverTimeline(specs))

	finalAudioPath := renderRealAudio(t, musclesPath, ffmpegPath, plan, audio.ResolvedAudioAssets{
		{AssetID: "clip-long", Path: clipPath},
		{AssetID: "vo-short", Path: voPath},
	})

	// 0..6s: both the ducked clip tone and the unity voiceover tone are present.
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 0, 6, 300, 700); got <= -30 {
		t.Fatalf("clip tone (440Hz) missing in the first 6s: %.2f dB", got)
	}
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 0, 6, 800, 1200); got <= -30 {
		t.Fatalf("voiceover tone (1000Hz) missing in the first 6s: %.2f dB", got)
	}
	// 6..10s: the clip tone continues, but the voiceover must be gone.
	if got := bandMaxVolumeDB(t, ffmpegPath, finalAudioPath, 6, 10, 300, 700); got <= -30 {
		t.Fatalf("clip tone (440Hz) missing in the last 4s: %.2f dB", got)
	}
	if got := bandMeanVolumeDB(t, ffmpegPath, finalAudioPath, 6, 10, 800, 1200); got >= -25 {
		t.Fatalf("voiceover tone (1000Hz) must be absent after its 6s narration: mean %.2f dB", got)
	}
}

// TestVoiceoverZeroDBRendersLoudNotMuted is the volumedetect-based regression
// guard for the dB→linear boundary: a voiceover at unity (GainDB=0) must
// render at FFmpeg volume=1.0 and come out loud, never flattened to
// volume=0. A single VO-only scene is mixed through the real Rust/FFmpeg
// renderer and the mastered file's peak is measured directly.
func TestVoiceoverZeroDBRendersLoudNotMuted(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)
	if ffprobePath == "" {
		t.Skip("ffprobe binary not found")
	}

	assetsDir := t.TempDir()
	voPath := filepath.Join(assetsDir, "vo-zero.wav")
	generateToneAsset(t, ffmpegPath, voPath, 1000, 2.0)
	voDuration := probeDurationUS(t, ffprobePath, voPath)
	if voDuration < 1_900_000 {
		t.Fatalf("unexpected voiceover asset duration: %dus", voDuration)
	}

	specs := []voiceoverSceneSpec{{
		index: 0, clipID: "clip-zero", voiceoverID: "vo-zero",
		timelineStart: 0, durationUS: voDuration, voDurationUS: voDuration, clipSourceUS: voDuration,
	}}
	plan := compileVoiceoverOnlyPlan(t, buildVoiceoverTimeline(specs))

	voEvents := trackEvents(plan, audio.TrackVoiceover)
	if len(voEvents) != 1 {
		t.Fatalf("voiceover cardinality = %d, want 1", len(voEvents))
	}
	if voEvents[0].GainDB != 0 {
		t.Fatalf("voiceover must stay at unity (0 dB): %+v", voEvents[0])
	}
	if len(trackEvents(plan, audio.TrackClipAudio)) != 0 {
		t.Fatalf("VOICEOVER_ONLY plan must drop clip audio")
	}

	finalAudioPath := renderRealAudio(t, musclesPath, ffmpegPath, plan, audio.ResolvedAudioAssets{
		{AssetID: "vo-zero", Path: voPath},
	})

	maxVolume := runVolumeDetect(t, ffmpegPath, finalAudioPath, "")
	if maxVolume <= -3.0 {
		t.Fatalf("0 dB voiceover rendered muted (max_volume=%.2f dB): the dB→linear conversion must map 0 dB to volume=1.0, never volume=0", maxVolume)
	}
}

type recordingAudioRunner struct {
	binary string
	inner  RustProcessRunner
	mu     sync.Mutex
	input  []byte
}

func (r *recordingAudioRunner) Run(ctx context.Context, _ string, input []byte, outputLimit int64) ([]byte, []byte, error) {
	r.mu.Lock()
	r.input = append(r.input, input...)
	r.mu.Unlock()
	return r.inner.Run(ctx, r.binary, input, outputLimit)
}

func assertRealWireAssets(t *testing.T, input []byte, specs []voiceoverSceneSpec) {
	t.Helper()
	var req request
	if err := json.Unmarshal(input, &req); err != nil {
		t.Fatalf("decode recorded wire request: %v", err)
	}
	if req.Operation != OperationRenderAudioPlan || len(req.AudioAssets) != 2*len(specs) {
		t.Fatalf("wire request drift: op=%s assets=%d", req.Operation, len(req.AudioAssets))
	}
	ids := map[string]struct{}{}
	for _, asset := range req.AudioAssets {
		if asset.AssetID == "" || asset.Path == "" {
			t.Fatalf("wire asset unresolved: %+v", asset)
		}
		info, err := os.Stat(asset.Path)
		if err != nil || info.Size() <= 0 {
			t.Fatalf("wire asset %q is not an existing file: %v", asset.Path, err)
		}
		if _, dup := ids[asset.AssetID]; dup {
			t.Fatalf("duplicate wire asset %q", asset.AssetID)
		}
		ids[asset.AssetID] = struct{}{}
	}
	if len(ids) != 2*len(specs) {
		t.Fatalf("unique wire assets = %d, want %d", len(ids), 2*len(specs))
	}
	for _, spec := range specs {
		if _, ok := ids[spec.voiceoverID]; !ok {
			t.Fatalf("wire request missing voiceover asset %q", spec.voiceoverID)
		}
		if _, ok := ids[spec.clipID]; !ok {
			t.Fatalf("wire request missing clip asset %q", spec.clipID)
		}
	}
}

// TestComediansRealAudioRenderCertification is the real 10-scene comedians
// job: every scene carries one unique voiceover and one clip, the compiled
// plan is ducked, all 20 resolved assets reach the Rust process, and the
// mastered file is certified loud (the voiceover is audible, so 0 dB cannot
// have been flattened to volume=0).
func TestComediansRealAudioRenderCertification(t *testing.T) {
	musclesPath := resolveMusclesBinary()
	if musclesPath == "" {
		t.Skip("pipelinegen-muscles binary not found (set VELOX_RUST_MUSCLES_PATH or build via make build-muscles)")
	}
	ffmpegPath := resolveFFmpegBinary()
	if ffmpegPath == "" {
		t.Skip("ffmpeg binary not found")
	}
	ffprobePath := resolveFFprobeBinary(ffmpegPath)
	if ffprobePath == "" {
		t.Skip("ffprobe binary not found")
	}

	assetsDir := t.TempDir()
	specs := comediansTenScenes()
	if len(specs) != 10 {
		t.Fatalf("comedians scene count = %d, want 10", len(specs))
	}
	assets := make(audio.ResolvedAudioAssets, 0, 2*len(specs))
	voDurations := make(map[string]int64, len(specs))
	for i := range specs {
		voPath := filepath.Join(assetsDir, specs[i].voiceoverID+".wav")
		generateToneAsset(t, ffmpegPath, voPath, 440, 1.0)
		voDurations[specs[i].voiceoverID] = probeDurationUS(t, ffprobePath, voPath)
		specs[i].voDurationUS = voDurations[specs[i].voiceoverID]
		assets = append(assets, audio.ResolvedAudioAsset{AssetID: specs[i].voiceoverID, Path: voPath})

		clipPath := filepath.Join(assetsDir, specs[i].clipID+".wav")
		generateSilenceAsset(t, ffmpegPath, clipPath, 1.0)
		specs[i].clipSourceUS = probeDurationUS(t, ffprobePath, clipPath)
		assets = append(assets, audio.ResolvedAudioAsset{AssetID: specs[i].clipID, Path: clipPath})
	}

	timeline := buildVoiceoverTimeline(specs)
	if err := timeline.Validate(); err != nil {
		t.Fatalf("real comedians timeline invalid: %v", err)
	}
	plan := compileDuckedVoiceoverPlan(t, timeline)

	voEvents := trackEvents(plan, audio.TrackVoiceover)
	clipEvents := trackEvents(plan, audio.TrackClipAudio)
	if len(voEvents) != len(specs) {
		t.Fatalf("voiceover cardinality = %d, want %d", len(voEvents), len(specs))
	}
	if len(clipEvents) != len(specs) {
		t.Fatalf("clip audio cardinality = %d, want %d", len(clipEvents), len(specs))
	}
	uniqueVoiceover := map[string]struct{}{}
	for _, ev := range voEvents {
		if ev.AssetID == "" {
			t.Fatalf("voiceover event without asset: %+v", ev)
		}
		if _, dup := uniqueVoiceover[ev.AssetID]; dup {
			t.Fatalf("voiceover asset %q reused across scenes", ev.AssetID)
		}
		uniqueVoiceover[ev.AssetID] = struct{}{}
	}
	uniqueClip := map[string]struct{}{}
	for _, ev := range clipEvents {
		if ev.AssetID == "" {
			t.Fatalf("clip audio event without asset: %+v", ev)
		}
		if _, dup := uniqueClip[ev.AssetID]; dup {
			t.Fatalf("clip asset %q reused across scenes", ev.AssetID)
		}
		uniqueClip[ev.AssetID] = struct{}{}
	}
	if len(uniqueVoiceover) != len(specs) {
		t.Fatalf("unique voiceover IDs = %d, want %d", len(uniqueVoiceover), len(specs))
	}
	if len(uniqueClip) != len(specs) {
		t.Fatalf("unique clip IDs = %d, want %d", len(uniqueClip), len(specs))
	}
	for i, spec := range specs {
		ev := voEvents[i]
		if ev.TimelineStartUS != spec.timelineStart || ev.SourceInUS != 0 || ev.SourceDurationUS != voDurations[spec.voiceoverID] {
			t.Fatalf("voiceover event[%d] placement drift: got=%+v want start=%d real_duration=%d", i, ev, spec.timelineStart, voDurations[spec.voiceoverID])
		}
	}
	if plan.MixPolicy != audio.MixVoiceoverWithDuckedClip || len(plan.Automation) != len(specs) {
		t.Fatalf("ducked plan contract broken: policy=%s automation=%d", plan.MixPolicy, len(plan.Automation))
	}

	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	recorder := &recordingAudioRunner{binary: musclesPath, inner: newPersistentRustProcessRunner()}
	executor.runner = recorder
	// This test decorates the runner directly; bypass the executor pool so
	// the recording seam observes the request instead of a pooled runner.
	executor.runnerPool = nil
	processor := &VideoProcessor{client: NewClientWithExecutor(executor, nil)}
	combinedAudio, err := NewCombinedAudioRenderer(processor)
	if err != nil {
		t.Fatal(err)
	}
	finalAudio, metrics, err := combinedAudio.Render(context.Background(), plan, assets)
	if err != nil {
		t.Fatalf("real comedians render failed: %v", err)
	}
	if metrics.AudioEncodePasses != 1 || metrics.MixMS <= 0 || metrics.AACEncodeMS <= 0 {
		t.Fatalf("real render must encode once with measured stage timings: %+v", metrics)
	}
	if err := audio.ValidateFinalAudio(audio.FinalAudioAsset{
		AssetID:              finalAudio.AssetID,
		AudioContractVersion: finalAudio.AudioContractVersion,
		AudioPlanVersion:     finalAudio.AudioPlanVersion,
		AudioPlanSHA256:      finalAudio.PlanSHA256,
		FinalAudioSHA256:     finalAudio.FinalAudioSHA256,
		Codec:                finalAudio.Codec,
		Profile:              finalAudio.Profile,
		SampleRate:           finalAudio.SampleRate,
		Channels:             finalAudio.Channels,
		ChannelLayout:        finalAudio.ChannelLayout,
		Bitrate:              finalAudio.Bitrate,
		DurationMS:           finalAudio.DurationMS,
		StartPTS:             finalAudio.StartPTS,
		SizeBytes:            finalAudio.SizeBytes,
		FinalMix:             finalAudio.FinalMix,
		CopyEligible:         finalAudio.CopyEligible,
	}, plan); err != nil {
		t.Fatalf("real final audio certification failed: %v", err)
	}
	assertRealWireAssets(t, recorder.input, specs)

	maxVolume := runVolumeDetect(t, ffmpegPath, finalAudio.Path, "")
	if maxVolume <= -30.0 {
		t.Fatalf("voiceover track is silent in the real mix (max_volume=%.2f dB): the dB→linear gain conversion regressed", maxVolume)
	}
}
