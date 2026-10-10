package rustexec

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

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
// restricted to a frequency band. Formerly in the deleted voiceover
// certification; kept because audio_gate_*_e2e tests still use it.
func bandMaxVolumeDB(t *testing.T, ffmpeg, path string, startSec, endSec, lowHz, highHz float64) float64 {
	t.Helper()
	filter := fmt.Sprintf("atrim=start=%.3f:end=%.3f,asetpts=PTS-STARTPTS,highpass=f=%.0f,lowpass=f=%.0f", startSec, endSec, lowHz, highHz)
	return runVolumeDetect(t, ffmpeg, path, filter)
}

// bandMeanVolumeDB measures the average energy of the master inside a time
// window restricted to a frequency band. Same origin as above.
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
	if metrics.AudioEncodePasses != 1 || metrics.MixMS != 0 || metrics.AACEncodeMS <= 0 || metrics.ProbeMS <= 0 || metrics.HashMS <= 0 {
		t.Fatalf("real render must encode once with measured encode/probe/hash timings: %+v", metrics)
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

// resolveMusclesBinary locates the pipelinegen-muscles binary for e2e tests.
// Formerly owned by the deleted comedians_voiceover_certification_test.go;
// kept because several *_e2e_test.go files still use it.
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

// resolveFFmpegBinary locates ffmpeg for e2e tests. Same origin as above.
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

// generateToneAsset synthesizes a sine-wave WAV via ffmpeg. Same origin as above.
func generateToneAsset(t *testing.T, ffmpeg, path string, frequency, durationSec float64) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("aevalsrc=sin(2*PI*%.0f*t):s=48000", frequency),
		"-t", fmt.Sprintf("%.3f", durationSec), "-ac", "1", "-c:a", "pcm_s16le", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate tone %s: %v: %s", path, err, out)
	}
}

// probeDurationUS reads media duration in microseconds via ffprobe. Same origin.
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

// trackEvents collects all events for one track role from a compiled plan.
// Formerly in the deleted comedians_full_audio e2e certification.
func trackEvents(plan audio.CompiledAudioPlan, role audio.AudioTrackRole) []audio.AudioEvent {
	var events []audio.AudioEvent
	for _, track := range plan.Tracks {
		if track.Role == role {
			events = append(events, track.Events...)
		}
	}
	return events
}

// mapAudioAssetSource is the in-test AudioAssetSource: asset_id → verified
// path + certified duration. Formerly in the deleted bgm_loop_fundamental
// e2e certification; kept because audio_gate_*_e2e tests still use it.
type mapAudioAssetSource struct {
	assets map[string]audio.ResolvedAudioAsset
}

func (s mapAudioAssetSource) ResolveAudioAsset(_ context.Context, assetID string) (audio.ResolvedAudioAsset, error) {
	asset, ok := s.assets[assetID]
	if !ok {
		return audio.ResolvedAudioAsset{}, fmt.Errorf("unknown audio asset %q", assetID)
	}
	return asset, nil
}

var _ scripts.AudioAssetSource = mapAudioAssetSource{}

// probeAudioStreamField reads one audio-stream field via ffprobe. Formerly in
// the deleted bgm_loop_fundamental e2e certification.
func probeAudioStreamField(t *testing.T, ffprobe, path, key string) string {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream="+key, "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe %s field %s: %v: %s", path, key, err, out)
	}
	return strings.TrimSpace(string(out))
}

// resolveFFprobeBinary locates ffprobe next to ffmpeg. Same origin as above.
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
