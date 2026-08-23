package rustexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// TestStockRustFinalAudioCopyNoReencode drives the REAL Rust render_stock →
// mux_audio_copy sequence with a certified AAC-LC master and proves the audio
// is packet-copied (never re-encoded) by comparing the SHA256 of the AAC
// bitstream extracted from the source M4A and from the final MP4.
//
// Sequence under test:
//
//	render_stock  → final.mp4.video.mp4  (video only, KeepAudio=false)
//	mux_audio_copy → final.mp4            (-c:a copy, no encode fallback)
func TestStockRustFinalAudioCopyNoReencode(t *testing.T) {
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

	const (
		width  = 1280
		height = 720
		fps    = 30
		secs   = 10
		frames = secs * fps // 300
	)

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "clip.mp4")
	audioPath := filepath.Join(dir, "final_audio.m4a")
	generateTestsrcClip(t, ffmpegPath, videoPath, frames, width, height, fps)
	generateAACM4A(t, ffmpegPath, audioPath, float64(secs))

	// Certify the final audio asset with its real on-disk identity.
	size, sum, err := hashOutput(audioPath)
	if err != nil {
		t.Fatalf("hash final audio: %v", err)
	}

	// Build the sealed canonical plan with the certified final audio asset.
	clipBytes, err := os.ReadFile(videoPath)
	if err != nil {
		t.Fatal(err)
	}
	clipSum := sha256.Sum256(clipBytes)
	timeline := audio.CanonicalTimeline{
		Version:    audio.TimelineVersion,
		DurationUS: int64(secs) * 1_000_000,
		Segments: []audio.TimelineSegment{{
			ID: "scene", Index: 0, TimelineStartUS: 0, DurationUS: int64(secs) * 1_000_000,
			Video: audio.VideoSegment{AssetID: "clip", SourceInUS: 0, SourceDurationUS: int64(secs) * 1_000_000},
			Audio: audio.AudioIntent{Mode: audio.AudioSilence},
		}},
	}
	outputPath := filepath.Join(dir, "final.mp4")
	plan, err := render.Compile(render.CompileInput{
		JobID: "stockrust-final-audio-copy", Revision: "generation.v1",
		OutputPath: outputPath, FrameRate: audio.IntegerFrameRate(fps), Timeline: timeline,
		FinalAudio: &render.FinalAudioAsset{
			AssetID: "final-audio", AssetKind: "final_audio", Strategy: string(audio.FinalAudioCopy),
			Path: audioPath, SHA256: sum, PlanSHA256: strings.Repeat("a", 64),
			AudioContractVersion: audio.AudioContractVersion, AudioPlanVersion: audio.AudioPlanVersion,
			Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2, ChannelLayout: "stereo",
			DurationMS: int64(secs) * 1000, StartPTS: 0, SizeBytes: size, FinalMix: true, CopyEligible: true,
		},
		Manifest: []render.AssetManifestEntry{{AssetID: "clip", Path: videoPath, SHA256: hex.EncodeToString(clipSum[:]), FrameCount: frames}},
	})
	if err != nil {
		t.Fatal(err)
	}
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatalf("validate canonical render plan: %v", err)
	}

	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	stock := &StockRenderer{
		client: NewClientWithExecutor(executor, nil),
		policy: mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
		profile: mediaexec.VideoProfile{
			Width: width, Height: height, FPSNum: fps, FPSDen: 1, KeyframeInterval: 60,
			AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
		},
	}
	if err := stock.RenderCanonicalPlan(context.Background(), validated); err != nil {
		t.Fatalf("stockrust render + final audio copy failed: %v", err)
	}

	// The intermediate video-only file must be removed after the mux.
	if _, err := os.Stat(outputPath + ".video.mp4"); err == nil {
		t.Fatalf("intermediate video %s.video.mp4 was not removed", outputPath)
	}

	info, err := os.Stat(outputPath)
	if err != nil || info.Size() == 0 {
		t.Fatalf("final output missing or empty: %v", err)
	}

	// Container sanity: exactly one h264 video stream + one aac-LC audio stream.
	streams := ffprobeStreams(t, ffprobePath, outputPath)
	if len(streams) != 2 {
		t.Fatalf("expected 2 streams (video + audio), got %d", len(streams))
	}
	var videoFound, audioFound bool
	for _, stream := range streams {
		switch stream.CodecType {
		case "video":
			videoFound = true
			if stream.CodecName != "h264" || stream.Width != width || stream.Height != height || stream.RFrameRate != "30/1" {
				t.Fatalf("unexpected video stream: %+v", stream)
			}
		case "audio":
			audioFound = true
			if stream.CodecName != "aac" || !strings.EqualFold(stream.Profile, "LC") || stream.SampleRate != "48000" || stream.Channels != 2 {
				t.Fatalf("unexpected audio stream: %+v", stream)
			}
		}
	}
	if !videoFound || !audioFound {
		t.Fatalf("missing expected video/audio streams: %+v", streams)
	}

	// The decisive assertion: extract the AAC elementary stream from the
	// source master and from the muxed MP4 and compare hashes. A packet copy
	// yields identical bitstreams; any re-encode changes the SHA256.
	originalAAC := filepath.Join(dir, "original.aac")
	muxedAAC := filepath.Join(dir, "muxed.aac")
	extractAACBitstream(t, ffmpegPath, audioPath, originalAAC)
	extractAACBitstream(t, ffmpegPath, outputPath, muxedAAC)
	originalHash := fileSHA256Hex(t, originalAAC)
	muxedHash := fileSHA256Hex(t, muxedAAC)
	if originalHash != muxedHash {
		t.Fatalf("AAC bitstream changed across the mux (audio was re-encoded):\n  original=%s\n  muxed=%s", originalHash, muxedHash)
	}

	// The final MP4 must decode fully with zero ffmpeg errors.
	if decodeErrors := fullDecode(t, ffmpegPath, outputPath); decodeErrors != "" {
		t.Fatalf("final output decode produced errors:\n%s", decodeErrors)
	}
}

type streamProbe struct {
	CodecType  string `json:"codec_type"`
	CodecName  string `json:"codec_name"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	RFrameRate string `json:"r_frame_rate"`
	Profile    string `json:"profile"`
	SampleRate string `json:"sample_rate"`
	Channels   int    `json:"channels"`
}

func ffprobeStreams(t *testing.T, ffprobe, path string) []streamProbe {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error",
		"-show_entries", "stream=codec_type,codec_name,width,height,r_frame_rate,profile,sample_rate,channels",
		"-of", "json", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe streams %s: %v: %s", path, err, out)
	}
	var report struct {
		Streams []streamProbe `json:"streams"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("parse ffprobe streams %s: %v", path, err)
	}
	return report.Streams
}

// extractAACBitstream demuxes the AAC elementary stream into an ADTS file so
// two containers carrying the same packet-copied audio hash identically.
func extractAACBitstream(t *testing.T, ffmpeg, input, output string) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-i", input, "-map", "0:a:0", "-c:a", "copy", "-f", "adts", output)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extract AAC from %s: %v: %s", input, err, out)
	}
}

func fileSHA256Hex(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
