package media

import (
	"context"
	"errors"
	"testing"
)

func TestStreamSignature_Fingerprint_Deterministic(t *testing.T) {
	c := DefaultAssemblyReadyVideoContract()
	sig1 := StreamSignatureFromContract(c)
	sig2 := StreamSignatureFromContract(c)
	if sig1.Fingerprint() != sig2.Fingerprint() {
		t.Fatalf("identical signatures must produce identical fingerprints:\n  %s\n  %s", sig1.Fingerprint(), sig2.Fingerprint())
	}
	// Different field order must NOT change the fingerprint (JSON key ordering is deterministic).
	t.Logf("canonical stream_signature_sha256: %s", sig1.Fingerprint())
}

func TestStreamSignature_Fingerprint_DiffersOnChange(t *testing.T) {
	c := DefaultAssemblyReadyVideoContract()
	baseline := StreamSignatureFromContract(c).Fingerprint()

	// FPS change.
	sig := StreamSignatureFromContract(c)
	sig.FPS = FrameRate{Num: 30, Den: 1}
	if sig.Fingerprint() == baseline {
		t.Fatal("FPS change must produce different fingerprint")
	}

	// Geometry change.
	sig = StreamSignatureFromContract(c)
	sig.Width = 1280
	sig.Height = 720
	if sig.Fingerprint() == baseline {
		t.Fatal("geometry change must produce different fingerprint")
	}

	// Codec change.
	sig = StreamSignatureFromContract(c)
	sig.VideoCodec = "vp9"
	if sig.Fingerprint() == baseline {
		t.Fatal("codec change must produce different fingerprint")
	}

	// Audio change.
	sig = StreamSignatureFromContract(c)
	sig.AudioSampleRate = 44100
	if sig.Fingerprint() == baseline {
		t.Fatal("audio sample rate change must produce different fingerprint")
	}

	// Video timebase change.
	sig = StreamSignatureFromContract(c)
	sig.VideoTimeBase = Rational{Num: 1, Den: 30}
	if sig.Fingerprint() == baseline {
		t.Fatal("timebase change must produce different fingerprint")
	}
}

func TestStreamSignatureFromProbe_MatchesContract(t *testing.T) {
	c := DefaultAssemblyReadyVideoContract()
	contractSig := StreamSignatureFromContract(c)

	probe := ProbeFacts{
		Container:          c.Container,
		VideoCodec:         c.VideoCodec,
		VideoProfile:       c.VideoProfile,
		VideoLevel:         c.VideoLevel,
		PixelFormat:        c.PixelFormat,
		Width:              c.Width,
		Height:             c.Height,
		FPSNum:             c.FPS.Num,
		FPSDen:             c.FPS.Den,
		SARNum:             c.SAR.Num,
		SARDen:             c.SAR.Den,
		VideoTimeBaseNum:   c.VideoTimeBase.Num,
		VideoTimeBaseDen:   c.VideoTimeBase.Den,
		AudioTimeBaseNum:   c.AudioTimeBase.Num,
		AudioTimeBaseDen:   c.AudioTimeBase.Den,
		ColorRange:         c.ColorRange,
		ColorSpace:         c.ColorSpace,
		ColorTransfer:      c.ColorTransfer,
		ColorPrimaries:     c.ColorPrimaries,
		FieldOrder:         c.FieldOrder,
		AudioCodec:         c.AudioCodec,
		AudioProfile:       c.AudioProfile,
		AudioSampleRate:    c.AudioSampleRate,
		Channels:           c.AudioChannels,
		AudioChannelLayout: c.AudioChannelLayout,
		AudioBitrate:       c.AudioBitrate,
		VideoStreams:       c.VideoStreams,
		AudioStreams:       c.AudioStreams,
		StartPTS:           c.StartPTS,
	}
	probeSig := StreamSignatureFromProbe(probe)
	if contractSig.Fingerprint() != probeSig.Fingerprint() {
		t.Fatalf("probe signature must match contract signature:\n  contract: %s\n  probe:    %s", contractSig.Fingerprint(), probeSig.Fingerprint())
	}
}

func TestAssemblyCompatibilityGate_IdenticalSignaturesPass(t *testing.T) {
	sig := StreamSignatureFromContract(DefaultAssemblyReadyVideoContract()).Fingerprint()
	apply := func(i int) AssemblyInput {
		return AssemblyInput{
			Path:       "clip.mp4",
			ContractID: AssemblyReadyVideoContractID,
			Signature:  sig,
			Probe: &ProbeFacts{
				Container:          "mp4",
				VideoCodec:         "h264",
				VideoProfile:       "high",
				PixelFormat:        "yuv420p",
				Width:              1920,
				Height:             1080,
				FPSNum:             24,
				FPSDen:             1,
				SARNum:             1,
				SARDen:             1,
				VideoTimeBaseNum:   1,
				VideoTimeBaseDen:   90000,
				AudioTimeBaseNum:   1,
				AudioTimeBaseDen:   48000,
				ColorRange:         "tv",
				ColorSpace:         "bt709",
				AudioCodec:         "aac",
				AudioProfile:       "LC",
				AudioSampleRate:    48000,
				Channels:           2,
				AudioChannelLayout: "stereo",
				VideoStreams:       1,
				AudioStreams:       1,
				StartPTS:           0,
			},
		}
	}
	inputs := []AssemblyInput{apply(0), apply(1), apply(2)}
	if err := AssemblyCompatibilityGate(context.Background(), inputs); err != nil {
		t.Fatalf("identical signatures must pass: %v", err)
	}
}

