// Package rustexec is the single Go adapter for the Rust media execution
// plane. Application code must depend on its existing ports, not this package.
package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

type request struct {
	Operation        string          `json:"operation"`
	FFmpegPath       string          `json:"ffmpeg_path,omitempty"`
	SourcePath       string          `json:"source_path,omitempty"`
	OutputPath       string          `json:"output_path,omitempty"`
	TimestampSec     float64         `json:"timestamp_sec,omitempty"`
	StartSec         float64         `json:"start_sec,omitempty"`
	EndSec           float64         `json:"end_sec,omitempty"`
	IntervalFrames   uint32          `json:"interval_frames,omitempty"`
	Columns          uint32          `json:"columns,omitempty"`
	Rows             uint32          `json:"rows,omitempty"`
	Codec            string          `json:"codec,omitempty"`
	Preset           string          `json:"preset,omitempty"`
	CRF              int             `json:"crf,omitempty"`
	Width            uint32          `json:"width,omitempty"`
	Height           uint32          `json:"height,omitempty"`
	FPS              uint32          `json:"fps,omitempty"`
	DurationSec      float64         `json:"duration_sec,omitempty"`
	KeepAudio        bool            `json:"keep_audio,omitempty"`
	NoAudio          bool            `json:"no_audio,omitempty"`
	OverlayPath      string          `json:"overlay_path,omitempty"`
	Opacity          float64         `json:"opacity,omitempty"`
	InputPaths       []string        `json:"input_paths,omitempty"`
	NoTransitions    bool            `json:"no_transitions,omitempty"`
	TransitionEvery  int             `json:"transition_every,omitempty"`
	ClipDurationSec  int             `json:"clip_duration_sec,omitempty"`
	NoEffects        bool            `json:"no_effects,omitempty"`
	EffectsDir       string          `json:"effects_dir,omitempty"`
	EffectEvery      int             `json:"effect_every,omitempty"`
	EffectIndexHint  int             `json:"effect_index_hint,omitempty"`
	OverlayOpacity   float64         `json:"overlay_opacity,omitempty"`
	KeyframeInterval uint32          `json:"keyframe_interval,omitempty"`
	Font             string          `json:"font,omitempty"`
	Effects          []renderEffect  `json:"effects,omitempty"`
	Overlays         []renderOverlay `json:"overlays,omitempty"`
	MaxDurationSec   float64         `json:"max_duration_sec,omitempty"`
}

type renderEffect struct {
	Path     string  `json:"path"`
	DelayMS  int     `json:"delay_ms"`
	Duration float64 `json:"duration"`
	Volume   string  `json:"volume"`
}

type renderOverlay struct {
	Text  string `json:"text"`
	Start string `json:"start"`
	End   string `json:"end"`
	Size  string `json:"size"`
	Y     string `json:"y"`
	Color string `json:"color"`
}

type response struct {
	OK        bool           `json:"ok"`
	Operation string         `json:"operation"`
	Metadata  *mediaMetadata `json:"metadata"`
	Error     string         `json:"error"`
}

type mediaMetadata struct {
	DurationSec float64 `json:"duration_sec"`
	Width       uint32  `json:"width"`
	Height      uint32  `json:"height"`
	FPS         float64 `json:"fps"`
	VideoCodec  string  `json:"video_codec"`
	AudioCodec  string  `json:"audio_codec"`
	SampleRate  uint32  `json:"sample_rate"`
	Channels    uint32  `json:"channels"`
	HasVideo    bool    `json:"has_video"`
	HasAudio    bool    `json:"has_audio"`
}

type Client struct {
	binaryPath string
	ffmpegPath string
	log        *zap.Logger
	runner     commandRunner
}

type commandRunner interface {
	Run(context.Context, string, []byte) ([]byte, []byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, binary string, input []byte) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, binary)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func NewClient(binaryPath, ffmpegPath string, log *zap.Logger) *Client {
	if log == nil {
		log = zap.NewNop()
	}
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	return &Client{binaryPath: binaryPath, ffmpegPath: ffmpegPath, log: log, runner: execCommandRunner{}}
}

