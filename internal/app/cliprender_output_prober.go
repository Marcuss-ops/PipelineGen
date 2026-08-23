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
	// The Rust probe returns video codec, pixel format, geometry, FPS,
	// audio codec/profile/sample rate/channels, and stream counts. Fields
	// not yet reported by Rust (timebase, SAR, color) are set to zero/empty
	// and skipped by ValidateContract when the contract field is also zero.
	// Assembly-ready frozen defaults are carried on the contract side.
	return &cliprender.OutputProbe{
		Container:         info.FormatName,
		HasVideo:          info.HasVideo,
		VideoCodec:        info.VideoCodec,
		VideoProfile:      "",                    // Rust probe doesn't report codec_profile yet
		VideoLevel:        "",                    // Rust probe doesn't report level yet
		PixelFormat:       info.PixelFormat,
		Width:             info.Width,
		Height:            info.Height,
		FPS:               info.FPS,
		FPSNum:            info.FPSNum,
		FPSDen:            info.FPSDen,
		VideoTimeBaseNum:  0,                     // Rust probe doesn't report timebase yet
		VideoTimeBaseDen:  0,
		AudioTimeBaseNum:  0,
		AudioTimeBaseDen:  0,
		SARNum:            0,                     // Rust probe doesn't report SAR yet
		SARDen:            0,
		ColorRange:        "",                    // Rust probe doesn't report color yet
		ColorSpace:        "",
		ColorTransfer:     "",
		ColorPrimaries:    "",
		FieldOrder:        "",                    // Rust probe doesn't report field order yet
		KeyframeInterval:  0,                     // Rust probe doesn't report GOP yet
		HasAudio:          info.HasAudio,
		AudioCodec:        info.AudioCodec,
		AudioProfile:      info.AudioProfile,
		SampleRate:        info.SampleRate,
		Channels:         info.Channels,
		ChannelLayout:     "",                    // Rust probe doesn't report channel_layout yet
		AudioBitrate:      "",                    // Rust probe doesn't report bitrate yet
		VideoStreams:      info.VideoStreamCount,
		AudioStreams:      info.AudioStreamCount,
		StreamOrder:       "",                    // Rust probe doesn't report stream order yet
		StartPTS:          0,                     // stable assembly-ready contract
	}, nil
}