// Package render — concrete StockRenderer port implementation (PR6, June 2026).
//
// This package owns ALL FFmpeg-specific knowledge for stock chunk rendering:
// filter_complex construction, transition filter fragments (via the
// application-side TransitionRegistry), codec args (h264_nvenc vs libx264),
// overlay effects (loading .mp4 files from EffectsDir), and the actual
// process.Run() invocation.
//
// Import-boundary invariant (AGENTS.md Pattern 0 + Pattern 8):
//   - internal/application/** MUST NOT import this package.
//   - This package MAY import internal/application/assets/providers/stock
//     to satisfy `StockRenderer` (port-side types live in the application
//     layer; the infrastructure adapts to them). This is the standard
//     hexagonal architecture: application owns the port, infrastructure
//     owns the adapter.
//
// Building FFmpeg filter chains from the TransitionRegistry eliminates
// the two large near-symmetric switches in the pre-PR6 render.go.
package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// FFmpegRenderer is the canonical concrete implementation of the
// StockRenderer port. It reads configuration from RenderRequest and
// builds the FFmpeg filter_complex + arguments from the active
// TransitionRegistry. The renderer owns its own scratchbuffer pool
// (listPool) so no package-level mutable state leaks across instances.
type FFmpegRenderer struct {
	ffmpegPath  string
	encoderMode string
	encoder     *ffmpeg.Processor
	transitions stockpipeline.TransitionRegistry
	log         *zap.Logger
	listPool    *appliedListPoolImpl
}

// NewFFmpegRenderer constructs the FFmpeg renderer with the canonical
// binary path + transition catalog + logger. The caller (composition
// root) is responsible for constructing the stockpipeline.DefaultTransitionRegistry
// and passing it in — the infra renderer is intentionally ignorant of
// which transitions exist.
func NewFFmpegRenderer(ffmpegPath string, transitions stockpipeline.TransitionRegistry, log *zap.Logger) *FFmpegRenderer {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if transitions == nil {
		transitions = DefaultTransitionRegistry()
	}
	return newFFmpegRenderer(ffmpegPath, string(ffmpeg.EncoderLibX264), transitions, log)
}

// NewFFmpegRendererWithConfig constructs the renderer with the configured
// runtime encoder policy. The existing constructor remains software-first for
// callers that do not provide platform configuration.
func NewFFmpegRendererWithConfig(ffmpegPath, encoderMode string, transitions stockpipeline.TransitionRegistry, log *zap.Logger) *FFmpegRenderer {
	return newFFmpegRenderer(ffmpegPath, encoderMode, transitions, log)
}

