package media

import (
	"context"
	"fmt"
)

// AssemblyInput is one clip to be assembled.
type AssemblyInput struct {
	Path        string
	Probe       *ProbeFacts          // from OutputProbe / StreamSignature
	ContractID  string
	Signature   string               // StreamSignature Fingerprint
	Composition *CompositionContract  // editorial facts (watermark, subtitles, overlay, zoom, scale)
}

// ProbeFacts is the minimal probe needed for gate (from OutputProbe).
// ProbeFacts is the minimal probe needed for gate (from OutputProbe).
type ProbeFacts struct {
	Container   string
	VideoCodec  string
	VideoProfile string
	VideoLevel  string
	PixelFormat string
	Width       int
	Height      int
	FPSNum      int
	FPSDen      int
	SARNum      int
	SARDen      int
	VideoTimeBaseNum int
	VideoTimeBaseDen int
	AudioTimeBaseNum int
	AudioTimeBaseDen int
	ColorRange     string
	ColorSpace     string
	ColorTransfer  string
	ColorPrimaries string
	FieldOrder     string
	KeyframeInterval int
	AudioCodec     string
	AudioProfile   string
	AudioSampleRate int
	Channels       int
	AudioChannelLayout string
	AudioBitrate       string
	VideoStreams int
	AudioStreams int
	StartPTS int64
}

var ErrAssemblyInputContractMismatch = fmt.Errorf("assembly: input contract mismatch")
var ErrAssemblyCompositionRejected = fmt.Errorf("assembly: input composition rejected")

