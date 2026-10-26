package media

import (
	"fmt"
)

// AssemblyInput is one clip to be assembled.
type AssemblyInput struct {
	Path        string
	Probe       *ProbeFacts // from OutputProbe / StreamSignature
	ContractID  string
	Signature   string               // StreamSignature Fingerprint
	Composition *CompositionContract // editorial facts (watermark, subtitles, overlay, zoom, scale)
}

// ProbeFacts is the minimal probe needed for gate (from OutputProbe).
// ProbeFacts is the minimal probe needed for gate (from OutputProbe).
type ProbeFacts struct {
	Container          string
	VideoCodec         string
	VideoProfile       string
	VideoLevel         string
	PixelFormat        string
	Width              int
	Height             int
	FPSNum             int
	FPSDen             int
	SARNum             int
	SARDen             int
	VideoTimeBaseNum   int
	VideoTimeBaseDen   int
	AudioTimeBaseNum   int
	AudioTimeBaseDen   int
	ColorRange         string
	ColorSpace         string
	ColorTransfer      string
	ColorPrimaries     string
	FieldOrder         string
	KeyframeInterval   int
	AudioCodec         string
	AudioProfile       string
	AudioSampleRate    int
	Channels           int
	AudioChannelLayout string
	AudioBitrate       string
	VideoStreams       int
	AudioStreams       int
	StartPTS           int64
}

var ErrAssemblyInputContractMismatch = fmt.Errorf("assembly: input contract mismatch")
var ErrAssemblyCompositionRejected = fmt.Errorf("assembly: input composition rejected")

// AssemblyCompatibilityGate checks all inputs have identical contract and stream signature.
// Fail-closed: any mismatch → ErrAssemblyInputContractMismatch, never re-encode.
// Additionally checks CompositionContract consistency (all inputs must have the same
// composition facts).
