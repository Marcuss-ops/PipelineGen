package rustexec

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strconv"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	ffmpeg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/ffmpeg"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

type VideoProcessor struct {
	client  *Client
	policy  config.VideoEncoderPolicy
	profile config.CanonicalVideoProfile
}

func NewVideoProcessor(binaryPath, ffmpegPath string, log *zap.Logger) *VideoProcessor {
	return &VideoProcessor{client: NewClient(binaryPath, ffmpegPath, log)}
}

// NewConfiguredVideoProcessor binds the single Go-owned encoder policy to all
// encoding capabilities exposed by this adapter. Probe and copy operations do
// not use it; encoded operations fail closed when it is absent.
func NewConfiguredVideoProcessor(binaryPath, ffmpegPath string, policy config.VideoEncoderPolicy, profile config.CanonicalVideoProfile, log *zap.Logger) *VideoProcessor {
	return &VideoProcessor{client: NewClient(binaryPath, ffmpegPath, log), policy: policy, profile: profile.WithDefaults()}
}

func (p *VideoProcessor) policyFor(codec, preset string, crf int) (string, string, int, error) {
	if codec == "" {
		codec = p.policy.Codec
	}
	if preset == "" {
		preset = p.policy.Preset
	}
	if crf <= 0 {
		crf = p.policy.CRF
	}
	if codec == "" || preset == "" || crf <= 0 {
		return "", "", 0, fmt.Errorf("ENCODER_POLICY_REQUIRED: Go did not provide a complete video encoder policy")
	}
	return codec, preset, crf, nil
}

func (p *VideoProcessor) Normalize(ctx context.Context, input, output string, opts ffmpeg.NormalizeOptions) error {
	profile := opts.Profile
	if profile == (config.CanonicalVideoProfile{}) {
		profile = p.profile
	}
	profile = profile.WithDefaults()
	codec, preset, crf, err := p.policyFor(opts.Policy.Codec, opts.Policy.Preset, opts.Policy.CRF)
	if err != nil {
		return err
	}
	return p.run(ctx, request{
		Operation: "normalize", SourcePath: input, OutputPath: output,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec: profile.AudioCodec, AudioBitrate: profile.AudioBitrate,
		SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels),
		Codec: codec, Preset: preset, CRF: crf,
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
	profile := opts.Profile
	if profile == (config.CanonicalVideoProfile{}) {
		profile = p.profile
	}
	profile = profile.WithDefaults()
	codec, preset, crf, err := p.policyFor(opts.Policy.Codec, opts.Policy.Preset, opts.Policy.CRF)
	if err != nil {
		return err
	}
	return p.run(ctx, request{
		Operation: "cut_and_normalize", SourcePath: input, OutputPath: output,
		StartSec: startSec, EndSec: endSec, NoAudio: opts.NoAudio,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec: profile.AudioCodec, AudioBitrate: profile.AudioBitrate,
		SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels),
		Codec: codec, Preset: preset, CRF: crf,
	})
}

func (p *VideoProcessor) ApplyWatermark(ctx context.Context, input, output string, opts ffmpeg.WatermarkOptions) error {
	codec, preset, crf, err := p.policyFor("", "", 0)
	if err != nil {
		return err
	}
	profile := p.profile.WithDefaults()
	return p.run(ctx, request{Operation: "watermark", SourcePath: input, OutputPath: output, OverlayPath: opts.ImagePath, Opacity: opts.Opacity, Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS), KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec: profile.AudioCodec, AudioBitrate: profile.AudioBitrate, SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels)})
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
	codec, preset, crf, err := p.policyFor("", "", 0)
	if err != nil {
		return err
	}
	profile := p.profile.WithDefaults()
	return p.run(ctx, request{Operation: "generate_proxy", SourcePath: input, OutputPath: output, Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS), KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec: profile.AudioCodec, AudioBitrate: profile.AudioBitrate, SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels)})
}

