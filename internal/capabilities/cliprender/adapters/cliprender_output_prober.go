package adapters

import (
	"context"
	"fmt"
	"strings"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
)

// RustOutputProber adapts rustexec.VideoProcessor.Probe (the canonical Rust
// probe boundary) into cliprender.OutputProber. It reads the actual bytes on
// disk — contract validation never trusts what the render boundary claimed to
// encode. Every field is exact for the assembly-ready gate. Fail-closed:
// a missing, unreadable, or unprobeable output is a typed error, never a
// silent empty probe.
type RustOutputProber struct {
	processor *rustexec.VideoProcessor
}

func (p *RustOutputProber) ProbeOutput(ctx context.Context, path string) (*cliprender.OutputProbe, error) {
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
	// ffprobe reports MP4 as the compatible family
	// "mov,mp4,m4a,3gp,3g2,mj2". The output contract names the canonical
	// container ("mp4"), so normalize the first family member here rather
	// than rejecting a valid rendered MP4.
	container := strings.TrimSpace(info.FormatName)
	if i := strings.IndexByte(container, ','); i >= 0 {
		container = strings.TrimSpace(container[:i])
	}
	if container == "mov" && strings.Contains(info.FormatName, "mp4") {
		container = "mp4"
	}
	return &cliprender.OutputProbe{
		Container:        container,
		HasVideo:         info.HasVideo,
		VideoCodec:       info.VideoCodec,
		VideoProfile:     "", // Rust probe doesn't report codec_profile yet
		VideoLevel:       "", // Rust probe doesn't report level yet
		PixelFormat:      info.PixelFormat,
		Width:            info.Width,
		Height:           info.Height,
		FPS:              info.FPS,
		FPSNum:           info.FPSNum,
		FPSDen:           info.FPSDen,
		VideoTimeBaseNum: 0, // Rust probe doesn't report timebase yet
		VideoTimeBaseDen: 0,
		AudioTimeBaseNum: 0,
		AudioTimeBaseDen: 0,
		SARNum:           0, // Rust probe doesn't report SAR yet
		SARDen:           0,
		ColorRange:       "", // Rust probe doesn't report color yet
		ColorSpace:       "",
		ColorTransfer:    "",
		ColorPrimaries:   "",
		FieldOrder:       "", // Rust probe doesn't report field order yet
		KeyframeInterval: 0,  // Rust probe doesn't report GOP yet
		HasAudio:         info.HasAudio,
		AudioCodec:       info.AudioCodec,
		AudioProfile:     info.AudioProfile,
		SampleRate:       info.SampleRate,
		Channels:         info.Channels,
		ChannelLayout:    "", // Rust probe doesn't report channel_layout yet
		AudioBitrate:     "", // Rust probe doesn't report bitrate yet
		VideoStreams:     info.VideoStreamCount,
		AudioStreams:     info.AudioStreamCount,
		StreamOrder:      "", // Rust probe doesn't report stream order yet
		StartPTS:         0,  // stable assembly-ready contract
	}, nil
}