func (c *Client) call(ctx context.Context, req request) (response, error) {
	req.FFmpegPath = c.ffmpegPath
	payload, err := json.Marshal(req)
	if err != nil {
		return response{}, fmt.Errorf("marshal rust media request: %w", err)
	}
	stdout, stderr, err := c.runner.Run(ctx, c.binaryPath, append(payload, '\n'))
	if err != nil {
		return response{}, fmt.Errorf("rust media executor: %w: %s", err, stderr)
	}
	var result response
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &result); err != nil {
		return response{}, fmt.Errorf("decode rust media response: %w", err)
	}
	if !result.OK {
		return result, fmt.Errorf("rust media %s: %s", req.Operation, result.Error)
	}
	return result, nil
}

// VideoProcessor implements the infrastructure media processor port through
// the Rust executor. It contains no business policy or persistence logic.
type VideoProcessor struct{ client *Client }

func NewVideoProcessor(binaryPath, ffmpegPath string, log *zap.Logger) *VideoProcessor {
	return &VideoProcessor{client: NewClient(binaryPath, ffmpegPath, log)}
}

func (p *VideoProcessor) Normalize(ctx context.Context, input, output string, opts ffmpeg.NormalizeOptions) error {
	profile := opts.Profile.WithDefaults()
	policy := opts.Policy
	return p.run(ctx, request{
		Operation: "normalize", SourcePath: input, OutputPath: output,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS),
		Codec: policy.Codec, Preset: policy.Preset, CRF: policy.CRF,
		DurationSec: durationSeconds(opts), KeepAudio: opts.KeepAudio,
	})
}

func durationSeconds(opts ffmpeg.NormalizeOptions) float64 {
	if opts.DisableDuration {
		return 0
	}
	return float64(opts.Duration)
}

func (p *VideoProcessor) CutCopy(ctx context.Context, input, output, start, end string, noAudio bool) error {
	startSec, _ := strconv.ParseFloat(start, 64)
	endSec, _ := strconv.ParseFloat(end, 64)
	return p.run(ctx, request{Operation: "cut_copy", SourcePath: input, OutputPath: output, StartSec: startSec, EndSec: endSec, NoAudio: noAudio})
}

func (p *VideoProcessor) CutAndNormalize(ctx context.Context, input, output, start, end string, opts ffmpeg.CutAndNormalizeOptions) error {
	startSec, _ := strconv.ParseFloat(start, 64)
	endSec, _ := strconv.ParseFloat(end, 64)
	profile := opts.Profile.WithDefaults()
	policy := opts.Policy
	return p.run(ctx, request{
		Operation: "cut_and_normalize", SourcePath: input, OutputPath: output,
		StartSec: startSec, EndSec: endSec, NoAudio: opts.NoAudio,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS),
		Codec: policy.Codec, Preset: policy.Preset, CRF: policy.CRF,
	})
}

func (p *VideoProcessor) ApplyWatermark(ctx context.Context, input, output string, opts ffmpeg.WatermarkOptions) error {
	return p.run(ctx, request{Operation: "watermark", SourcePath: input, OutputPath: output, OverlayPath: opts.ImagePath, Opacity: opts.Opacity})
}

func (p *VideoProcessor) RemuxHLS(ctx context.Context, sourceURL, output string) error {
	return p.run(ctx, request{Operation: "remux_hls", SourcePath: sourceURL, OutputPath: output})
}

func (p *VideoProcessor) Probe(ctx context.Context, path string) (*ffmpeg.MediaInfo, error) {
	result, err := p.client.call(ctx, request{Operation: "probe", SourcePath: path})
	if err != nil {
		return nil, err
	}
	if result.Metadata == nil {
		return nil, fmt.Errorf("rust media probe returned no metadata")
	}
	m := result.Metadata
	return &ffmpeg.MediaInfo{
		Duration: durationFromSeconds(m.DurationSec),
		Width:    int(m.Width), Height: int(m.Height), FPS: m.FPS,
		VideoCodec: m.VideoCodec, AudioCodec: m.AudioCodec,
		SampleRate: int(m.SampleRate), Channels: int(m.Channels),
		HasVideo: m.HasVideo, HasAudio: m.HasAudio,
	}, nil
}

func (p *VideoProcessor) ExtractFrame(ctx context.Context, input, output string, timestamp float64) error {
	return p.run(ctx, request{Operation: "extract_frame", SourcePath: input, OutputPath: output, TimestampSec: timestamp})
}

func (p *VideoProcessor) GenerateProxy(ctx context.Context, input, output string) error {
	return p.run(ctx, request{Operation: "generate_proxy", SourcePath: input, OutputPath: output})
}

