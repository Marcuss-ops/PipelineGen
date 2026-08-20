package capabilities

// chronon_clip_renderer.go is the complex-render boundary. Rust continues to
// own acquisition/probing/mux/upload; Chronon owns the Vulkan text/composite
// graph. The adapter deliberately uses a plan + assets-root and never sends
// decoded video frames through Go memory.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

type chrononClipRenderExecutor struct {
	binary string
	ffmpeg string
}

// The current Chronon native Vulkan/NVENC surface pool is process-safe for
// one render at a time but not for several independent CLI processes sharing
// the same device. Keep Rust acquisition and publishing parallel, while
// serializing only the Chronon critical section until Chronon daemon-level
// surface ownership is enabled.
var chrononRenderMu sync.Mutex

func NewChrononClipRenderExecutor(binary, ffmpeg string) *chrononClipRenderExecutor {
	if strings.TrimSpace(binary) == "" {
		return nil
	}
	return &chrononClipRenderExecutor{binary: binary, ffmpeg: ffmpeg}
}

func (r *chrononClipRenderExecutor) RenderClip(ctx context.Context, plan cliprender.ClipRenderPlanV1, _ cliprender.RenderBackend) (rustexec.ClipRenderResult, error) {
	if r == nil || strings.TrimSpace(r.binary) == "" {
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon renderer is not configured")
	}
	if err := plan.Validate(); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	started := time.Now()
	runRoot, err := os.MkdirTemp(filepath.Dir(plan.OutputPath), ".chronon-clip-*")
	if err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	defer os.RemoveAll(runRoot)
	assets := filepath.Join(runRoot, "assets")
	if err := os.MkdirAll(filepath.Join(assets, "fonts"), 0o755); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	link := func(src, name string) error {
		if src == "" {
			return nil
		}
		dst := filepath.Join(assets, name)
		// Chronon validates the canonical path against assets-root, so a
		// symlink to an external materialized asset is intentionally rejected.
		// A hardlink keeps the operation zero-copy at the filesystem level;
		// only cross-filesystem materialization falls back to a byte copy.
		if err := os.Link(src, dst); err == nil {
			return nil
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	if err := link(plan.Source.Path, "clip.mp4"); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	if plan.Watermark != nil && plan.Watermark.Text == "" && plan.Watermark.Path != "" {
		if err := link(plan.Watermark.Path, "watermark"+filepath.Ext(plan.Watermark.Path)); err != nil {
			return rustexec.ClipRenderResult{}, err
		}
	}
	font := "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
	if _, err := os.Stat(font); err != nil {
		font = ""
	}
	if font != "" {
		_ = link(font, "fonts/DejaVuSans.ttf")
	}

	durationMS := probeDurationMS(ctx, r.ffmpeg, plan.Source.Path)
	if durationMS <= 0 && plan.Subtitles != nil {
		for _, c := range plan.Subtitles.Cues {
			if c.EndMs > durationMS {
				durationMS = c.EndMs
			}
		}
	}
	if durationMS <= 0 {
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon: cannot determine source duration")
	}
	fps := plan.Output.FPSNum / plan.Output.FPSDen
	if fps < 1 {
		fps = 1
	}
	frames := int((durationMS*int64(fps) + 999) / 1000)
	layers := []map[string]any{{"id": "video", "type": "video", "source": "clip.mp4", "fit": "stretch", "start_frame": 0, "duration_frames": frames}}
	if plan.Watermark != nil {
		if plan.Watermark.Text != "" {
			layers = append(layers, map[string]any{"id": "watermark", "type": "text", "text": plan.Watermark.Text, "font": "fonts/DejaVuSans.ttf", "font_size": 64, "color": []float64{1, 1, 1, plan.Watermark.Opacity}, "position": []int{plan.Output.Width / 2, plan.Output.Height / 2}, "start_frame": 0, "duration_frames": frames})
		} else if plan.Watermark.Path != "" {
			layers = append(layers, map[string]any{"id": "watermark", "type": "image", "source": "watermark" + filepath.Ext(plan.Watermark.Path), "position": []int{plan.Output.Width / 2, plan.Output.Height / 2}, "start_frame": 0, "duration_frames": frames})
		}
	}
	if plan.Subtitles != nil && len(plan.Subtitles.Cues) > 0 {
		var b strings.Builder
		for i, c := range plan.Subtitles.Cues {
			fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtTime(c.StartMs), srtTime(c.EndMs), c.Text)
		}
		if err := os.WriteFile(filepath.Join(assets, "subtitles.srt"), []byte(b.String()), 0o644); err != nil {
			return rustexec.ClipRenderResult{}, err
		}
		layers = append(layers, map[string]any{"id": "subtitles", "type": "subtitle_track", "source": "subtitles.srt", "format": "srt", "font": "fonts/DejaVuSans.ttf", "start_frame": 0, "duration_frames": frames})
	}
	planJSON := map[string]any{"schema": "chronon.render-plan", "version": 1, "job_id": plan.RunID, "canvas": map[string]any{"width": plan.Output.Width, "height": plan.Output.Height, "fps": fps, "duration_frames": frames}, "layers": layers, "output": map[string]any{"path": "chronon.mp4", "format": "mp4", "codec": "h264"}}
	planPath := filepath.Join(runRoot, "plan.json")
	b, _ := json.Marshal(planJSON)
	if err := os.WriteFile(planPath, b, 0o644); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	cmd := exec.CommandContext(ctx, r.binary, "render", "--plan", planPath, "--output", filepath.Join(runRoot, "chronon.mp4"), "--assets-root", assets, "--backend", "vulkan", "--hardware", "nvenc", "--ffmpeg-mode", "pipe", "--encoder-backend", "native", "--report")
	chrononRenderMu.Lock()
	out, renderErr := cmd.CombinedOutput()
	chrononRenderMu.Unlock()
	if renderErr != nil {
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon render: %w: %s", renderErr, strings.TrimSpace(string(out)))
	}
	if err := os.MkdirAll(filepath.Dir(plan.OutputPath), 0o755); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	if err := muxChrononAudio(ctx, r.ffmpeg, filepath.Join(runRoot, "chronon.mp4"), plan.Source.Path, plan.OutputPath); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	st, err := os.Stat(plan.OutputPath)
	if err != nil || st.Size() == 0 {
		return rustexec.ClipRenderResult{}, fmt.Errorf("chronon output missing or empty")
	}
	return rustexec.ClipRenderResult{OutputPath: plan.OutputPath, SizeBytes: st.Size(), DurationSec: float64(durationMS) / 1000, Width: uint32(plan.Output.Width), Height: uint32(plan.Output.Height), FPSNum: uint32(plan.Output.FPSNum), FPSDen: uint32(plan.Output.FPSDen), FFmpegMS: time.Since(started).Milliseconds(), AudioCopyEligible: boolPtr(true), AudioEncodePasses: intPtr(0), SubtitleRasterCPU: boolPtr(false)}, nil
}

func srtTime(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, (ms/60000)%60, (ms/1000)%60, ms%1000)
}
func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

func probeDurationMS(ctx context.Context, ffmpeg, path string) int64 {
	ffprobe := "ffprobe"
	if strings.TrimSpace(ffmpeg) != "" {
		ffprobe = filepath.Join(filepath.Dir(ffmpeg), "ffprobe")
	}
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return int64(sec*1000 + .5)
}

func muxChrononAudio(ctx context.Context, ffmpeg, video, source, output string) error {
	bin := ffmpeg
	if strings.TrimSpace(bin) == "" {
		bin = "ffmpeg"
	}
	tmp := output + ".chronon-mux.tmp.mp4"
	_ = os.Remove(tmp)
	cmd := exec.CommandContext(ctx, bin, "-y", "-hide_banner", "-loglevel", "error", "-i", video, "-i", source, "-map", "0:v:0", "-map", "1:a?", "-c:v", "copy", "-c:a", "copy", "-shortest", tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chronon audio mux: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return os.Rename(tmp, output)
}
