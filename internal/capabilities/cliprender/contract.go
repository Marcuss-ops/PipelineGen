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
// (video codec, pixel format, geometry, fps, audio codec/rate/channels) must
// match exactly — the contract pins what VeloxEditing accepts, and the
// probe reads the actual bytes on disk (never what the render boundary
// claimed to encode).
func ValidateContract(contract *ResolvedContract, probe *OutputProbe) error {
	if contract == nil {
		return fmt.Errorf("%w: contract is nil", ErrContractMismatch)
	}
	if probe == nil {
		return fmt.Errorf("%w: probe is nil", ErrContractMismatch)
	}
	if !probe.HasVideo {
		return fmt.Errorf("%w: output has no video stream", ErrContractMismatch)
	}
	if probe.VideoCodec != contract.VideoCodec {
		return fmt.Errorf("%w: video codec %q != %q", ErrContractMismatch, probe.VideoCodec, contract.VideoCodec)
	}
	if probe.PixelFormat != contract.PixelFormat {
		return fmt.Errorf("%w: pixel format %q != %q", ErrContractMismatch, probe.PixelFormat, contract.PixelFormat)
	}
	if probe.Width != contract.Width || probe.Height != contract.Height {
		return fmt.Errorf("%w: geometry %dx%d != %dx%d", ErrContractMismatch, probe.Width, probe.Height, contract.Width, contract.Height)
	}
	if math.Abs(probe.FPS-float64(contract.FPS)) > 0.5 {
		return fmt.Errorf("%w: fps %.3f != %d", ErrContractMismatch, probe.FPS, contract.FPS)
	}
	if !probe.HasAudio {
		return fmt.Errorf("%w: output has no audio stream (contract requires %s/%dHz/%dch)", ErrContractMismatch, contract.AudioCodec, contract.SampleRate, contract.Channels)
	}
	if probe.AudioCodec != contract.AudioCodec {
		return fmt.Errorf("%w: audio codec %q != %q", ErrContractMismatch, probe.AudioCodec, contract.AudioCodec)
	}
	if probe.SampleRate != contract.SampleRate {
		return fmt.Errorf("%w: sample rate %d != %d", ErrContractMismatch, probe.SampleRate, contract.SampleRate)
	}
	if probe.Channels != contract.Channels {
		return fmt.Errorf("%w: channels %d != %d", ErrContractMismatch, probe.Channels, contract.Channels)
	}
	return nil
}

// NewContractResolver returns the canonical ContractResolver. It supports
// OutputContractVeloxEditingClipV1 today; a future contract adds a case here
// (the single resolution owner) rather than duplicating codec settings.
func NewContractResolver() ContractResolver {
	return defaultContractResolver{}
}

type defaultContractResolver struct{}

func (defaultContractResolver) Resolve(_ context.Context, req *RenderRequest) (*ResolvedContract, error) {
	if req == nil || req.Output == nil {
		return nil, fmt.Errorf("%w: request output block is missing", ErrUnsupportedContract)
	}
	switch req.Output.Contract {
	case OutputContractVeloxEditingClipV1:
		return &ResolvedContract{
			ContractID:   OutputContractVeloxEditingClipV1,
			Container:    "mp4",
			VideoCodec:   "h264",
			VideoProfile: "high",
			PixelFormat:  "yuv420p",
			Width:        req.Output.Width,
			Height:       req.Output.Height,
			FPS:          req.Output.FPS,
			AudioCodec:   "aac",
			SampleRate:   48000,
			Channels:     2,
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedContract, req.Output.Contract)
	}
}
