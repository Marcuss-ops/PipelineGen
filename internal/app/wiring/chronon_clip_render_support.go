package wiring

// chronon_clip_render_support.go holds the pure helper surface of the
// Chronon complex-render boundary: the capability-probe decoration, the
// watermark geometry helpers and the process/IPC/audio-mux primitives the
// executor drives. Keeping them in a dedicated file keeps the executor
// itself (chronon_clip_renderer.go) focused on render orchestration and
// satisfies the strict 600-line gate.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	"go.uber.org/zap"
)

// Process-wide Chronon lifecycle is initialized once, but GPU work is admitted
// through a bounded semaphore (default C=2) instead of a global render mutex.
// The semaphore is context-aware and is shared by every Chronon render in this
// process; mux and publication happen after the permit is released.
var chrononRuntimeControlInit sync.Once

// WithChrononMetrics attaches the Chronon Metrics Adapter. It is optional and
// nil-tolerant: without it the render simply skips the performance-registry
// projection.
func (r *chrononClipRenderExecutor) WithChrononMetrics(a *cliprender.ChrononMetricsAdapter) *chrononClipRenderExecutor {
	if r != nil {
		r.chrononMetrics = a
	}
	return r
}

// logPhase emits a single chronon render phase line with consistent fields so
// the server log can be grep'd by run_id or phase to reconstruct timelines.
func (r *chrononClipRenderExecutor) logPhase(phase, runID string, fields ...zap.Field) {
	all := append([]zap.Field{
		zap.String("subsystem", "chronon_render"),
		zap.String("phase", phase),
		zap.String("run_id", runID),
	}, fields...)
	r.log.Info("clip.render.chronon.phase", all...)
}

// chrononTimingProjection contains the common GPU and hardware metrics
// projected from Chronon's timing sidecar.
type chrononTimingProjection struct {
	Job struct {
		GPU struct {
			GPUReadbackBytes        *uint64 `json:"gpu_readback_bytes"`
			GPUUploadBytes          *uint64 `json:"gpu_upload_bytes"`
			EncoderStagingCopyBytes *uint64 `json:"encoder_staging_copy_bytes"`
			NV12ToRGBAFrames        *uint64 `json:"nv12_to_rgba_frames"`
			RGBAToNV12Frames        *uint64 `json:"rgba_to_nv12_frames"`
			CUDACompositeFrames     *uint64 `json:"cuda_composite_frames"`
			CUDACompositeWallUS     *uint64 `json:"cuda_composite_wall_us"`
			VideoDecodeWallMS       *uint64 `json:"video_decode_wall_ms"`
		} `json:"gpu"`
		Hardware struct {
			GPUUtilizationAvg   *float64 `json:"gpu_utilization_avg"`
			GPUUtilizationPeak  *float64 `json:"gpu_utilization_peak"`
			NVENCUtilizationAvg *float64 `json:"nvenc_utilization_avg"`
			NVDECUtilizationAvg *float64 `json:"nvdec_utilization_avg"`
			VRAMUsedPeakMB      *uint64  `json:"vram_used_peak_mb"`
		} `json:"hardware"`
	} `json:"job"`
}

func readChrononProjection(path string) (rustexec.ClipRenderResult, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	var doc chrononTimingProjection
	if err := json.Unmarshal(b, &doc); err != nil {
		return rustexec.ClipRenderResult{}, err
	}
	var out rustexec.ClipRenderResult
	g := doc.Job.GPU
	out.GPUReadbackBytes, out.GPUUploadBytes = g.GPUReadbackBytes, g.GPUUploadBytes
	out.EncoderStagingCopyBytes = g.EncoderStagingCopyBytes
	out.NV12ToRGBAFrames, out.RGBAToNV12Frames = g.NV12ToRGBAFrames, g.RGBAToNV12Frames
	out.CUDACompositeFrames = g.CUDACompositeFrames
	if g.VideoDecodeWallMS != nil {
		out.DecodeMS = i64Ptr(*g.VideoDecodeWallMS)
	}
	if g.CUDACompositeWallUS != nil {
		out.FilterGraphMS = i64Ptr((*g.CUDACompositeWallUS + 999) / 1000)
	}
	h := doc.Job.Hardware
	out.GPUUtilizationAvg, out.GPUUtilizationPeak = h.GPUUtilizationAvg, h.GPUUtilizationPeak
	out.NVENCUtilizationAvg, out.NVDECUtilizationAvg = h.NVENCUtilizationAvg, h.NVDECUtilizationAvg
	out.VRAMUsedPeakMB = h.VRAMUsedPeakMB
	return out, nil
}

func i64Ptr(v uint64) *int64 {
	if v > uint64(^uint64(0)>>1) {
		return nil
	}
	n := int64(v)
	return &n
}

// chrononAwareCapabilityProbe decorates the canonical ffmpeg capability probe
// with the Chronon render binary's presence. Backend selection is the
// resolver's single authority, so the probe must honestly report whether the
// Chronon backend is configured at all — without this flag the resolver could
// never select chronon_vulkan (the adapter's try-and-fall-back shortcut that
// used to reach it is gone).
type chrononAwareCapabilityProbe struct {
	base       cliprender.BackendCapabilityProbe
	chrononBin string
}

