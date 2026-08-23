package rustexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// stockrustClip is one synthetic source clip in the live 3-clip battery.
type stockrustClip struct {
	assetID    string
	durationUS int64
	frameCount int
}

// TestStockRustRenderThreeSyntheticClips drives the REAL Rust render_stock
// canonical executor against three ffmpeg `testsrc` clips (2s + 3s + 4s, no
// audio, no transitions/effects). It certifies the end-to-end concat: output
// exists, is H.264 1280x720 30fps, ~9.000s / ~270 frames, and decodes fully
// with zero ffmpeg errors.
func TestStockRustRenderThreeSyntheticClips(t *testing.T) {
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
	)
	clips := []stockrustClip{
		{assetID: "clip-0", durationUS: 2_000_000, frameCount: 60},  // 2s
		{assetID: "clip-1", durationUS: 3_000_000, frameCount: 90},  // 3s
		{assetID: "clip-2", durationUS: 4_000_000, frameCount: 120}, // 4s
	}
	var totalUS int64
	for _, c := range clips {
		totalUS += c.durationUS
	}
	wantFrames := 0
	for _, c := range clips {
		wantFrames += c.frameCount
	}

	// 1. Generate three visually distinct synthetic clips (testsrc) with exact
	//    frame counts so the canonical plan's source ranges stay frame-accurate.
	assetsDir := t.TempDir()
	clipPaths := make([]string, len(clips))
	for i, c := range clips {
		clipPaths[i] = filepath.Join(assetsDir, c.assetID+".mp4")
		generateTestsrcClip(t, ffmpegPath, clipPaths[i], c.frameCount, width, height, fps)
	}

	// 2. Compile and validate the sealed canonical render plan.
	plan := compileThreeClipPlan(t, clips, clipPaths, totalUS, fps)
	validated, err := render.ValidateRenderPlan(plan, filesystem.NewOS())
	if err != nil {
		t.Fatalf("validate canonical render plan: %v", err)
	}

	// 3. Execute through the real Rust render_stock executor.
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
		t.Fatalf("stockrust render failed: %v", err)
	}

	output := plan.OutputPath

	// 4. ffprobe: container/codec/profile sanity.
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		t.Fatalf("output missing or empty: %v", err)
	}
	probe := ffprobeOutput(t, ffprobePath, output)
	if len(probe.Streams) != 1 {
		t.Fatalf("expected exactly 1 video stream (no audio), got %d", len(probe.Streams))
	}
	stream := probe.Streams[0]
	if stream.CodecName != "h264" {
		t.Fatalf("codec = %q, want h264", stream.CodecName)
	}
	if stream.Width != width || stream.Height != height {
		t.Fatalf("resolution = %dx%d, want %dx%d", stream.Width, stream.Height, width, height)
	}
	if stream.RFrameRate != "30/1" {
		t.Fatalf("r_frame_rate = %q, want 30/1", stream.RFrameRate)
	}
	dur, err := strconv.ParseFloat(probe.Format.Duration, 64)
	if err != nil {
		t.Fatalf("parse output duration %q: %v", probe.Format.Duration, err)
	}
	if dur < 8.9 || dur > 9.1 {
		t.Fatalf("duration = %.3fs, want ~9.000s", dur)
	}

	// 5. Frame count must be exactly the plan's duration_frames.
	actualFrames := ffprobeFrameCount(t, ffprobePath, output)
	if actualFrames != wantFrames {
		t.Fatalf("frame count = %d, want %d", actualFrames, wantFrames)
	}

	// 6. Full decode: a single ffmpeg error line fails the certification.
	decodeErrors := fullDecode(t, ffmpegPath, output)
	if decodeErrors != "" {
		t.Fatalf("full decode produced errors:\n%s", decodeErrors)
	}
}

func generateTestsrcClip(t *testing.T, ffmpeg, path string, frames, width, height, fps int) {
	t.Helper()
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc=size=%dx%d:rate=%d", width, height, fps),
		"-frames:v", strconv.Itoa(frames),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-an", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate testsrc clip %s: %v: %s", path, err, out)
	}
}

func compileThreeClipPlan(t *testing.T, clips []stockrustClip, clipPaths []string, totalUS int64, fps int) render.RenderPlan {
	t.Helper()
	segments := make([]audio.TimelineSegment, len(clips))
	manifest := make([]render.AssetManifestEntry, len(clips))
	var startUS int64
	for i, c := range clips {
		contents, err := os.ReadFile(clipPaths[i])
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		segments[i] = audio.TimelineSegment{
			ID:              fmt.Sprintf("scene-%d", i),
			Index:           i,
			TimelineStartUS: startUS,
			DurationUS:      c.durationUS,
			Video:           audio.VideoSegment{AssetID: c.assetID, SourceInUS: 0, SourceDurationUS: c.durationUS},
			Audio:           audio.AudioIntent{Mode: audio.AudioSilence},
		}
		manifest[i] = render.AssetManifestEntry{
			AssetID: c.assetID, Path: clipPaths[i],
			SHA256: hex.EncodeToString(sum[:]), FrameCount: int64(c.frameCount),
		}
		startUS += c.durationUS
	}
	timeline := audio.CanonicalTimeline{Version: audio.TimelineVersion, DurationUS: totalUS, Segments: segments}
	plan, err := render.Compile(render.CompileInput{
		JobID: "stockrust-live-3clip", Revision: "generation.v1",
		OutputPath: filepath.Join(t.TempDir(), "stockrust-output.mp4"),
		FrameRate:  audio.IntegerFrameRate(fps), Timeline: timeline, Manifest: manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

type ffprobeStream struct {
	CodecName  string `json:"codec_name"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	RFrameRate string `json:"r_frame_rate"`
}

type ffprobeReport struct {
	Streams []ffprobeStream `json:"streams"`
	Format  struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func ffprobeOutput(t *testing.T, ffprobe, path string) ffprobeReport {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error",
		"-show_entries", "stream=codec_name,width,height,r_frame_rate,nb_frames",
		"-show_entries", "format=duration",
		"-of", "json", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %s: %v: %s", path, err, out)
	}
	var report ffprobeReport
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("parse ffprobe output for %s: %v", path, err)
	}
	return report
}

func ffprobeFrameCount(t *testing.T, ffprobe, path string) int {
	t.Helper()
	cmd := exec.Command(ffprobe, "-v", "error", "-select_streams", "v:0", "-count_frames",
		"-show_entries", "stream=nb_read_frames", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe frame count %s: %v: %s", path, err, out)
	}
	frames, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || frames <= 0 {
		t.Fatalf("ffprobe frame count %s: invalid value %q", path, string(out))
	}
	return frames
}

func fullDecode(t *testing.T, ffmpeg, path string) string {
	t.Helper()
	var stderr bytes.Buffer
	cmd := exec.Command(ffmpeg, "-v", "error", "-i", path, "-f", "null", "-")
	cmd.Stderr = &stderr
	// A non-zero exit may still indicate decode errors, but we only assert on
	// the stderr stream: `-v error` emits nothing when the file decodes clean.
	_ = cmd.Run()
	return strings.TrimSpace(stderr.String())
}
