package cliprender

import (
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// ── Assembly-ready contract SSOT bridge ───────────────────────────────
//
// ResolvedContract is the clip.render capability's working representation of
// the assembly-ready video contract. The single source of truth for the
// frozen values is kernel/media.VideoContract (AssemblyReadyVideoContractID =
// "VELOX_ASSEMBLY_READY_V1"). This bridge keeps the two representations in
// sync without a hard type dependency in every consumer.
//
// All frozen values are canonically owned by kernel/media. ResolvedContract
// mirrors them with flat int fields for ergonomic consumption inside the
// clip.render capability (performance + simpler struct literals). Future
// changes to the assembly-ready spec MUST be reflected in
// kernel/media.DefaultAssemblyReadyVideoContract() FIRST, then propagated
// here via ToVideoContract ↔ FromVideoContract.

// ToVideoContract converts the clip.render working contract to the canonical
// kernel/media.VideoContract SSOT.
func (c *ResolvedContract) ToVideoContract() kernelmedia.VideoContract {
	if c == nil {
		return kernelmedia.DefaultAssemblyReadyVideoContract()
	}
	return kernelmedia.VideoContract{
		ID:                 c.ContractID,
		Version:            kernelmedia.AssemblyReadyVideoVersion,
		Container:          c.Container,
		VideoCodec:         c.VideoCodec,
		VideoProfile:       c.VideoProfile,
		VideoLevel:         c.VideoLevel,
		PixelFormat:        c.PixelFormat,
		Width:              c.Width,
		Height:             c.Height,
		FPS:                kernelmedia.FrameRate{Num: c.FPSNum, Den: c.FPSDen},
		VideoTimeBase:      kernelmedia.Rational{Num: c.VideoTimeBaseNum, Den: c.VideoTimeBaseDen},
		AudioTimeBase:      kernelmedia.Rational{Num: c.AudioTimeBaseNum, Den: c.AudioTimeBaseDen},
		SAR:                kernelmedia.Rational{Num: c.SARNum, Den: c.SARDen},
		ColorRange:         c.ColorRange,
		ColorSpace:         c.ColorSpace,
		ColorTransfer:      c.ColorTransfer,
		ColorPrimaries:     c.ColorPrimaries,
		FieldOrder:         c.FieldOrder,
		KeyframeInterval:   c.KeyframeInterval,
		AudioCodec:         c.AudioCodec,
		AudioProfile:       c.AudioProfile,
		AudioSampleRate:    c.SampleRate,
		AudioChannels:      c.Channels,
		AudioChannelLayout: c.AudioChannelLayout,
		AudioBitrate:       c.AudioBitrate,
		VideoStreams:       c.VideoStreams,
		AudioStreams:       c.AudioStreams,
		StartPTS:           c.StartPTS,
	}
}

// FromVideoContract populates a ResolvedContract from the canonical
// kernel/media.VideoContract SSOT.
func FromVideoContract(vc kernelmedia.VideoContract) *ResolvedContract {
	return &ResolvedContract{
		ContractID:         vc.ID,
		Container:          vc.Container,
		VideoCodec:         vc.VideoCodec,
		VideoProfile:       vc.VideoProfile,
		VideoLevel:         vc.VideoLevel,
		PixelFormat:        vc.PixelFormat,
		Width:              vc.Width,
		Height:             vc.Height,
		FPSNum:             vc.FPS.Num,
		FPSDen:             vc.FPS.Den,
		VideoTimeBaseNum:   vc.VideoTimeBase.Num,
		VideoTimeBaseDen:   vc.VideoTimeBase.Den,
		AudioTimeBaseNum:   vc.AudioTimeBase.Num,
		AudioTimeBaseDen:   vc.AudioTimeBase.Den,
		SARNum:             vc.SAR.Num,
		SARDen:             vc.SAR.Den,
		ColorRange:         vc.ColorRange,
		ColorSpace:         vc.ColorSpace,
		ColorTransfer:      vc.ColorTransfer,
		ColorPrimaries:     vc.ColorPrimaries,
		FieldOrder:         vc.FieldOrder,
		KeyframeInterval:   vc.KeyframeInterval,
		AudioCodec:         vc.AudioCodec,
		AudioProfile:       vc.AudioProfile,
		SampleRate:         vc.AudioSampleRate,
		Channels:           vc.AudioChannels,
		AudioChannelLayout: vc.AudioChannelLayout,
		AudioBitrate:       vc.AudioBitrate,
		VideoStreams:       vc.VideoStreams,
		AudioStreams:       vc.AudioStreams,
		StartPTS:           vc.StartPTS,
	}
}
