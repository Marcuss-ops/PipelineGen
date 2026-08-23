package rustexec

// clip_render_e2e_test.go — LIVE E2E for the render_clip operation (feature
// spec §6/§7/§9). Drives the REAL Rust binary against an ffmpeg-generated
// 16:9 source with audio: blur_source background, watermark overlay, burn
// subtitles (libass, CPU stage), audio copy_if_compatible (aac/48k/stereo
// → copied verbatim, zero encode passes). Certifies the output contract
// (1080x1920@60 h264 yuv420p), positive duration, expected audio stream,
// copy-policy outcome, CPU-subtitle honesty, and a clean full decode.
//
// Skips when the pipelinegen-muscles binary or ffmpeg/ffprobe are missing.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
)

func TestClipRenderE2E_BlurSourceWatermarkBurnCopyAudio(t *testing.T) {
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

	// 1. Synthetic 16:9 source WITH audio (aac/48k/stereo — satisfies the
	//    plan's audio contract, so copy_if_compatible must copy verbatim).
	sourcePath := filepath.Join(dir, "source.mp4")
	generateSourceWithAudio(t, ffmpegPath, sourcePath)

	// 2. Watermark PNG + burn ASS (both real artifacts referenced by the plan).
	watermarkPath := filepath.Join(dir, "watermark.png")
	generateWatermarkPNG(t, ffmpegPath, watermarkPath)
	subtitlePath := filepath.Join(dir, "subtitles.ass")
	writeBurnASS(t, subtitlePath)

	// 3. Seal the fully-resolved ClipRenderPlanV1 (background blur_source,
	//    watermark top_right 0.85/40, subtitles burn, audio copy policy).
	plan := compileClipRenderE2EPlan(t, dir, sourcePath, watermarkPath, subtitlePath)

	// 4. Execute through the REAL Rust render_clip boundary.
	executor := newRealExecutor(t, musclesPath, ffmpegPath)
	renderer := NewClipRendererWithExecutor(executor,
		mediaexec.EncoderPolicy{Codec: "libx264", Preset: "veryfast", CRF: 23},
		mediaexec.VideoProfile{
			Width: 1080, Height: 1920, FPSNum: 60, FPSDen: 1, KeyframeInterval: 120,
			AudioCodec: "aac", AudioBitrate: "128k", SampleRate: 48000, Channels: 2,
		},
		nil,
	)
	result, err := renderer.RenderClip(context.Background(), plan, cliprender.BackendFFmpegFallback)
	if err != nil {
		t.Fatalf("render_clip failed: %v", err)
	}

	// 5. Copy policy + honest GPU accounting from the typed outcome.
	if result.AudioCopyEligible == nil || !*result.AudioCopyEligible {
		t.Fatalf("AudioCopyEligible = %v, want true (source already aac/48k/stereo)", result.AudioCopyEligible)
	}
	if result.AudioEncodePasses == nil || *result.AudioEncodePasses != 0 {
		t.Fatalf("AudioEncodePasses = %v, want 0 (copy, zero re-encodes)", result.AudioEncodePasses)
	}
	if result.SubtitleRasterCPU == nil || !*result.SubtitleRasterCPU {
		t.Fatalf("SubtitleRasterCPU = %v, want true (burn rasterizes libass = CPU stage)", result.SubtitleRasterCPU)
	}
	if result.FFmpegMS <= 0 {
		t.Fatalf("FFmpegMS = %d, want native encode wall time > 0", result.FFmpegMS)
	}
	if result.OutputPath != plan.OutputPath || result.SizeBytes == 0 {
		t.Fatalf("output facts: %+v", result)
	}

	// 6. ffprobe: full contract validation (codec/pixel/geometry/fps/audio).
	probe := clipRenderFFprobe(t, ffprobePath, plan.OutputPath)
	if len(probe.Streams) != 2 {
		t.Fatalf("expected exactly 2 streams (video+audio), got %d", len(probe.Streams))
	}
	video := probe.Streams[0]
	if video.CodecName != "h264" || video.PixFmt != "yuv420p" {
		t.Fatalf("video codec/pix_fmt = %s/%s, want h264/yuv420p", video.CodecName, video.PixFmt)
	}
	if video.Width != 1080 || video.Height != 1920 {
		t.Fatalf("resolution = %dx%d, want 1080x1920", video.Width, video.Height)
	}
	if video.RFrameRate != "60/1" {
		t.Fatalf("r_frame_rate = %q, want 60/1", video.RFrameRate)
	}
	duration, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil {
		t.Fatalf("parse duration %q: %v", probe.Format.Duration, err)
	}
	if duration < 3.9 || duration > 4.1 {
		t.Fatalf("duration = %.3fs, want ~4.0s", duration)
	}
	audio := probe.Streams[1]
	if audio.CodecName != "aac" || audio.SampleRate != "48000" || audio.Channels != 2 {
		t.Fatalf("audio = %s %sHz %dch, want aac 48000 2ch", audio.CodecName, audio.SampleRate, audio.Channels)
	}

	// 7. Full decode: zero ffmpeg errors certifies the file is not a stub.
	if decodeErrors := fullDecode(t, ffmpegPath, plan.OutputPath); decodeErrors != "" {
		t.Fatalf("full decode produced errors:\n%s", decodeErrors)
	}
}

