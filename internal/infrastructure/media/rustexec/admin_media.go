package rustexec

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
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
	defer os.Remove(tmpPath)
	_, err := p.client.call(ctx, request{
		Operation: "trim", SourcePath: inputPath, OutputPath: tmpPath,
		MaxDurationSec: maxSeconds, Codec: p.policy.Codec, Preset: p.policy.Preset, CRF: p.policy.CRF,
		Width: uint32(p.profile.Width), Height: uint32(p.profile.Height), FPS: uint32(p.profile.FPS), KeyframeInterval: uint32(p.profile.KeyframeInterval),
		AudioCodec: p.profile.AudioCodec, AudioBitrate: p.profile.AudioBitrate, SampleRate: uint32(p.profile.SampleRate), Channels: uint32(p.profile.Channels),
	})
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, inputPath)
}

func (p *AdminMediaProcessor) Render(ctx context.Context, manifest adminmedia.RenderManifest) error {
	if err := validateResolvedProfile(p.profile); err != nil {
		return err
	}
	req := request{Operation: "admin_render", SourcePath: manifest.Input, OutputPath: manifest.Output, Font: manifest.Font, Codec: p.policy.Codec, Preset: p.policy.Preset, CRF: p.policy.CRF,
		Width: uint32(p.profile.Width), Height: uint32(p.profile.Height), FPS: uint32(p.profile.FPS), KeyframeInterval: uint32(p.profile.KeyframeInterval),
		AudioCodec: p.profile.AudioCodec, AudioBitrate: p.profile.AudioBitrate, SampleRate: uint32(p.profile.SampleRate), Channels: uint32(p.profile.Channels)}
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

// StockRenderer adapts the resolved neutral StockRenderer port to the Rust
// render_stock capability. Go resolves transition IDs and exact effect paths
// before this adapter serializes the request; Rust owns only graph construction
// and execution.
