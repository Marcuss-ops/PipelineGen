package overlay

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ffmpegAvailable reports whether the ffmpeg/ffprobe toolchain is installed.
func ffmpegAvailable() (string, string, bool) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", "", false
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		return "", "", false
	}
	return ffmpeg, ffprobe, true
}

// hostOffersNVENC probes the local ffmpeg build for the NVENC H.264 encoder.
// CPU-only hosts return false so GPU execution tests skip instead of failing
// on hardware they were never promised.
func hostOffersNVENC(t *testing.T, ffmpeg string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Logf("ffmpeg -encoders probe failed: %v", err)
		return false
	}
	return strings.Contains(string(out), "h264_nvenc")
}

// executeProfile encodes one second of generated test pattern with the exact
// arguments the profile declares and returns the encoded file.
func executeProfile(t *testing.T, ffmpeg, ffprobe string, profile MapEncodeProfile) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out := filepath.Join(t.TempDir(), "profile-check.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=s=320x240:r=24:d=1"}
	args = append(args, MapEncodeVideoArgs(profile)...)
	args = append(args, "-pix_fmt", "yuv420p", out)
	if output, err := exec.CommandContext(ctx, ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("profile %+v failed to execute: %v: %s", profile, err, strings.TrimSpace(string(output)))
	}
	info, err := os.Stat(out)
	if err != nil || info.Size() == 0 {
		t.Fatalf("profile %+v produced no bytes: %v", profile, err)
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer probeCancel()
	codec, err := exec.CommandContext(probeCtx, ffprobe, "-hide_banner", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=codec_name",
		"-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatalf("probe encoded profile %+v: %v", profile, err)
	}
	if strings.TrimSpace(string(codec)) != "h264" {
		t.Fatalf("profile %+v encoded codec = %q, want h264", profile, strings.TrimSpace(string(codec)))
	}
	return out
}

// TestMapProductionProfileExecutesOnGPU certifies the declared production
// execution profile against real hardware: the exact ffmpeg arguments
// MapEncodeVideoArgs emits for the default profile must encode H.264 on this
// host's NVIDIA encoder. Hosts without NVENC skip — the profile is a
// production-hardware contract, and a CPU-only machine was never promised it.
func TestMapProductionProfileExecutesOnGPU(t *testing.T) {
	ffmpeg, ffprobe, ok := ffmpegAvailable()
	if !ok {
		t.Skip("ffmpeg/ffprobe toolchain not installed")
	}
	if !hostOffersNVENC(t, ffmpeg) {
		t.Skip("host ffmpeg has no h264_nvenc; production GPU profile needs NVIDIA hardware")
	}
	profile := DefaultMapEncodeProfile()
	if profile.Codec != MapProductionCodec || !profile.ExpectGPU {
		t.Fatalf("default profile = %+v, want production NVENC", profile)
	}
	out := executeProfile(t, ffmpeg, ffprobe, profile)
	t.Logf("production profile certified on host GPU: %s", out)
}

// TestMapCPUProfileExecutesWithoutGPU certifies the separately certified CPU
// path the same way: the declared libx264 arguments must encode H.264 with
// no GPU on any host carrying the ffmpeg toolchain.
func TestMapCPUProfileExecutesWithoutGPU(t *testing.T) {
	ffmpeg, ffprobe, ok := ffmpegAvailable()
	if !ok {
		t.Skip("ffmpeg/ffprobe toolchain not installed")
	}
	profile, err := ResolveMapEncodeProfile("libx264")
	if err != nil {
		t.Fatal(err)
	}
	out := executeProfile(t, ffmpeg, ffprobe, profile)
	t.Logf("CPU profile certified without GPU: %s", out)
}