// AssemblyCompatibilityGate checks all inputs have identical contract and stream signature.
// Fail-closed: any mismatch → ErrAssemblyInputContractMismatch, never re-encode.
// Additionally checks CompositionContract consistency (all inputs must have the same
// composition facts).
func AssemblyCompatibilityGate(ctx context.Context, inputs []AssemblyInput) error {
	if len(inputs) == 0 {
		return fmt.Errorf("%w: no inputs", ErrAssemblyInputContractMismatch)
	}
	first := inputs[0]
	if first.ContractID != AssemblyMediaContractID {
		return fmt.Errorf("%w: contract %q != %q", ErrAssemblyInputContractMismatch, first.ContractID, AssemblyMediaContractID)
	}

	// ── Composition gate: all inputs must share the same composition facts ──
	if first.Composition != nil {
		for i, in := range inputs {
			if in.Composition == nil {
				return fmt.Errorf("%w: input %d missing composition contract", ErrAssemblyCompositionRejected, i)
			}
			if in.Composition.WatermarkApplied != first.Composition.WatermarkApplied {
				return fmt.Errorf("%w: input %d watermark_applied mismatch", ErrAssemblyCompositionRejected, i)
			}
			if in.Composition.SubtitlesBurned != first.Composition.SubtitlesBurned {
				return fmt.Errorf("%w: input %d subtitles_burned mismatch", ErrAssemblyCompositionRejected, i)
			}
			if in.Composition.OverlayApplied != first.Composition.OverlayApplied {
				return fmt.Errorf("%w: input %d overlay_applied mismatch", ErrAssemblyCompositionRejected, i)
			}
			if in.Composition.SlowZoom != first.Composition.SlowZoom {
				return fmt.Errorf("%w: input %d slow_zoom mismatch", ErrAssemblyCompositionRejected, i)
			}
			if in.Composition.ScaleMode != first.Composition.ScaleMode {
				return fmt.Errorf("%w: input %d scale_mode %q != %q", ErrAssemblyCompositionRejected, i, in.Composition.ScaleMode, first.Composition.ScaleMode)
			}
		}
	}

	for i, in := range inputs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if in.ContractID != first.ContractID {
			return fmt.Errorf("%w: input %d contract %q != %q", ErrAssemblyInputContractMismatch, i, in.ContractID, first.ContractID)
		}
		if in.Signature != "" && first.Signature != "" && in.Signature != first.Signature {
			return fmt.Errorf("%w: input %d stream_signature %q != %q", ErrAssemblyInputContractMismatch, i, in.Signature, first.Signature)
		}
		if in.Probe == nil {
			return fmt.Errorf("%w: input %d missing probe", ErrAssemblyInputContractMismatch, i)
		}
		p := in.Probe
		fp := first.Probe
		if p.Container != fp.Container || p.VideoCodec != fp.VideoCodec || p.PixelFormat != fp.PixelFormat {
			return fmt.Errorf("%w: input %d codec/container mismatch", ErrAssemblyInputContractMismatch, i)
		}
		if p.VideoProfile != fp.VideoProfile {
			return fmt.Errorf("%w: input %d video profile %q != %q", ErrAssemblyInputContractMismatch, i, p.VideoProfile, fp.VideoProfile)
		}
		if p.Width != fp.Width || p.Height != fp.Height {
			return fmt.Errorf("%w: input %d geometry %dx%d != %dx%d", ErrAssemblyInputContractMismatch, i, p.Width, p.Height, fp.Width, fp.Height)
		}
		if p.FPSNum*fp.FPSDen != fp.FPSNum*p.FPSDen {
			return fmt.Errorf("%w: input %d fps %d/%d != %d/%d", ErrAssemblyInputContractMismatch, i, p.FPSNum, p.FPSDen, fp.FPSNum, fp.FPSDen)
		}
		if p.SARNum*fp.SARDen != fp.SARNum*p.SARDen {
			return fmt.Errorf("%w: input %d SAR %d/%d != %d/%d", ErrAssemblyInputContractMismatch, i, p.SARNum, p.SARDen, fp.SARNum, fp.SARDen)
		}
		if p.VideoTimeBaseNum*fp.VideoTimeBaseDen != fp.VideoTimeBaseNum*p.VideoTimeBaseDen {
			return fmt.Errorf("%w: input %d video timebase %d/%d != %d/%d", ErrAssemblyInputContractMismatch, i, p.VideoTimeBaseNum, p.VideoTimeBaseDen, fp.VideoTimeBaseNum, fp.VideoTimeBaseDen)
		}
		if p.ColorRange != fp.ColorRange || p.ColorSpace != fp.ColorSpace || p.ColorTransfer != fp.ColorTransfer || p.ColorPrimaries != fp.ColorPrimaries {
			return fmt.Errorf("%w: input %d color %s/%s/%s/%s != %s/%s/%s/%s", ErrAssemblyInputContractMismatch, i, p.ColorRange, p.ColorSpace, p.ColorTransfer, p.ColorPrimaries, fp.ColorRange, fp.ColorSpace, fp.ColorTransfer, fp.ColorPrimaries)
		}
		if p.AudioCodec != fp.AudioCodec || p.AudioProfile != fp.AudioProfile || p.AudioSampleRate != fp.AudioSampleRate || p.Channels != fp.Channels || p.AudioChannelLayout != fp.AudioChannelLayout {
			return fmt.Errorf("%w: input %d audio %s/%s/%d/%d/%s != %s/%s/%d/%d/%s", ErrAssemblyInputContractMismatch, i, p.AudioCodec, p.AudioProfile, p.AudioSampleRate, p.Channels, p.AudioChannelLayout, fp.AudioCodec, fp.AudioProfile, fp.AudioSampleRate, fp.Channels, fp.AudioChannelLayout)
		}
		if p.VideoStreams != fp.VideoStreams || p.AudioStreams != fp.AudioStreams {
			return fmt.Errorf("%w: input %d stream count v%d/a%d != v%d/a%d", ErrAssemblyInputContractMismatch, i, p.VideoStreams, p.AudioStreams, fp.VideoStreams, fp.AudioStreams)
		}
		if p.StartPTS != fp.StartPTS {
			return fmt.Errorf("%w: input %d start_pts %d != %d", ErrAssemblyInputContractMismatch, i, p.StartPTS, fp.StartPTS)
		}
	}
	return nil
}
