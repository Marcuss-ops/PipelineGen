// Package rustexec — audio_gate_helpers_e2e_test.go holds the shared
// machinery of the listen-gate tests: real render with stage metrics,
// persistent masters under out/audio-gate/ for manual listening, master
// format certification, and timing logs.
package rustexec

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scripts "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// gateRender is the outcome of one listen-gate render: the persisted master
// (for manual listening), its content hash, the certified final-audio
// reference and the per-stage timing metrics.
type gateRender struct {
	Path    string
	SHA256  string
	Final   scripts.FinalAudioReference
	Metrics scripts.AudioPipelineMetrics
}

// gateRepoRoot resolves the repository root from this file's location.
func gateRepoRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the repository root for the gate output")
	}
	return filepath.Join(filepath.Dir(source), "..", "..", "..", "..")
}

// gateOutputDir creates out/audio-gate/<name> under the repository root so
// the mastered files survive the test run and can be listened to manually.
func gateOutputDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(gateRepoRoot(t), "out", "audio-gate", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create gate output dir %s: %v", dir, err)
	}
	return dir
}

// fileSHA256 hashes a file's full content.
func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// renderGateAudio renders the sealed plan through the real Rust/FFmpeg
// renderer exactly like renderRealAudio, then copies the certified master
// into out/audio-gate/<name>/final_audio.m4a (hash-verified) so a human can
// listen to it afterwards.
func renderGateAudio(t *testing.T, musclesPath, ffmpegPath string, plan audio.CompiledAudioPlan, assets audio.ResolvedAudioAssets, gateName string) gateRender {
	t.Helper()
	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	processor := &VideoProcessor{client: NewClientWithExecutor(executor, nil)}
	combinedAudio, err := NewCombinedAudioRenderer(processor)
	if err != nil {
		t.Fatal(err)
	}
	final, metrics, err := combinedAudio.Render(context.Background(), plan, assets)
	if err != nil {
		t.Fatalf("real render_audio_plan failed: %v", err)
	}
	if metrics.AudioEncodePasses != 1 || metrics.MixMS <= 0 || metrics.AACEncodeMS <= 0 {
		t.Fatalf("real render must encode once with measured stage timings: %+v", metrics)
	}
	if err := audio.ValidateFinalAudio(audio.FinalAudioAsset{
		AssetID:              final.AssetID,
		AudioContractVersion: final.AudioContractVersion,
		AudioPlanVersion:     final.AudioPlanVersion,
		AudioPlanSHA256:      final.PlanSHA256,
		FinalAudioSHA256:     final.FinalAudioSHA256,
		Codec:                final.Codec,
		Profile:              final.Profile,
		SampleRate:           final.SampleRate,
		Channels:             final.Channels,
		ChannelLayout:        final.ChannelLayout,
		Bitrate:              final.Bitrate,
		DurationMS:           final.DurationMS,
		StartPTS:             final.StartPTS,
		SizeBytes:            final.SizeBytes,
		FinalMix:             final.FinalMix,
		CopyEligible:         final.CopyEligible,
	}, plan); err != nil {
		t.Fatalf("real final audio certification failed: %v", err)
	}
	sum := fileSHA256(t, final.Path)
	dest := filepath.Join(gateOutputDir(t, gateName), "final_audio.m4a")
	data, err := os.ReadFile(final.Path)
	if err != nil {
		t.Fatalf("read rendered master %s: %v", final.Path, err)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		t.Fatalf("persist gate master %s: %v", dest, err)
	}
	if got := fileSHA256(t, dest); got != sum {
		t.Fatalf("persisted gate master hash mismatch: %s vs %s", got, sum)
	}
	return gateRender{Path: dest, SHA256: sum, Final: final, Metrics: metrics}
}

// certifyGateMaster probes the mastered file: present stream, AAC-LC,
// 48kHz stereo, duration within ±100ms of the timeline, non-empty file.
func certifyGateMaster(t *testing.T, ffprobe, path string, wantDurationUS int64) {
	t.Helper()
	dur := probeDurationUS(t, ffprobe, path)
	if dur < wantDurationUS-100_000 || dur > wantDurationUS+100_000 {
		t.Fatalf("master duration = %dus, want ~%dus", dur, wantDurationUS)
	}
	if got := probeAudioStreamField(t, ffprobe, path, "codec_name"); got != "aac" {
		t.Fatalf("master codec = %q, want aac", got)
	}
	if got := probeAudioStreamField(t, ffprobe, path, "profile"); got != "LC" {
		t.Fatalf("master profile = %q, want LC", got)
	}
	if got := probeAudioStreamField(t, ffprobe, path, "sample_rate"); got != "48000" {
		t.Fatalf("master sample rate = %q, want 48000", got)
	}
	if got := probeAudioStreamField(t, ffprobe, path, "channels"); got != "2" {
		t.Fatalf("master channels = %q, want 2 (stereo)", got)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		t.Fatalf("master file %s is empty or unavailable", path)
	}
}

// logGateTiming prints the compile/render stage split of one run so the
// cost of the audio pipeline can be compared across gates.
func logGateTiming(t *testing.T, label string, compileMS int64, render gateRender) {
	t.Helper()
	t.Logf("[timing:%s] compile_ms=%d mix_ms=%d aac_encode_ms=%d probe_ms=%d hash_ms=%d total_ms=%d sha256=%s",
		label, compileMS, render.Metrics.MixMS, render.Metrics.AACEncodeMS,
		render.Metrics.ProbeMS, render.Metrics.HashMS, render.Metrics.TotalMS, render.SHA256)
}
