package cliprender

import (
	"encoding/json"

	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// ResolvedContract holds the canonical assembly contract by value: there is no
// second flat schema to convert to and from, so the two representations cannot
// drift and a new dimension has exactly one declaration site.
//
// What this file owns is the WIRE adapter for documents written before that
// unification. A resume document carries the contract as JSON, and a document
// persisted by an earlier build spells the compound dimensions as flat keys
// (FPSNum, SampleRate, ...). The adapter is read-only: it accepts both
// spellings and never emits the legacy one, so anything this build writes uses
// the canonical shape.

// NewResolvedContract is the only sanctioned way to build a contract that is
// not the frozen default: it starts from the kernel value and lets the caller
// override the dimensions the caller actually selects. Nothing else may
// assemble a contract field by field, so a restated codec/geometry cannot be
// reintroduced through a literal.
func NewResolvedContract(apply func(*kernelmedia.AssemblyMediaContract)) *ResolvedContract {
	contract := kernelmedia.DefaultAssemblyMediaContract()
	if apply != nil {
		apply(&contract)
	}
	return &ResolvedContract{AssemblyMediaContract: contract}
}

// legacyResolvedContract is the retired flat schema, read only to accept a
// document produced by a build that still mirrored the contract. Every field is
// a pointer so that "absent" stays distinguishable from "zero".
type legacyResolvedContract struct {
	ContractID       *string
	FPSNum           *int
	FPSDen           *int
	VideoTimeBaseNum *int
	VideoTimeBaseDen *int
	AudioTimeBaseNum *int
	AudioTimeBaseDen *int
	SARNum           *int
	SARDen           *int
	SampleRate       *int
}

// UnmarshalJSON reads a resolved contract in either spelling. The canonical
// keys win; the legacy flat keys only fill the dimensions the canonical shape
// cannot express under the same name.
func (c *ResolvedContract) UnmarshalJSON(data []byte) error {
	type canonical kernelmedia.AssemblyMediaContract
	var base canonical
	if err := json.Unmarshal(data, &base); err != nil {
		return err
	}
	c.AssemblyMediaContract = kernelmedia.AssemblyMediaContract(base)

	var legacy legacyResolvedContract
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	if legacy.ContractID != nil {
		c.ID = *legacy.ContractID
	}
	if legacy.FPSNum != nil {
		c.FPS.Num = *legacy.FPSNum
	}
	if legacy.FPSDen != nil {
		c.FPS.Den = *legacy.FPSDen
	}
	if legacy.VideoTimeBaseNum != nil {
		c.VideoTimeBase.Num = *legacy.VideoTimeBaseNum
	}
	if legacy.VideoTimeBaseDen != nil {
		c.VideoTimeBase.Den = *legacy.VideoTimeBaseDen
	}
	if legacy.AudioTimeBaseNum != nil {
		c.AudioTimeBase.Num = *legacy.AudioTimeBaseNum
	}
	if legacy.AudioTimeBaseDen != nil {
		c.AudioTimeBase.Den = *legacy.AudioTimeBaseDen
	}
	if legacy.SARNum != nil {
		c.SAR.Num = *legacy.SARNum
	}
	if legacy.SARDen != nil {
		c.SAR.Den = *legacy.SARDen
	}
	if legacy.SampleRate != nil {
		c.AudioSampleRate = *legacy.SampleRate
	}
	if c.Version == 0 {
		c.Version = kernelmedia.AssemblyMediaContractVersion
		if c.ID == kernelmedia.AssemblyMediaContractV2ID {
			c.Version = kernelmedia.AssemblyMediaContractV2Version
		}
	}
	return nil
}

// ContractWireBlock is the flat contract block the clip.render result reports
// to its workflow boundary. It is the ONLY place that projection is written: the
// values come from the canonical contract, so the wire keys cannot describe a
// different stream than the one the renderer was held to.
//
// The flat spelling is a published wire shape (the final-job handoff carries it
// into the remote assembler gate, which compares it segment to segment); keeping
// the projection in one function is what stops it from being restated per site.
func ContractWireBlock(c *ResolvedContract) map[string]any {
	if c == nil {
		return map[string]any{}
	}
	return map[string]any{
		"width":                  c.Width,
		"height":                 c.Height,
		"fps_num":                c.FPS.Num,
		"fps_den":                c.FPS.Den,
		"video_codec":            c.VideoCodec,
		"audio_codec":            c.AudioCodec,
		"pixel_format":           c.PixelFormat,
		"video_extradata_sha256": c.VideoExtradataSHA256,
	}
}
