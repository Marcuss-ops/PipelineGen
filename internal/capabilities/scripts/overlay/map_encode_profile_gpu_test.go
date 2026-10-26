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

// TestMapLegacyGPUProfileExecutesOnGPU certifies the closed legacy NVENC
// profile's ffmpeg shaping against real hardware: the exact arguments
// MapEncodeVideoArgs emits must encode H.264 on this host's NVIDIA encoder.
// Hosts without NVENC skip — the profile is a hardware contract. It stays
// declarable and executable as WIRE CONTRACT for the future OpenCL-sampler
// port, while the native render boundary rejects it (demolition debt).
func TestMapLegacyGPUProfileExecutesOnGPU(t *testing.T) {
	ffmpeg, ffprobe, ok := ffmpegAvailable()
	if !ok {
		t.Skip("ffmpeg/ffprobe toolchain not installed")
	}
	if !hostOffersNVENC(t, ffmpeg) {
		t.Skip("host ffmpeg has no h264_nvenc; the legacy GPU profile needs NVIDIA hardware")
	}
	profile, err := ResolveMapEncodeProfile(MapLegacyGPUCodec)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Codec != MapLegacyGPUCodec || !profile.ExpectGPU {
		t.Fatalf("legacy GPU profile = %+v, want the closed NVENC contract", profile)
	}
	if err := RequireNativeServableProfile(profile); err == nil {
		t.Fatal("the legacy GPU profile must fail closed at the native render boundary")
	}
	out := executeProfile(t, ffmpeg, ffprobe, profile)
	t.Logf("legacy GPU profile shaping certified on host GPU: %s", out)
}

// TestMapCPUProfileExecutesWithoutGPU certifies the native renderer's
// production profile the same way: the declared libx264 arguments must encode
// H.264 with no GPU on any host carrying the ffmpeg toolchain, and the
// profile must be servable by the native render boundary.
func TestMapCPUProfileExecutesWithoutGPU(t *testing.T) {
	ffmpeg, ffprobe, ok := ffmpegAvailable()
	if !ok {
		t.Skip("ffmpeg/ffprobe toolchain not installed")
	}
	profile, err := ResolveMapEncodeProfile("libx264")
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireNativeServableProfile(profile); err != nil {
		t.Fatalf("the production CPU profile must be servable by the native renderer: %v", err)
	}
	out := executeProfile(t, ffmpeg, ffprobe, profile)
	t.Logf("CPU profile certified without GPU: %s", out)
}
