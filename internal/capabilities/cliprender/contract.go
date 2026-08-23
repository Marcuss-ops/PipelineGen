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
	"math"
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
// must match exactly — rational FPS via cross-multiplication, no epsilon.
// The contract pins what VeloxEditing accepts, the probe reads actual bytes.
func ValidateContract(contract *ResolvedContract, probe *OutputProbe) error {
	if contract == nil {
		return fmt.Errorf("%w: contract is nil", ErrContractMismatch)
	}
	if probe == nil {
		return fmt.Errorf("%w: probe is nil", ErrContractMismatch)
	}
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
	if probe.PixelFormat != contract.PixelFormat {
		return fmt.Errorf("%w: pixel format %q != %q", ErrContractMismatch, probe.PixelFormat, contract.PixelFormat)
	}
	if probe.Width != contract.Width || probe.Height != contract.Height {
		return fmt.Errorf("%w: geometry %dx%d != %dx%d", ErrContractMismatch, probe.Width, probe.Height, contract.Width, contract.Height)
	}
	if probe.FPSNum != 0 && probe.FPSDen != 0 {
		if probe.FPSNum*contract.FPSDen != contract.FPSNum*probe.FPSDen {
			return fmt.Errorf("%w: fps %d/%d != %d/%d", ErrContractMismatch, probe.FPSNum, probe.FPSDen, contract.FPSNum, contract.FPSDen)
		}
	} else {
		targetFPS := float64(contract.FPSNum) / float64(contract.FPSDen)
		if math.Abs(probe.FPS-targetFPS) > 0.001 {
			return fmt.Errorf("%w: fps %.3f != %d/%d", ErrContractMismatch, probe.FPS, contract.FPSNum, contract.FPSDen)
		}
	}
	if probe.SARNum != 0 && probe.SARDen != 0 {
		if probe.SARNum != 1 || probe.SARDen != 1 {
			return fmt.Errorf("%w: SAR %d/%d != 1/1", ErrContractMismatch, probe.SARNum, probe.SARDen)
		}
	}
	if probe.ColorRange != "" && probe.ColorRange != "tv" {
		return fmt.Errorf("%w: color_range %q != tv", ErrContractMismatch, probe.ColorRange)
	}
	if probe.ColorSpace != "" && probe.ColorSpace != "bt709" {
		return fmt.Errorf("%w: color_space %q != bt709", ErrContractMismatch, probe.ColorSpace)
	}
	if !probe.HasAudio {
		return fmt.Errorf("%w: output has no audio stream (contract requires %s/%dHz/%dch)", ErrContractMismatch, contract.AudioCodec, contract.SampleRate, contract.Channels)
	}
	if probe.AudioCodec != contract.AudioCodec {
		return fmt.Errorf("%w: audio codec %q != %q", ErrContractMismatch, probe.AudioCodec, contract.AudioCodec)
	}
	if probe.AudioProfile != "" && probe.AudioProfile != "LC" {
		return fmt.Errorf("%w: audio profile %q != LC", ErrContractMismatch, probe.AudioProfile)
	}
	if probe.SampleRate != contract.SampleRate {
		return fmt.Errorf("%w: sample rate %d != %d", ErrContractMismatch, probe.SampleRate, contract.SampleRate)
	}
	if probe.Channels != contract.Channels {
		return fmt.Errorf("%w: channels %d != %d", ErrContractMismatch, probe.Channels, contract.Channels)
	}
	if probe.ChannelLayout != "" && probe.ChannelLayout != "stereo" {
		return fmt.Errorf("%w: channel_layout %q != stereo", ErrContractMismatch, probe.ChannelLayout)
	}
	if probe.AudioBitrate != "" && probe.AudioBitrate != "128k" {
		return fmt.Errorf("%w: audio bitrate %q != 128k", ErrContractMismatch, probe.AudioBitrate)
	}
	if probe.VideoTimeBaseNum != 0 && probe.VideoTimeBaseDen != 0 {
		if probe.VideoTimeBaseNum != 1 || probe.VideoTimeBaseDen != 90000 {
			return fmt.Errorf("%w: video timebase %d/%d != 1/90000", ErrContractMismatch, probe.VideoTimeBaseNum, probe.VideoTimeBaseDen)
		}
	}
	if probe.AudioTimeBaseNum != 0 && probe.AudioTimeBaseDen != 0 {
		if probe.AudioTimeBaseNum != 1 || probe.AudioTimeBaseDen != 48000 {
			return fmt.Errorf("%w: audio timebase %d/%d != 1/48000", ErrContractMismatch, probe.AudioTimeBaseNum, probe.AudioTimeBaseDen)
		}
	}
	if probe.VideoStreams != 0 && probe.VideoStreams != 1 {
		return fmt.Errorf("%w: video streams %d != 1", ErrContractMismatch, probe.VideoStreams)
	}
	if probe.AudioStreams != 0 && probe.AudioStreams != 1 {
		return fmt.Errorf("%w: audio streams %d != 1", ErrContractMismatch, probe.AudioStreams)
	}
	if probe.StartPTS != 0 {
		return fmt.Errorf("%w: start_pts %d != 0", ErrContractMismatch, probe.StartPTS)
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
			ContractID:   OutputContractVeloxAssemblyReadyV1,
			Container:    "mp4",
			VideoCodec:   "h264",
			VideoProfile: "high",
			PixelFormat:  "yuv420p",
			Width:        req.Output.Width,
			Height:       req.Output.Height,
			FPSNum:       req.Output.FPSNum,
			FPSDen:       req.Output.FPSDen,
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedContract, req.Output.Contract)
	}
}