func (p *VideoProcessor) GenerateStoryboard(ctx context.Context, input, output string, intervalFrames, cols, rows int) error {
	return p.run(ctx, request{Operation: "generate_storyboard", SourcePath: input, OutputPath: output, IntervalFrames: uint32(intervalFrames), Columns: uint32(cols), Rows: uint32(rows)})
}

func (p *VideoProcessor) run(ctx context.Context, req request) error {
	_, err := p.client.call(ctx, req)
	return err
}

// AdminMediaProcessor adapts the Rust capabilities to the operator media
// ports. Drive traversal and publication remain in Go.
type AdminMediaProcessor struct {
	client *Client
	policy config.VideoEncoderPolicy
}

func NewAdminMediaProcessor(binaryPath, ffmpegPath string, policy config.VideoEncoderPolicy, log *zap.Logger) *AdminMediaProcessor {
	return &AdminMediaProcessor{client: NewClient(binaryPath, ffmpegPath, log), policy: policy}
}

func (p *AdminMediaProcessor) Probe(ctx context.Context, path string) (time.Duration, error) {
	info, err := (&VideoProcessor{client: p.client}).Probe(ctx, path)
	if err != nil {
		return 0, err
	}
	return info.Duration, nil
}

func (p *AdminMediaProcessor) Trim(ctx context.Context, inputPath string, maxSeconds float64) error {
	ext := filepath.Ext(inputPath)
	tmpPath := inputPath + ".trim.tmp" + ext
	defer os.Remove(tmpPath)
	_, err := p.client.call(ctx, request{
		Operation: "trim", SourcePath: inputPath, OutputPath: tmpPath,
		MaxDurationSec: maxSeconds, Codec: p.policy.Codec, Preset: p.policy.Preset, CRF: p.policy.CRF,
	})
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, inputPath)
}

func (p *AdminMediaProcessor) Render(ctx context.Context, manifest adminmedia.RenderManifest) error {
	req := request{Operation: "admin_render", SourcePath: manifest.Input, OutputPath: manifest.Output, Font: manifest.Font, Codec: p.policy.Codec, Preset: p.policy.Preset, CRF: p.policy.CRF}
	for _, effect := range manifest.Effects {
		req.Effects = append(req.Effects, renderEffect{Path: effect.Path, DelayMS: effect.DelayMS, Duration: effect.Duration, Volume: effect.Volume})
	}
	for _, overlay := range manifest.Overlays {
		req.Overlays = append(req.Overlays, renderOverlay{Text: overlay.Text, Start: overlay.Start, End: overlay.End, Size: overlay.Size, Y: overlay.Y, Color: overlay.Color})
	}
	_, err := p.client.call(ctx, req)
	return err
}

var _ adminmedia.AudioEditor = (*AdminMediaProcessor)(nil)
var _ adminmedia.ShortRenderer = (*AdminMediaProcessor)(nil)

// StockRenderer adapts the neutral StockRenderer port to the Rust
// render_stock capability. Transition and effect selection remains encoded in
// the neutral request; Rust owns FFmpeg graph construction and execution.
type StockRenderer struct {
	client *Client
}

func NewStockRenderer(binaryPath, ffmpegPath string, log *zap.Logger) *StockRenderer {
	return &StockRenderer{client: NewClient(binaryPath, ffmpegPath, log)}
}

func (r *StockRenderer) Render(ctx context.Context, input stockpipeline.RenderRequest) (stockpipeline.RenderResult, error) {
	_, err := r.client.call(ctx, request{
		Operation: "render_stock", OutputPath: input.OutputPath, InputPaths: input.InputPaths,
		Codec: input.Codec, Preset: input.Preset, CRF: input.CRF,
		Width: uint32(input.Width), Height: uint32(input.Height), FPS: uint32(input.FPS),
		KeepAudio: input.KeepAudio, NoTransitions: input.NoTransitions,
		TransitionEvery: input.TransitionEvery, ClipDurationSec: input.ClipDurationSec,
		NoEffects: input.NoEffects, EffectsDir: input.EffectsDir,
		EffectEvery: input.EffectEvery, EffectIndexHint: input.EffectIndexHint,
		OverlayOpacity: input.OverlayOpacity, KeyframeInterval: uint32(input.KeyframeInterval),
	})
	if err != nil {
		return stockpipeline.RenderResult{}, err
	}
	return stockpipeline.RenderResult{UsedFastPath: input.NoTransitions && input.NoEffects}, nil
}

var _ stockpipeline.StockRenderer = (*StockRenderer)(nil)

func durationFromSeconds(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
