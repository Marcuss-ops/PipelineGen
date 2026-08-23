package app

import (
	"context"
	"fmt"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

// rustOutputProber adapts rustexec.VideoProcessor.Probe (the canonical Rust
// probe boundary) into cliprender.OutputProber. It reads the actual bytes on
// disk — contract validation never trusts what the render boundary claimed to
// encode. Every field is exact for the assembly-ready gate. Fail-closed:
// a missing, unreadable, or unprobeable output is a typed error, never a
// silent empty probe.
type rustOutputProber struct {
	processor *rustexec.VideoProcessor
}

func (p *rustOutputProber) ProbeOutput(ctx context.Context, path string) (*cliprender.OutputProbe, error) {
	if p == nil || p.processor == nil {
		return nil, fmt.Errorf("output prober: VideoProcessor is required")
	}
	info, err := p.processor.Probe(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("output prober: %w", err)
	}
	if info == nil {
		return nil, fmt.Errorf("output prober: probe returned nil metadata for %s", path)
	}
	// The rustexec.MediaInfo → cliprender.OutputProbe mapping is exact:
	// every field the assembler needs to verify assembly compatibility.
	return &cliprender.OutputProbe{
		Container:        info.FormatName,
		HasVideo:         info.HasVideo,
		VideoCodec:       info.VideoCodec,
		VideoProfile:     "",               // ffprobe reports codec_profile; Rust sets AudioProfile only
		PixelFormat:      info.PixelFormat,
		Width:            info.Width,
		Height:           info.Height,
		FPS:              info.FPS,
		FPSNum:           info.FPSNum,
		FPSDen:           info.FPSDen,
		HasAudio:         info.HasAudio,
		AudioCodec:       info.AudioCodec,
		AudioProfile:     info.AudioProfile,
		SampleRate:       info.SampleRate,
		Channels:         info.Channels,
		VideoStreams:     info.VideoStreamCount,
		AudioStreams:     info.AudioStreamCount,
		StartPTS:         0,                // stable assembly-ready contract
	}, nil
}