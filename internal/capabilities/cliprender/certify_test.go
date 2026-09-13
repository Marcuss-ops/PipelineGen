package cliprender

import (
	"errors"
	"testing"
)

func certOutcome() *RenderOutcome {
	return &RenderOutcome{
		Container:    "mov,mp4,m4a,3gp,3g2,mj2",
		VideoCodec:   "h264",
		VideoProfile: "High",
		PixelFormat:  "yuv420p",
		Width:        1920,
		Height:       1080,
		FPSNum:       24,
		FPSDen:       1,
		AudioStreams: 1,
	}
}

// fullFacts is the complete structural certification a RenderingGen worker
// emits — including every dimension the deleted local Rust probe could not
// report (codec level, timebase, SAR, colour, field order, GOP, audio block).
func fullFacts() *OutputFacts {
	return &OutputFacts{
		Container:        "mov,mp4,m4a,3gp,3g2,mj2",
		HasVideo:         true,
		VideoStreams:     1,
		VideoCodec:       "h264",
		VideoProfile:     "High",
		VideoLevel:       "4.1",
		PixelFormat:      "yuv420p",
		Width:            1920,
		Height:           1080,
		FPSNum:           24,
		FPSDen:           1,
		VideoTimeBaseNum: 1,
		VideoTimeBaseDen: 12288,
		AudioTimeBaseNum: 1,
		AudioTimeBaseDen: 48000,
		SARNum:           1,
		SARDen:           1,
		ColorRange:       "tv",
		ColorSpace:       "bt709",
		ColorTransfer:    "bt709",
		ColorPrimaries:   "bt709",
		FieldOrder:       "progressive",
		KeyframeInterval: 48,
		HasAudio:         true,
		AudioStreams:     1,
		AudioCodec:       "aac",
		AudioProfile:     "LC",
		SampleRate:       48000,
		Channels:         2,
		ChannelLayout:    "stereo",
		AudioBitrate:     "128000",
	}
}

// TestOutputProbeFromCertified_ProjectsEveryCertifiedDimension is the core
// contract: the dimensions the deleted local Rust probe could not observe must
// reach ValidateContract from the certified owner instead of being skipped.
func TestOutputProbeFromCertified_ProjectsEveryCertifiedDimension(t *testing.T) {
	outcome := &RenderOutcome{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Facts: fullFacts()}
	probe, err := OutputProbeFromCertified(outcome)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"container", probe.Container, "mp4"},
		{"video profile", probe.VideoProfile, "high"},
		{"video level", probe.VideoLevel, "4.1"},
		{"video timebase num", probe.VideoTimeBaseNum, 1},
		{"video timebase den", probe.VideoTimeBaseDen, 12288},
		{"audio timebase num", probe.AudioTimeBaseNum, 1},
		{"audio timebase den", probe.AudioTimeBaseDen, 48000},
		{"sar num", probe.SARNum, 1},
		{"sar den", probe.SARDen, 1},
		{"color range", probe.ColorRange, "tv"},
		{"color space", probe.ColorSpace, "bt709"},
		{"color transfer", probe.ColorTransfer, "bt709"},
		{"color primaries", probe.ColorPrimaries, "bt709"},
		{"field order", probe.FieldOrder, "progressive"},
		{"keyframe interval", probe.KeyframeInterval, 48},
		{"audio profile", probe.AudioProfile, "LC"},
		{"sample rate", probe.SampleRate, 48000},
		{"channels", probe.Channels, 2},
		{"channel layout", probe.ChannelLayout, "stereo"},
		{"audio bitrate", probe.AudioBitrate, "128000"},
		{"has audio", probe.HasAudio, true},
		{"audio streams", probe.AudioStreams, 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if probe.FPS != 24.0 {
		t.Errorf("fps float projection = %v, want 24", probe.FPS)
	}
}

// TestOutputProbeFromCertified_FailsClosedWhenNothingCertified pins the
// fail-closed rule: an output nobody certified is never validated into
// existence with an empty probe that skips every dimension.
func TestOutputProbeFromCertified_FailsClosedWhenNothingCertified(t *testing.T) {
	if _, err := OutputProbeFromCertified(&RenderOutcome{}); !errors.Is(err, ErrUncertifiedOutput) {
		t.Fatalf("uncertified outcome error = %v, want ErrUncertifiedOutput", err)
	}
	if _, err := OutputProbeFromCertified(nil); !errors.Is(err, ErrUncertifiedOutput) {
		t.Fatalf("nil outcome error = %v, want ErrUncertifiedOutput", err)
	}
}

