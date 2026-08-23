package media

import (
	"context"
	"fmt"
)

// AssemblyInput is one clip to be assembled.
type AssemblyInput struct {
	Path      string
	Probe     *ProbeFacts // from OutputProbe / StreamSignature
	ContractID string
	Signature string // StreamSignature Fingerprint
}

// ProbeFacts is the minimal probe needed for gate (from OutputProbe).
type ProbeFacts struct {
	Container   string
	VideoCodec  string
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
	VideoProfile   string
	AudioCodec     string
	AudioSampleRate int
	Channels       int
	VideoStreams int
	AudioStreams int
	StartPTS int64
}

var ErrAssemblyInputContractMismatch = fmt.Errorf("assembly: input contract mismatch")

// AssemblyCompatibilityGate checks all inputs have identical contract and stream signature.
// Fail-closed: any mismatch → ErrAssemblyInputContractMismatch, never re-encode.
func AssemblyCompatibilityGate(ctx context.Context, inputs []AssemblyInput) error {
	if len(inputs) == 0 {
		return fmt.Errorf("%w: no inputs", ErrAssemblyInputContractMismatch)
	}
	first := inputs[0]
	if first.ContractID != AssemblyReadyVideoContractID {
		return fmt.Errorf("%w: contract %q != %q", ErrAssemblyInputContractMismatch, first.ContractID, AssemblyReadyVideoContractID)
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
		if p.ColorRange != fp.ColorRange || p.ColorSpace != fp.ColorSpace {
			return fmt.Errorf("%w: input %d color %s/%s != %s/%s", ErrAssemblyInputContractMismatch, i, p.ColorRange, p.ColorSpace, fp.ColorRange, fp.ColorSpace)
		}
		if p.VideoProfile != fp.VideoProfile {
			return fmt.Errorf("%w: input %d profile %q != %q", ErrAssemblyInputContractMismatch, i, p.VideoProfile, fp.VideoProfile)
		}
		if p.AudioCodec != fp.AudioCodec || p.AudioSampleRate != fp.AudioSampleRate || p.Channels != fp.Channels {
			return fmt.Errorf("%w: input %d audio %s/%d/%d != %s/%d/%d", ErrAssemblyInputContractMismatch, i, p.AudioCodec, p.AudioSampleRate, p.Channels, fp.AudioCodec, fp.AudioSampleRate, fp.Channels)
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