func TestAssemblyCompatibilityGate_SignatureMismatchFails(t *testing.T) {
	sig := StreamSignatureFromContract(DefaultAssemblyReadyVideoContract()).Fingerprint()
	base := AssemblyInput{
		Path:       "clip.mp4",
		ContractID: AssemblyReadyVideoContractID,
		Signature:  sig,
		Probe: &ProbeFacts{
			Container:       "mp4",
			VideoCodec:      "h264",
			PixelFormat:     "yuv420p",
			Width:           1920,
			Height:          1080,
			FPSNum:          24,
			FPSDen:          1,
			SARNum:          1,
			SARDen:          1,
			VideoProfile:    "high",
			AudioCodec:      "aac",
			AudioSampleRate: 48000,
			Channels:        2,
			VideoStreams:    1,
			AudioStreams:    1,
			StartPTS:        0,
		},
	}
	bad := base
	bad.Signature = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	inputs := []AssemblyInput{base, bad}
	if err := AssemblyCompatibilityGate(context.Background(), inputs); err == nil {
		t.Fatal("different signatures must fail the gate")
	} else if !errors.Is(err, ErrAssemblyInputContractMismatch) {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestAssemblyCompatibilityGate_ContractIDMismatchFails(t *testing.T) {
	base := AssemblyInput{
		Path:       "clip.mp4",
		ContractID: AssemblyReadyVideoContractID,
		Probe: &ProbeFacts{
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			SARNum: 1, SARDen: 1, VideoProfile: "high",
			Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p",
			AudioCodec: "aac", AudioProfile: "LC", AudioSampleRate: 48000, Channels: 2,
			VideoStreams: 1, AudioStreams: 1, StartPTS: 0,
		},
	}
	bad := base
	bad.ContractID = "SOME_OTHER_CONTRACT"
	inputs := []AssemblyInput{base, bad}
	if err := AssemblyCompatibilityGate(context.Background(), inputs); err == nil {
		t.Fatal("different contract IDs must fail the gate")
	}
}

func TestAssemblyCompatibilityGate_FPSMismatchViasProbeFails(t *testing.T) {
	base := AssemblyInput{
		Path:       "clip.mp4",
		ContractID: AssemblyReadyVideoContractID,
		Probe: &ProbeFacts{
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			SARNum: 1, SARDen: 1, VideoProfile: "high",
			Container: "mp4", VideoCodec: "h264", PixelFormat: "yuv420p",
			AudioCodec: "aac", AudioProfile: "LC", AudioSampleRate: 48000, Channels: 2,
			VideoStreams: 1, AudioStreams: 1, StartPTS: 0,
		},
	}
	bad := base
	badProbe := *base.Probe
	badProbe.FPSNum = 30
	badProbe.FPSDen = 1
	bad.Probe = &badProbe
	inputs := []AssemblyInput{base, bad}
	if err := AssemblyCompatibilityGate(context.Background(), inputs); err == nil {
		t.Fatal("24fps + 30fps probes must fail the gate")
	}
}

func TestAssemblyCompatibilityGate_EmptyInputsFails(t *testing.T) {
	if err := AssemblyCompatibilityGate(context.Background(), nil); err == nil {
		t.Fatal("empty inputs must fail")
	}
}

func TestAssemblyCompatibilityGate_WrongContractFails(t *testing.T) {
	input := AssemblyInput{
		Path:       "clip.mp4",
		ContractID: "WRONG_ID",
		Probe:      &ProbeFacts{Width: 1920, Height: 1080},
	}
	if err := AssemblyCompatibilityGate(context.Background(), []AssemblyInput{input}); err == nil {
		t.Fatal("wrong contract ID must fail immediately")
	}
}

func TestVideoContract_ValidateExact_PassesDefault(t *testing.T) {
	c := DefaultAssemblyReadyVideoContract()
	if err := c.ValidateExact(); err != nil {
		t.Fatalf("default contract must validate: %v", err)
	}
}

func TestVideoContract_ValidateExact_FailsOnWrongFPS(t *testing.T) {
	c := DefaultAssemblyReadyVideoContract()
	c.FPS = FrameRate{Num: 30, Den: 1}
	if err := c.ValidateExact(); err == nil {
		t.Fatal("30fps contract must fail ValidateExact")
	}
}