func (p *VideoProcessor) GenerateStoryboard(ctx context.Context, input, output string, intervalFrames, cols, rows int) error {
	return p.run(ctx, request{Operation: "generate_storyboard", SourcePath: input, OutputPath: output, IntervalFrames: uint32(intervalFrames), Columns: uint32(cols), Rows: uint32(rows)})
}

// Cut implements the Stock VideoCutter port through the same client and
// protocol used by every other Rust capability.
func (p *VideoProcessor) Cut(ctx context.Context, req stockpipeline.CutRequest) (stockpipeline.CutBatchResult, error) {
	result := stockpipeline.CutBatchResult{SourcePath: req.SourcePath, Items: make([]stockpipeline.CutItemResult, len(req.Jobs))}
	codec, preset, crf, err := p.policyFor(req.Codec, req.Preset, req.CRF)
	if err != nil {
		return result, err
	}
	profile := p.profile
	if profile == (config.CanonicalVideoProfile{}) {
		profile = config.CanonicalVideoProfile{Width: req.Width, Height: req.Height, FPS: req.FPS, KeyframeInterval: req.KeyframeInterval}
	}
	profile = profile.WithDefaults()
	wire := request{
		Operation: "cut_batch", SourcePath: req.SourcePath, Codec: codec, Preset: preset, CRF: crf,
		Width: uint32(profile.Width), Height: uint32(profile.Height), FPS: uint32(profile.FPS),
		KeyframeInterval: uint32(profile.KeyframeInterval),
		AudioCodec: profile.AudioCodec, AudioBitrate: profile.AudioBitrate,
		SampleRate: uint32(profile.SampleRate), Channels: uint32(profile.Channels), NoAudio: req.NoAudio,
	}
	wireJobs := make([]cutRequestJob, len(req.Jobs))
	for i, job := range req.Jobs {
		result.Items[i] = stockpipeline.CutItemResult{JobID: job.OutputPath, OutputPath: job.OutputPath, Status: stockpipeline.CutItemStatusUnknown}
		wireJobs[i] = cutRequestJob{JobID: job.OutputPath, StartSec: job.StartSec, EndSec: job.EndSec, OutputPath: job.OutputPath}
	}
	wire.Jobs = wireJobs
	response, err := p.client.call(ctx, wire)
	if err != nil {
		return result, err
	}
	byJob := make(map[string]cutItem, len(response.Items))
	for _, item := range response.Items {
		byJob[item.JobID] = item
	}
	for i, job := range req.Jobs {
		item, ok := byJob[job.OutputPath]
		if !ok {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("rust muscles omitted job %q", job.OutputPath)
			continue
		}
		result.Items[i].OutputPath = item.OutputPath
		result.Items[i].SizeBytes = item.SizeBytes
		if (item.Status != "succeeded" && item.Status != "validated") || item.OutputPath == "" {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("rust cut failed: %s", item.Error)
			continue
		}
		result.Items[i].DurationSec = item.DurationSec
		result.Items[i].Status = stockpipeline.CutItemStatusValidated
		size, hash, hashErr := hashOutput(item.OutputPath)
		if hashErr != nil {
			result.Items[i].Status = stockpipeline.CutItemStatusFailed
			result.Items[i].Err = fmt.Errorf("validate rust cut output: %w", hashErr)
			continue
		}
		result.Items[i].SizeBytes, result.Items[i].SHA256Hex = size, hash
	}
	return result, nil
}

func hashOutput(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() == 0 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return 0, "", err
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return 0, "", err
	}
	return info.Size(), fmt.Sprintf("%x", digest.Sum(nil)), nil
}

func (p *VideoProcessor) run(ctx context.Context, req request) error {
	_, err := p.client.call(ctx, req)
	return err
}

// AdminMediaProcessor adapts the Rust capabilities to the operator media
// ports. Drive traversal and publication remain in Go.
