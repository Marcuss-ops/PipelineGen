package cliprender

// contract.go owns the canonical VeloxEditing output contract resolution.
// The precise codec/pixel/timebase values live HERE (single canonical owner,
// per the feature spec §12 "VeloxEditing compatibility contract") — the
// request only selects the contract ID + resolution/fps, and no other file
// duplicates these settings.
//
// The contract is a pure function of the request: no I/O, no ports. It is
// the one preparation phase that requires no adapter.

import (
	"context"
	"errors"
	"fmt"
)

// ErrUnsupportedContract is the typed sentinel for an output.contract ID the
// capability does not know. Fail-closed: an unknown contract is never
// silently mapped to a default.
var ErrUnsupportedContract = errors.New("clip.render: unsupported output contract")

// ErrContractMismatch is returned when the probed output does not satisfy
// the resolved VeloxEditing contract on any dimension. Fail-closed: a
// rendered file that violates the contract is never published as a derived
// asset — the single-pass render is re-run or the job fails, never silently
// accepted.
var ErrContractMismatch = errors.New("clip.render: rendered output violates the output contract")

// ValidateContract is the pure post-render contract gate: it compares the
// probed output facts against the fully-resolved contract. Every dimension
// must match exactly — rational FPS/SAR/timebase via cross-multiplication,
// no float epsilon anywhere. No value is hardcoded; every check reads from
// the contract struct so the SSOT is the Resolve() function.
func ValidateContract(contract *ResolvedContract, probe *OutputProbe) error {
	if contract == nil {
		return fmt.Errorf("%w: contract is nil", ErrContractMismatch)
	}
	if probe == nil {
		return fmt.Errorf("%w: probe is nil", ErrContractMismatch)
	}
	// Container and video identity.
	if probe.Container != "" && probe.Container != contract.Container {
		return fmt.Errorf("%w: container %q != %q", ErrContractMismatch, probe.Container, contract.Container)
	}
	if !probe.HasVideo {
		return fmt.Errorf("%w: output has no video stream", ErrContractMismatch)
	}
	if probe.VideoCodec != contract.VideoCodec {
		return fmt.Errorf("%w: video codec %q != %q", ErrContractMismatch, probe.VideoCodec, contract.VideoCodec)
	}
	if probe.VideoProfile != "" && probe.VideoProfile != contract.VideoProfile {
		return fmt.Errorf("%w: video profile %q != %q", ErrContractMismatch, probe.VideoProfile, contract.VideoProfile)
	}
	if probe.VideoLevel != "" && probe.VideoLevel != contract.VideoLevel {
		return fmt.Errorf("%w: video level %q != %q", ErrContractMismatch, probe.VideoLevel, contract.VideoLevel)
	}
	if probe.PixelFormat != contract.PixelFormat {
		return fmt.Errorf("%w: pixel format %q != %q", ErrContractMismatch, probe.PixelFormat, contract.PixelFormat)
	}
	// Geometry.
	if probe.Width != contract.Width || probe.Height != contract.Height {
		return fmt.Errorf("%w: geometry %dx%d != %dx%d", ErrContractMismatch, probe.Width, probe.Height, contract.Width, contract.Height)
	}
	// FPS: exact rational via cross-multiplication — no float epsilon.
	if contract.FPSNum > 0 && contract.FPSDen > 0 {
		if probe.FPSNum*contract.FPSDen != contract.FPSNum*probe.FPSDen {
			return fmt.Errorf("%w: fps %d/%d != %d/%d", ErrContractMismatch, probe.FPSNum, probe.FPSDen, contract.FPSNum, contract.FPSDen)
		}
	}
	// Timebase: exact rational.
	if contract.VideoTimeBaseNum > 0 && contract.VideoTimeBaseDen > 0 {
		if probe.VideoTimeBaseNum != 0 && probe.VideoTimeBaseDen != 0 {
			if probe.VideoTimeBaseNum*contract.VideoTimeBaseDen != contract.VideoTimeBaseNum*probe.VideoTimeBaseDen {
				return fmt.Errorf("%w: video timebase %d/%d != %d/%d", ErrContractMismatch, probe.VideoTimeBaseNum, probe.VideoTimeBaseDen, contract.VideoTimeBaseNum, contract.VideoTimeBaseDen)
			}
		}
	}
	if contract.AudioTimeBaseNum > 0 && contract.AudioTimeBaseDen > 0 {
		if probe.AudioTimeBaseNum != 0 && probe.AudioTimeBaseDen != 0 {
			if probe.AudioTimeBaseNum*contract.AudioTimeBaseDen != contract.AudioTimeBaseNum*probe.AudioTimeBaseDen {
				return fmt.Errorf("%w: audio timebase %d/%d != %d/%d", ErrContractMismatch, probe.AudioTimeBaseNum, probe.AudioTimeBaseDen, contract.AudioTimeBaseNum, contract.AudioTimeBaseDen)
			}
		}
	}
	// SAR: exact rational.
	if contract.SARNum > 0 && contract.SARDen > 0 {
		if probe.SARNum != 0 && probe.SARDen != 0 {
			if probe.SARNum*contract.SARDen != contract.SARNum*probe.SARDen {
				return fmt.Errorf("%w: SAR %d/%d != %d/%d", ErrContractMismatch, probe.SARNum, probe.SARDen, contract.SARNum, contract.SARDen)
			}
		}
	}
	// Color metadata.
	if contract.ColorRange != "" && probe.ColorRange != "" && probe.ColorRange != contract.ColorRange {
		return fmt.Errorf("%w: color_range %q != %q", ErrContractMismatch, probe.ColorRange, contract.ColorRange)
	}
	if contract.ColorSpace != "" && probe.ColorSpace != "" && probe.ColorSpace != contract.ColorSpace {
		return fmt.Errorf("%w: color_space %q != %q", ErrContractMismatch, probe.ColorSpace, contract.ColorSpace)
	}
	if contract.ColorTransfer != "" && probe.ColorTransfer != "" && probe.ColorTransfer != contract.ColorTransfer {
		return fmt.Errorf("%w: color_transfer %q != %q", ErrContractMismatch, probe.ColorTransfer, contract.ColorTransfer)
	}
	if contract.ColorPrimaries != "" && probe.ColorPrimaries != "" && probe.ColorPrimaries != contract.ColorPrimaries {
		return fmt.Errorf("%w: color_primaries %q != %q", ErrContractMismatch, probe.ColorPrimaries, contract.ColorPrimaries)
	}
	if contract.FieldOrder != "" && probe.FieldOrder != "" && probe.FieldOrder != contract.FieldOrder {
		return fmt.Errorf("%w: field_order %q != %q", ErrContractMismatch, probe.FieldOrder, contract.FieldOrder)
	}
	// GOP.
	if contract.KeyframeInterval > 0 && probe.KeyframeInterval != 0 && probe.KeyframeInterval != contract.KeyframeInterval {
		return fmt.Errorf("%w: keyframe interval %d != %d", ErrContractMismatch, probe.KeyframeInterval, contract.KeyframeInterval)
	}
	// Audio contract.
	if contract.AudioStreams > 0 {
		if !probe.HasAudio {
			return fmt.Errorf("%w: output has no audio stream (contract requires %s/%dHz/%dch)", ErrContractMismatch, contract.AudioCodec, contract.SampleRate, contract.Channels)
		}
		if probe.AudioCodec != contract.AudioCodec {
			return fmt.Errorf("%w: audio codec %q != %q", ErrContractMismatch, probe.AudioCodec, contract.AudioCodec)
		}
		if contract.AudioProfile != "" && probe.AudioProfile != "" && probe.AudioProfile != contract.AudioProfile {
			return fmt.Errorf("%w: audio profile %q != %q", ErrContractMismatch, probe.AudioProfile, contract.AudioProfile)
		}
		if probe.SampleRate != contract.SampleRate {
			return fmt.Errorf("%w: sample rate %d != %d", ErrContractMismatch, probe.SampleRate, contract.SampleRate)
		}
		if probe.Channels != contract.Channels {
			return fmt.Errorf("%w: channels %d != %d", ErrContractMismatch, probe.Channels, contract.Channels)
		}
		if contract.AudioChannelLayout != "" && probe.ChannelLayout != "" && probe.ChannelLayout != contract.AudioChannelLayout {
			return fmt.Errorf("%w: channel_layout %q != %q", ErrContractMismatch, probe.ChannelLayout, contract.AudioChannelLayout)
		}
		if contract.AudioBitrate != "" && probe.AudioBitrate != "" && probe.AudioBitrate != contract.AudioBitrate {
			return fmt.Errorf("%w: audio bitrate %q != %q", ErrContractMismatch, probe.AudioBitrate, contract.AudioBitrate)
		}
	}
	// Stream layout.
	if contract.VideoStreams > 0 && probe.VideoStreams != 0 && probe.VideoStreams != contract.VideoStreams {
		return fmt.Errorf("%w: video streams %d != %d", ErrContractMismatch, probe.VideoStreams, contract.VideoStreams)
	}
	if contract.AudioStreams > 0 && probe.AudioStreams != 0 && probe.AudioStreams != contract.AudioStreams {
		return fmt.Errorf("%w: audio streams %d != %d", ErrContractMismatch, probe.AudioStreams, contract.AudioStreams)
	}
	// StartPTS (always 0 for assembly-ready).
	if probe.StartPTS != contract.StartPTS {
		return fmt.Errorf("%w: start_pts %d != %d", ErrContractMismatch, probe.StartPTS, contract.StartPTS)
	}
	return nil
}