func newFFmpegRenderer(ffmpegPath, encoderMode string, transitions stockpipeline.TransitionRegistry, log *zap.Logger) *FFmpegRenderer {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	if transitions == nil {
		transitions = DefaultTransitionRegistry()
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &FFmpegRenderer{
		ffmpegPath:  ffmpegPath,
		encoderMode: encoderMode,
		encoder:     ffmpeg.NewProcessorWithEncoder(ffmpegPath, encoderMode),
		transitions: transitions,
		log:         log,
		listPool:    &appliedListPoolImpl{},
	}
}

// Compile-time check that FFmpegRenderer satisfies StockRenderer.
var _ stockpipeline.StockRenderer = (*FFmpegRenderer)(nil)

// Render is the concrete port implementation. It picks the fast-path or
// complex path based on RenderRequest.NoEffects/Request.NoTransitions,
// then builds + dispatches the appropriate FFmpeg invocation.
func (r *FFmpegRenderer) Render(ctx context.Context, req stockpipeline.RenderRequest) (stockpipeline.RenderResult, error) {
	start := time.Now()
	if r.log == nil {
		r.log = zap.NewNop()
	}
	if r.encoder == nil {
		r.encoder = ffmpeg.NewProcessorWithEncoder(r.ffmpegPath, r.encoderMode)
	}
	if len(req.InputPaths) == 0 {
		return stockpipeline.RenderResult{}, fmt.Errorf("render: no input paths")
	}
	if req.OutputPath == "" {
		return stockpipeline.RenderResult{}, fmt.Errorf("render: empty output path")
	}
	// Every rendered clip/chunk uses the same technical profile. Request
	// fields retain the neutral port shape, but providers cannot introduce
	// a second codec/resolution/FPS profile at this boundary.
	requestedCodec := req.Codec
	if requestedCodec == "" {
		requestedCodec = r.encoderMode
	}
	canonical := (config.VideoConfig{}).CanonicalClip()
	req.Width = canonical.Width
	req.Height = canonical.Height
	req.FPS = canonical.FPS
	req.Codec = r.encoder.ResolveEncoder(ctx, requestedCodec)
	req.Preset = canonical.Preset
	req.CRF = canonical.CRF
	req.KeyframeInterval = canonical.KeyframeInterval
	if req.Logger != nil {
		// Route app-provided logger through the renderer's log field so
		// RenderRequest.Log wins when provided; otherwise fall back to
		// the renderer-canonical logger.
		r.log = req.Logger
	}

	// Single-input: re-encode + normalize to target codec/container.
	// Effectively the fast concat with one source.
	if len(req.InputPaths) == 1 {
		return r.renderSingle(ctx, req, start)
	}

	// Fast path: skip filter_complex (no transitions + no effects).
	if req.NoTransitions && req.NoEffects {
		return r.renderFastConcat(ctx, req, start)
	}

	// Complex path: build filter_complex.
	return r.renderComplex(ctx, req, start)
}

// ── renderSingle — single clip → normalize to target encoding ─────────

func (r *FFmpegRenderer) renderSingle(ctx context.Context, req stockpipeline.RenderRequest, start time.Time) (stockpipeline.RenderResult, error) {
	args := r.baseArgs()
	args = append(args, "-i", req.InputPaths[0])
	args = append(args, r.encodeArgs(req)...)
	args = append(args, req.OutputPath)

	if err := r.encoder.RunWithEncoderFallback(ctx, req.Codec, args, 20*time.Minute); err != nil {
		return stockpipeline.RenderResult{}, fmt.Errorf("render single: %w", err)
	}
	r.log.Info("stock render: single clip normalized",
		zap.Int("chunk", req.ChunkIndex),
		zap.String("out", req.OutputPath))
	return stockpipeline.RenderResult{
		UsedFastPath: false, // normalisation still ran (encode args applied)
		DurationMS:   time.Since(start).Milliseconds(),
	}, nil
}

// ── renderFastConcat — multi-input concat demuxer + normalize ──────────

func (r *FFmpegRenderer) renderFastConcat(ctx context.Context, req stockpipeline.RenderRequest, start time.Time) (stockpipeline.RenderResult, error) {
	concatPath := req.OutputPath + ".concat.mp4"
	_ = os.Remove(concatPath)
	defer os.Remove(concatPath)

	// Build concat list file (concat demuxer, -safe 0).
	absPaths := make([]string, len(req.InputPaths))
	lines := make([]string, len(req.InputPaths))
	for i, inp := range req.InputPaths {
		abs, err := filepath.Abs(inp)
		if err != nil {
			abs = inp
		}
		absPaths[i] = abs
		lines[i] = fmt.Sprintf("file '%s'", strings.ReplaceAll(abs, "'", "'\\''"))
	}
	tmpFile, err := os.CreateTemp("", "ffmpeg_concat_*.txt")
	if err != nil {
		return stockpipeline.RenderResult{}, fmt.Errorf("render fast: temp list: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	if _, err := tmpFile.WriteString(strings.Join(lines, "\n")); err != nil {
		tmpFile.Close()
		return stockpipeline.RenderResult{}, fmt.Errorf("render fast: write list: %w", err)
	}
	_ = tmpFile.Close()

	args := r.baseArgs()
	args = append(args,
		"-f", "concat",
		"-safe", "0",
		"-i", tmpFile.Name(),
		"-c", "copy",
		concatPath,
	)
	if _, err := process.Run(ctx, r.ffmpegPath, args, process.Options{Timeout: 10 * time.Minute}); err != nil {
		return stockpipeline.RenderResult{}, fmt.Errorf("render fast: concat: %w", err)
	}

	// Normalize the concat result to the target encoding (re-encode pass
	// that the pre-PR6 implementation called `s.ffmpegProc.Normalize`).
	normArgs := r.baseArgs()
	normArgs = append(normArgs, "-i", concatPath)
	normArgs = append(normArgs, r.encodeArgs(req)...)
	normArgs = append(normArgs, req.OutputPath)
	if err := r.encoder.RunWithEncoderFallback(ctx, req.Codec, normArgs, 20*time.Minute); err != nil {
		return stockpipeline.RenderResult{}, fmt.Errorf("render fast: normalize: %w", err)
	}

	r.log.Info("stock render: fast concat + normalize",
		zap.Int("chunk", req.ChunkIndex),
		zap.Int("inputs", len(req.InputPaths)),
		zap.String("out", req.OutputPath))
	return stockpipeline.RenderResult{
		UsedFastPath: true,
		DurationMS:   time.Since(start).Milliseconds(),
	}, nil
}

// ── renderComplex — filter_complex with transitions + overlays ─────────

func (r *FFmpegRenderer) renderComplex(ctx context.Context, req stockpipeline.RenderRequest, start time.Time) (stockpipeline.RenderResult, error) {
	effects, err := r.loadEffects(req.EffectsDir)
	if err != nil && !req.NoEffects {
		r.log.Warn("render: failed to load effects, proceeding without overlays", zap.Error(err))
		effects = nil
	}

	args := r.baseArgs()
	for _, p := range req.InputPaths {
		args = append(args, "-i", p)
	}

	overlayIdx := -1
	if !req.NoEffects && len(effects) > 0 {
		// Deterministic-ish hint via EffectIndexHint (modulo length).
		idx := req.EffectIndexHint % len(effects)
		if idx < 0 {
			idx += len(effects)
		}
		overlayIdx = len(req.InputPaths)
		args = append(args, "-i", effects[idx])
	}

	// ── Build filter_complex ───────────────────────────────────────
	var fc strings.Builder
	inputCount := len(req.InputPaths)
	transitionEvery := req.TransitionEvery
	if transitionEvery < 0 {
		transitionEvery = 4 // negative → use safe default
	}
	effectEvery := req.EffectEvery
	if effectEvery < 0 {
		effectEvery = 3 // negative → use safe default
	}
	catalog := r.transitions.All()
	appliedTransitions := r.listPool.Get().(*[]string)
	*appliedTransitions = (*appliedTransitions)[:0]
	defer r.listPool.Put(appliedTransitions)
	appliedOverlays := r.listPool.Get().(*[]string)
	*appliedOverlays = (*appliedOverlays)[:0]
	defer r.listPool.Put(appliedOverlays)

	for idx := 0; idx < inputCount; idx++ {
		clipFilters := []string{
			ffmpeg.CanonicalClipFilter(config.VideoConfig{Width: req.Width, Height: req.Height, FPS: req.FPS}),
		}

		// Fade-out at the END of every Nth clip.
		// transitionEvery == 0 means "disabled" (every 0th clip = never).
		if !req.NoTransitions && transitionEvery > 0 && (idx+1)%transitionEvery == 0 && len(catalog) > 0 {
			tIdx := (idx + 1) / transitionEvery
			t := catalog[tIdx%len(catalog)]
			clipFilters = append(clipFilters, t.RenderEnd(req.ClipDurationSec))
			r.log.Info("stock pipeline transition applied",
				zap.Int("chunk_index", req.ChunkIndex),
				zap.Int("after_clip_index", idx),
				zap.String("type", t.Name),
			)
			*appliedTransitions = append(*appliedTransitions, t.Name)
		}

		// Fade-in at the START of every (Nth+1) clip after the first.
		// transitionEvery == 0 means "disabled".
		if !req.NoTransitions && transitionEvery > 0 && idx > 0 && idx%transitionEvery == 0 && len(catalog) > 0 {
			tIdx := idx / transitionEvery
			t := catalog[tIdx%len(catalog)]
			clipFilters = append(clipFilters, t.RenderStart(req.ClipDurationSec))
		}

		// Overlay effects: every Nth clip gets the .mp4 overlay blended on top.
		// effectEvery == 0 means "disabled".
		if !req.NoEffects && effectEvery > 0 && overlayIdx >= 0 && (idx+1)%effectEvery == 0 && len(effects) > 0 {
			r.log.Info("stock pipeline effect applied",
				zap.Int("chunk_index", req.ChunkIndex),
				zap.Int("clip_index", idx),
				zap.String("effect_file", filepath.Base(effects[req.EffectIndexHint%len(effects)])),
			)
			*appliedOverlays = append(*appliedOverlays, effects[req.EffectIndexHint%len(effects)])
			fc.WriteString(fmt.Sprintf("[%d:v]%s[vtemp%d];", idx, strings.Join(clipFilters, ","), idx))
			fc.WriteString(fmt.Sprintf("[%d:v]scale=%d:%d,fps=%d,setsar=1,format=yuva420p,colorchannelmixer=aa=%f[effect%d];",
				overlayIdx, req.Width, req.Height, req.FPS, req.OverlayOpacity, idx))
			fc.WriteString(fmt.Sprintf("[vtemp%d][effect%d]overlay=shortest=1[v%d];", idx, idx, idx))
		} else {
			fc.WriteString(fmt.Sprintf("[%d:v]%s[v%d];", idx, strings.Join(clipFilters, ","), idx))
		}
	}

	// Concat all per-clip streams [v0..vN-1] → [vfinal].
	for idx := 0; idx < inputCount; idx++ {
		fc.WriteString(fmt.Sprintf("[v%d]", idx))
	}
	fc.WriteString(fmt.Sprintf("concat=n=%d:v=1:a=0[vfinal]", inputCount))

	args = append(args, "-filter_complex", fc.String(), "-map", "[vfinal]")

	if !req.KeepAudio {
		args = append(args, "-an")
	}
	args = append(args, r.encodeArgs(req)...)
	args = append(args, req.OutputPath)

	if err := r.encoder.RunWithEncoderFallback(ctx, req.Codec, args, 20*time.Minute); err != nil {
		return stockpipeline.RenderResult{}, fmt.Errorf("render complex: %w", err)
	}

	r.log.Info("stock render: complex filter_complex",
		zap.Int("chunk", req.ChunkIndex),
		zap.Int("inputs", inputCount),
		zap.Strings("transitions", *appliedTransitions),
		zap.Strings("overlays", *appliedOverlays),
		zap.String("out", req.OutputPath))

	return stockpipeline.RenderResult{
		UsedFastPath:        false,
		AppliedTransitions:  append([]string(nil), *appliedTransitions...),
		AppliedOverlayFiles: append([]string(nil), *appliedOverlays...),
		DurationMS:          time.Since(start).Milliseconds(),
	}, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

// baseArgs is the canonical -y/-hide_banner/-loglevel preamble used by
// every FFmpeg invocation in this file.
func (r *FFmpegRenderer) baseArgs() []string {
	return []string{"-y", "-hide_banner", "-loglevel", "warning"}
}

// encodeArgs assembles the per-codec encoding args (shared by single,
// fast, and complex paths). h264_nvenc uses -qp + -rc constqp (libx264
// uses -crf); the pre-PR6 implementation branched on Codec here.
func (r *FFmpegRenderer) encodeArgs(req stockpipeline.RenderRequest) []string {
	args := []string{
		"-c:v", req.Codec,
		"-preset", ffmpeg.NormalizeEncoderPreset(req.Codec, req.Preset),
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ar", "48000",
		"-ac", "2",
		"-movflags", "+faststart",
	}
	if req.Codec == "h264_nvenc" {
		args = append(args,
			"-rc", "vbr",
			"-cq", fmt.Sprintf("%d", req.CRF),
			"-tune", "hq",
			"-bf", "0",
		)
	} else {
		args = append(args, "-crf", fmt.Sprintf("%d", req.CRF))
	}
	if req.KeyframeInterval > 0 {
		args = append(args, "-g", fmt.Sprintf("%d", req.KeyframeInterval))
	}
	return args
}

// loadEffects scans the given directory for .mp4 overlay effect files.
// Returns an error when the directory is empty (so callers can fall
// back to a no-overlay render). Empty dir → empty slice (no error).
func (r *FFmpegRenderer) loadEffects(dir string) ([]string, error) {
	if dir == "" {
		return nil, fmt.Errorf("effects dir is empty")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read effects dir %q: %w", dir, err)
	}
	var effects []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".mp4") {
			effects = append(effects, filepath.Join(dir, e.Name()))
		}
	}
	if len(effects) == 0 {
		return nil, fmt.Errorf("no .mp4 effect files found in %s", dir)
	}
	return effects, nil
}

// appliedListPool reuses []string buffers across complex render invocations to
// avoid an allocation per chunk. The pool lives on the FFmpegRenderer
// receiver (see listPool field + appliedListPoolImpl.buffer below), so no
// package-level mutable state survives between renderer instances. The
// pre-PR6 allocate-on-call with `var appliedTransitions []string` was simpler
// but per-chunk garbage; the pool keeps the perf neutral without sacrificing
// readability.
//
// Concurrency: renderComplex is serialised upstream in chunk rendering, so the
// per-renderer buffer is single-threaded by design. No sync.Mutex is needed.
// When StockRenderer becomes concurrent per renderer, replace the per-renderer
// slice with sync.Pool[string][]string so buffers are recycled per-goroutine.
//
// Known aliasing caveat: the impl holds a single internal buffer; the two
// current Get() callers inside renderComplex (appliedTransitions +
// appliedOverlays) alias the same backing array. This is unchanged from the
// pre-refactor behaviour and is intentionally preserved here to keep this PR
// scoped to "remove the global var"; the aliasing lives on the followup
// "FIX-OVERLAY-ALIASING" backlog item.

// appliedListPoolImpl holds the scratch []string buffer on the receiver so each
// FFmpegRenderer owns its own (no package-level mutables). Get returns a
// pointer to the buffer; Put zeroes the slice length so the buffer cap is
// reused without retaining pointers into long-lived render graphs.
type appliedListPoolImpl struct {
	buffer []string
}

func (p *appliedListPoolImpl) Get() any { return p.get() }
func (p *appliedListPoolImpl) Put(v any) {
	if s, ok := v.(*[]string); ok {
		*p.get() = (*s)[:0]
	}
}
func (p *appliedListPoolImpl) get() *[]string { return &p.buffer }