func (p chrononAwareCapabilityProbe) ProbeCapabilities(ctx context.Context) (cliprender.RendererCapabilities, error) {
	caps, err := p.base.ProbeCapabilities(ctx)
	if err != nil {
		return caps, err
	}
	socket := strings.TrimSpace(os.Getenv("CHRONON_RENDER_SOCKET"))
	if socket == "" {
		socket = strings.TrimSpace(os.Getenv("CHRONON_SOCKET_PATH"))
	}
	caps.ChrononVulkan = strings.TrimSpace(p.chrononBin) != "" || socket != ""
	return caps, nil
}

var _ cliprender.BackendCapabilityProbe = (*chrononAwareCapabilityProbe)(nil)

// watermarkPositionForSize resolves the world-space center position for a
// watermark of the GIVEN size. The ChrononPlanProjector passes the styled
// size (style.width_px/height_px overrides), so a resized logo keeps its
// declared position instead of being positioned by the original image size.
func watermarkPositionForSize(imgW, imgH int, position string, canvasW, canvasH, margin int) []int {
	if margin < 0 {
		margin = 0
	}
	if imgW <= 0 || imgH <= 0 {
		return []int{0, 0}
	}
	x, y := margin, margin
	switch position {
	case cliprender.PositionTopRight:
		x = canvasW - margin - imgW
	case cliprender.PositionBottomLeft:
		y = canvasH - margin - imgH
	case cliprender.PositionBottomRight:
		x = canvasW - margin - imgW
		y = canvasH - margin - imgH
	case cliprender.PositionCenter:
		x = (canvasW - imgW) / 2
		y = (canvasH - imgH) / 2
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	// Chronon image positions are world-space centers, not top-left pixels.
	return []int{x + imgW/2 - canvasW/2, y + imgH/2 - canvasH/2}
}

func watermarkDimensions(path string) (int, int) {
	imgW, imgH := 0, 0
	if f, err := os.Open(path); err == nil {
		if cfg, _, err := image.DecodeConfig(f); err == nil {
			imgW, imgH = cfg.Width, cfg.Height
		}
		_ = f.Close()
	}
	return imgW, imgH
}

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }

// runChrononIPC submits one RENDER_JOB to the persistent Chronon daemon.
// The wire format is the small stable protocol documented in
// Chronon3d/apps/chronon3d_cli/daemon/chronon_ipc.hpp:
// MAGIC|COMMAND|PAYLOAD_LEN|PAYLOAD, all integers big-endian.
func runChrononIPC(ctx context.Context, socket string, payload []byte, logPath, runID string, log *zap.Logger) (chrononProcessOutput, error) {
	const (
		magic       = uint32(0x43484e33) // CHN3
		renderJob   = uint32(6)
		maxPayload  = 64 * 1024 * 1024
		headerBytes = 12
	)
	if len(payload) > maxPayload {
		return chrononProcessOutput{}, fmt.Errorf("chronon IPC payload too large: %d", len(payload))
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socket)
	if err != nil {
		return chrononProcessOutput{}, fmt.Errorf("chronon IPC connect %q: %w", socket, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	frame := make([]byte, headerBytes+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], magic)
	binary.BigEndian.PutUint32(frame[4:8], renderJob)
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
	copy(frame[headerBytes:], payload)
	if _, err := conn.Write(frame); err != nil {
		return chrononProcessOutput{}, fmt.Errorf("chronon IPC write: %w", err)
	}
	replyHeader := make([]byte, headerBytes)
	if _, err := io.ReadFull(conn, replyHeader); err != nil {
		return chrononProcessOutput{}, fmt.Errorf("chronon IPC reply header: %w", err)
	}
	if binary.BigEndian.Uint32(replyHeader[0:4]) != magic {
		return chrononProcessOutput{}, fmt.Errorf("chronon IPC reply has invalid magic")
	}
	status := binary.BigEndian.Uint32(replyHeader[4:8])
	messageLen := binary.BigEndian.Uint32(replyHeader[8:12])
	if messageLen > maxPayload {
		return chrononProcessOutput{}, fmt.Errorf("chronon IPC reply too large: %d", messageLen)
	}
	message := make([]byte, messageLen)
	if _, err := io.ReadFull(conn, message); err != nil {
		return chrononProcessOutput{}, fmt.Errorf("chronon IPC reply body: %w", err)
	}
	if logFile, openErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); openErr == nil {
		_, _ = logFile.Write(message)
		_, _ = logFile.WriteString("\n")
		_ = logFile.Close()
	}
	if status != 0 {
		return chrononProcessOutput{Tail: append([]byte(nil), message...), TotalBytes: int64(len(message))}, fmt.Errorf("chronon IPC render failed (status=%d): %s", status, strings.TrimSpace(string(message)))
	}
	log.Debug("chronon IPC render completed", zap.String("run_id", runID), zap.String("socket", socket), zap.Int("reply_bytes", len(message)))
	return chrononProcessOutput{Tail: append([]byte(nil), message...), TotalBytes: int64(len(message))}, nil
}

func probeDurationMS(ctx context.Context, ffmpeg, path string) int64 {
	if cached, ok := chrononProbeLookup(path); ok {
		return cached
	}
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
	durationMS := int64(sec*1000 + .5)
	chrononProbeStore(path, durationMS)
	return durationMS
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