// TestOutputProbeFromCertified_LegacyFlatSummary pins the compatibility path: a
// boundary that certified only the flat summary still projects, with the
// container/profile normalizations the contract expects.
func TestOutputProbeFromCertified_LegacyFlatSummary(t *testing.T) {
	probe, err := OutputProbeFromCertified(certOutcome())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if probe.Container != "mp4" {
		t.Errorf("container = %q, want mp4 (family normalized)", probe.Container)
	}
	if probe.VideoProfile != "high" {
		t.Errorf("video profile = %q, want high (casing normalized)", probe.VideoProfile)
	}
	if probe.Width != 1920 || probe.Height != 1080 || probe.FPSNum != 24 || probe.FPSDen != 1 {
		t.Errorf("geometry/fps = %dx%d %d/%d, want 1920x1080 24/1", probe.Width, probe.Height, probe.FPSNum, probe.FPSDen)
	}
	if !probe.HasAudio || probe.AudioStreams != 1 {
		t.Errorf("audio = has=%v streams=%d, want true/1", probe.HasAudio, probe.AudioStreams)
	}
}

// TestCertifiedFactsFillTheContractGateway proves the projected facts make
// ValidateContract actually enforce the previously-skipped dimensions: a render
// that violates colour/GOP now FAILS the contract gate.
func TestCertifiedFactsFillTheContractGateway(t *testing.T) {
	contract := &ResolvedContract{
		Container: "mp4", VideoCodec: "h264", VideoProfile: "high", VideoLevel: "4.1",
		PixelFormat: "yuv420p", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		VideoTimeBaseNum: 1, VideoTimeBaseDen: 12288, AudioTimeBaseNum: 1, AudioTimeBaseDen: 48000,
		SARNum: 1, SARDen: 1, ColorRange: "tv", ColorSpace: "bt709", ColorTransfer: "bt709",
		ColorPrimaries: "bt709", FieldOrder: "progressive", KeyframeInterval: 48,
		AudioCodec: "aac", AudioProfile: "LC", SampleRate: 48000,
		Channels: 2, AudioChannelLayout: "stereo", AudioBitrate: "128000", AudioStreams: 1,
		VideoStreams: 1, StartPTS: 0,
	}
	outcome := &RenderOutcome{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, Facts: fullFacts()}
	probe, err := OutputProbeFromCertified(outcome)
	if err != nil {
		t.Fatalf("project certified facts: %v", err)
	}
	if err := ValidateContract(contract, probe); err != nil {
		t.Fatalf("a conforming certified output must validate: %v", err)
	}
	// A wrong GOP must now be caught (it was silently skipped before).
	bad := *probe
	bad.KeyframeInterval = 24
	if err := ValidateContract(contract, &bad); !errors.Is(err, ErrContractMismatch) {
		t.Fatalf("GOP mismatch must fail the contract gate, got %v", err)
	}
	// A wrong colour range must now be caught too.
	badColor := *probe
	badColor.ColorRange = "pc"
	if err := ValidateContract(contract, &badColor); !errors.Is(err, ErrContractMismatch) {
		t.Fatalf("colour range mismatch must fail the contract gate, got %v", err)
	}
}

// TestRenderOutcomeFacts_PrefersCompleteFacts pins the resolution order: the
// complete fact object wins over the flat summary, and a nil outcome resolves
// to nil facts.
func TestRenderOutcomeFacts_PrefersCompleteFacts(t *testing.T) {
	facts := fullFacts()
	got := RenderOutcomeFacts(&RenderOutcome{Container: "flat", Facts: facts})
	if got != facts {
		t.Fatal("RenderOutcomeFacts must prefer the complete Facts object")
	}
	if got := RenderOutcomeFacts(nil); got != nil {
		t.Fatal("nil outcome must resolve to nil facts")
	}
	if got := RenderOutcomeFacts(&RenderOutcome{}); got != nil {
		t.Fatal("an outcome that certified nothing must resolve to nil facts")
	}
}
