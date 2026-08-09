// Package rustexec is the single Go adapter for the Rust media execution
// plane. Application code must depend on its existing ports, not this package.
package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"go.uber.org/zap"
)

type request struct {
	Operation      string  `json:"operation"`
	FFmpegPath     string  `json:"ffmpeg_path,omitempty"`
	SourcePath     string  `json:"source_path,omitempty"`
	OutputPath     string  `json:"output_path,omitempty"`
	TimestampSec   float64 `json:"timestamp_sec,omitempty"`
	StartSec       float64 `json:"start_sec,omitempty"`
	EndSec         float64 `json:"end_sec,omitempty"`
	IntervalFrames uint32  `json:"interval_frames,omitempty"`
	Columns        uint32  `json:"columns,omitempty"`
	Rows           uint32  `json:"rows,omitempty"`
	Codec          string  `json:"codec,omitempty"`
	Preset         string  `json:"preset,omitempty"`
	CRF            int     `json:"crf,omitempty"`
	Width          uint32  `json:"width,omitempty"`
	Height         uint32  `json:"height,omitempty"`
	FPS            uint32  `json:"fps,omitempty"`
	DurationSec    float64 `json:"duration_sec,omitempty"`
	KeepAudio      bool    `json:"keep_audio,omitempty"`
	NoAudio        bool    `json:"no_audio,omitempty"`
	OverlayPath    string  `json:"overlay_path,omitempty"`
	Opacity        float64 `json:"opacity,omitempty"`
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

func durationFromSeconds(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}