// NewContractResolver returns the canonical ContractResolver. Single SSOT:
// VELOX_ASSEMBLY_READY_V1 (24/1, 1/90000, GOP48). Old velox-editing-clip-v1 is alias.
func NewContractResolver() ContractResolver {
	return defaultContractResolver{}
}

type defaultContractResolver struct{}

func (defaultContractResolver) Resolve(_ context.Context, req *RenderRequest) (*ResolvedContract, error) {
	if req == nil || req.Output == nil {
		return nil, fmt.Errorf("%w: request output block is missing", ErrUnsupportedContract)
	}
	switch req.Output.Contract {
	case OutputContractVeloxAssemblyReadyV1, OutputContractVeloxEditingClipV1:
		return &ResolvedContract{
			ContractID:         OutputContractVeloxAssemblyReadyV1,
			Container:          "mp4",
			VideoCodec:         "h264",
			VideoProfile:       "high",
			VideoLevel:         "4.0",
			PixelFormat:        "yuv420p",
			Width:              req.Output.Width,
			Height:             req.Output.Height,
			FPSNum:             req.Output.FPSNum,
			FPSDen:             req.Output.FPSDen,
			VideoTimeBaseNum:   1,
			VideoTimeBaseDen:   90000,
			AudioTimeBaseNum:   1,
			AudioTimeBaseDen:   48000,
			SARNum:             1,
			SARDen:             1,
			ColorRange:         "tv",
			ColorSpace:         "bt709",
			ColorTransfer:      "bt709",
			ColorPrimaries:     "bt709",
			FieldOrder:         "progressive",
			KeyframeInterval:   48,
			AudioCodec:         "aac",
			AudioProfile:        "LC",
			SampleRate:         48000,
			Channels:           2,
			AudioChannelLayout: "stereo",
			AudioBitrate:        "128k",
			VideoStreams:       1,
			AudioStreams:       1,
			StartPTS:           0,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedContract, req.Output.Contract)
	}
}