func generateSourceWithAudio(t *testing.T, ffmpeg, path string) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=1920x1080:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", "4",
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-ar", "48000", "-ac", "2", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate source %s: %v: %s", path, err, out)
	}
}

func generateWatermarkPNG(t *testing.T, ffmpeg, path string) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=red@0.8:s=180x100",
		"-frames:v", "1", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate watermark %s: %v: %s", path, err, out)
	}
}

func writeBurnASS(t *testing.T, path string) {
	t.Helper()
	ass := `[Script Info]
ScriptType: v4.00+

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: shorts-v1,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,3,2,2,20,20,20,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.00,0:00:02.00,shorts-v1,,0,0,0,,hello shorts
Dialogue: 0,0:00:02.00,0:00:04.00,shorts-v1,,0,0,0,,world burn
`
	if err := os.WriteFile(path, []byte(ass), 0o644); err != nil {
		t.Fatalf("write ASS: %v", err)
	}
}

func compileClipRenderE2EPlan(t *testing.T, dir, sourcePath, watermarkPath, subtitlePath string) cliprender.ClipRenderPlanV1 {
	t.Helper()
	shaOf := func(path string) string {
		t.Helper()
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		return hex.EncodeToString(sum[:])
	}
	plan, err := cliprender.Compile(cliprender.CompileInput{
		RunID: "e2e-clip-render",
		Source: &cliprender.MaterializedAsset{
			AssetID:   "asset-src",
			LocalPath: sourcePath,
			SHA256:    shaOf(sourcePath),
		},
		BackgroundMode: cliprender.BackgroundModeBlurSource,
		Watermark: &cliprender.MaterializedAsset{
			AssetID:   "asset-wm",
			LocalPath: watermarkPath,
			SHA256:    shaOf(watermarkPath),
		},
		WatermarkSpec: &cliprender.WatermarkSpec{
			Position: cliprender.PositionTopRight,
			Opacity:  0.85,
			MarginPX: 40,
		},
		Subtitles: &cliprender.SubtitleArtifact{
			LocalPath: subtitlePath,
			SHA256:    shaOf(subtitlePath),
			Mode:      cliprender.SubtitlesModeBurn,
			StyleID:   "shorts-v1",
		},
		Contract: &cliprender.ResolvedContract{
			ContractID:   cliprender.OutputContractVeloxAssemblyReadyV1,
			Container:    "mp4",
			VideoCodec:   "h264",
			VideoProfile: "high",
			PixelFormat:  "yuv420p",
			Width:        1080,
			Height:       1920,
			FPSNum:       60,
			FPSDen:       1,
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		},
		AudioMode:  cliprender.AudioModeCopyIfCompatible,
		OutputPath: filepath.Join(dir, "rendered-clip.mp4"),
	})
	if err != nil {
		t.Fatalf("compile sealed clip plan: %v", err)
	}
	return plan
}

type clipRenderProbeStream struct {
	CodecType  string `json:"codec_type"`
	CodecName  string `json:"codec_name"`
	PixFmt     string `json:"pix_fmt"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	RFrameRate string `json:"r_frame_rate"`
	SampleRate string `json:"sample_rate"`
	Channels   int    `json:"channels"`
}

type clipRenderProbeReport struct {
	Streams []clipRenderProbeStream `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func clipRenderFFprobe(t *testing.T, ffprobe, path string) clipRenderProbeReport {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error",
		"-show_entries", "stream=codec_type,codec_name,pix_fmt,width,height,r_frame_rate,sample_rate,channels",
		"-show_entries", "format=duration",
		"-of", "json", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %s: %v: %s", path, err, out)
	}
	var report clipRenderProbeReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("parse ffprobe output for %s: %v", path, err)
	}
	if len(report.Streams) == 0 {
		t.Fatalf("ffprobe %s returned no streams: %s", path, strings.TrimSpace(string(out)))
	}
	return report
}
