package rustexec

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/adminmedia"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"go.uber.org/zap"
)

type AdminMediaProcessor struct {
	client  *Client
	policy  mediaexec.EncoderPolicy
	profile mediaexec.VideoProfile
}

func NewAdminMediaProcessor(binaryPath, ffmpegPath string, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger) *AdminMediaProcessor {
	return NewAdminMediaProcessorWithExecutor(NewExecutor(binaryPath, ffmpegPath, log), policy, profile, log)
}

func NewAdminMediaProcessorWithExecutor(executor *Executor, policy mediaexec.EncoderPolicy, profile mediaexec.VideoProfile, log *zap.Logger) *AdminMediaProcessor {
	return &AdminMediaProcessor{client: NewClientWithExecutor(executor, log), policy: policy, profile: profile.WithDefaults()}
}

func (p *AdminMediaProcessor) Probe(ctx context.Context, path string) (time.Duration, error) {
	info, err := (&VideoProcessor{client: p.client}).Probe(ctx, path)
	if err != nil {
		return 0, err
	}
	return info.Duration, nil
}

func (p *AdminMediaProcessor) Trim(ctx context.Context, inputPath string, maxSeconds float64) error {
	if err := validateResolvedProfile(p.profile); err != nil {
		return err
	}
	ext := filepath.Ext(inputPath)
	tmpPath := inputPath + ".trim.tmp" + ext
	defer func() { _ = os.Remove(tmpPath) }()
	_, err := p.client.call(ctx, request{
		Operation: "trim", SourcePath: inputPath, OutputPath: tmpPath,
		MaxDurationSec: maxSeconds, Codec: p.policy.Codec, Preset: p.policy.Preset, CRF: p.policy.CRF,
		Width: uint32(p.profile.Width), Height: uint32(p.profile.Height), FPSNum: uint32(p.profile.FPSNum), FPSDen: uint32(p.profile.FPSDen), KeyframeInterval: uint32(p.profile.KeyframeInterval),
		AudioCodec: p.profile.AudioCodec, AudioBitrate: p.profile.AudioBitrate, SampleRate: uint32(p.profile.SampleRate), Channels: uint32(p.profile.Channels),
	})
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, inputPath)
}

var _ adminmedia.AudioEditor = (*AdminMediaProcessor)(nil)
