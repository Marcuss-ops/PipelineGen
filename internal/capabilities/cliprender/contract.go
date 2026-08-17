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
