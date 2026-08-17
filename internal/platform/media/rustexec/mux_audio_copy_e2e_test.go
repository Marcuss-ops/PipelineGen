package rustexec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// These tests drive the real Rust `mux_audio_copy` executor against files
// produced by the real ffmpeg/ffprobe. They certify the fail-closed duration
// gate that replaced `-shortest`: a matching video+audio pair is stream-copied
// into an MP4 whose video and audio streams agree within ±40ms, and any
// divergence beyond tolerance aborts the mux instead of being truncated.

const muxE2EToleranceUS = 40_000 // ±40ms, matching FinalAudioDurationToleranceUS

// generateVideoMP4 renders a real h264 video (no audio) of the requested
// duration using a solid color source at a fixed frame rate.
func generateVideoMP4(t *testing.T, ffmpeg, path string, durationSec float64) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=blue:s=320x240:r=25",
		"-t", fmt.Sprintf("%.3f", durationSec),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate video %s: %v: %s", path, err, out)
	}
}

// generateAACM4A renders a real AAC-LC 48kHz stereo master of the requested
// duration. It mirrors the canonical final-audio output contract so the
// copy-only mux can be exercised end-to-end.
func generateAACM4A(t *testing.T, ffmpeg, path string, durationSec float64) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", fmt.Sprintf("%.3f", durationSec),
		"-c:a", "aac", "-profile:a", "aac_low", "-b:a", "128k", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate audio %s: %v: %s", path, err, out)
	}
}

// probeStreamDurationUS returns the duration of the first stream of the given
// type (v or a) inside a container, independent of the container duration.
func probeStreamDurationUS(t *testing.T, ffprobe, path, streamType string) int64 {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error", "-select_streams", streamType+":0",
		"-show_entries", "stream=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe %s stream duration %s: %v: %s", streamType, path, err, out)
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || sec <= 0 {
		t.Fatalf("probe %s stream duration %s: invalid value %q", streamType, path, string(out))
	}
	return int64(sec * 1_000_000)
}

// copyEligibleAsset builds the FinalAudioAsset contract for a real AAC master
// so MuxFinalAudioCopy accepts it as canonical copy-eligible media.
func copyEligibleAsset(t *testing.T, ffprobe, audioPath string) audio.FinalAudioAsset {
	t.Helper()
	size, sum, err := hashOutput(audioPath)
	if err != nil {
		t.Fatalf("hash final audio %s: %v", audioPath, err)
	}
	durationUS := probeDurationUS(t, ffprobe, audioPath)
	return audio.FinalAudioAsset{
		AssetID:          audioPath,
		FinalAudioSHA256: sum,
		Codec:            "aac",
		Profile:          "LC",
		SampleRate:       48000,
		Channels:         2,
		ChannelLayout:    "stereo",
		DurationMS:       durationUS / 1000,
		StartPTS:         0,
		Bitrate:          128000,
		SizeBytes:        size,
		FinalMix:         true,
		CopyEligible:     true,
	}
}

func TestMuxAudioCopyE2EVerifiesMatchingDurations(t *testing.T) {
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

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "video.mp4")
	audioPath := filepath.Join(dir, "final-audio.m4a")
	outputPath := filepath.Join(dir, "final.mp4")
	generateVideoMP4(t, ffmpegPath, videoPath, 5.000)
	generateAACM4A(t, ffmpegPath, audioPath, 5.000)

	videoDur := probeDurationUS(t, ffprobePath, videoPath)
	audioDur := probeDurationUS(t, ffprobePath, audioPath)
	if videoDur <= 0 || audioDur <= 0 {
		t.Fatalf("unexpected generated durations: video=%dus audio=%dus", videoDur, audioDur)
	}

	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	processor := &VideoProcessor{client: NewClientWithExecutor(executor, nil)}
	asset := copyEligibleAsset(t, ffprobePath, audioPath)
	if err := processor.MuxFinalAudioCopy(context.Background(), videoPath, audioPath, outputPath, asset); err != nil {
		t.Fatalf("FINAL_AUDIO_COPY mux failed for matching durations: %v", err)
	}

	info, err := os.Stat(outputPath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("mux produced no output: %v", err)
	}
	outVideo := probeStreamDurationUS(t, ffprobePath, outputPath, "v")
	outAudio := probeStreamDurationUS(t, ffprobePath, outputPath, "a")
	delta := outVideo - outAudio
	if delta < 0 {
		delta = -delta
	}
	if delta > muxE2EToleranceUS {
		t.Fatalf("muxed MP4 video/audio durations diverge: video=%dus audio=%dus delta=%dus (tolerance %dus)", outVideo, outAudio, delta, muxE2EToleranceUS)
	}
}

func TestMuxAudioCopyE2EFailsClosedOnDurationMismatch(t *testing.T) {
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

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "video.mp4")
	audioPath := filepath.Join(dir, "final-audio.m4a")
	outputPath := filepath.Join(dir, "final.mp4")
	generateVideoMP4(t, ffmpegPath, videoPath, 5.000)
	generateAACM4A(t, ffmpegPath, audioPath, 6.000)

	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	processor := &VideoProcessor{client: NewClientWithExecutor(executor, nil)}
	asset := copyEligibleAsset(t, ffprobePath, audioPath)
	err := processor.MuxFinalAudioCopy(context.Background(), videoPath, audioPath, outputPath, asset)
	if err == nil {
		t.Fatalf("mux must fail closed when audio outlasts video beyond tolerance")
	}
	if !strings.Contains(err.Error(), "FINAL_MUX_DURATION_MISMATCH") {
		t.Fatalf("mux failed with non-duration error: %v", err)
	}
	if _, statErr := os.Stat(outputPath); statErr == nil {
		t.Fatalf("mux must not publish an output on duration mismatch")
	}
}
