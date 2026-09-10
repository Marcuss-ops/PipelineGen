package mediaexec

import (
	"testing"
)

// canonicalTarget is the frozen assembly-contract target the resolver
// compares against (1920x1080 24fps yuv420p h264 + aac 48k stereo).
func canonicalTarget() VideoProfile {
	c := VideoProfile{}.WithDefaults()
	return c
}

func TestResolveCutMode_CopyEligibleCanonicalSource(t *testing.T) {
	target := canonicalTarget()
	got := ResolveCutMode(CutEligibilityInput{
		Source: MediaFacts{
			VideoCodec:  "h264",
			PixelFormat: "yuv420p",
			Width:       target.Width,
			Height:      target.Height,
			FPSNum:      target.FPSNum,
			FPSDen:      target.FPSDen,
			HasAudio:    true,
			AudioCodec:  "aac",
			SampleRate:  target.SampleRate,
			Channels:    target.Channels,
		},
		Target:         target,
		StartSec:       10,
		EndSec:         20,
		KeepAudio:      true,
		StreamCopySafe: true,
	})
	if got != CutModeCopy {
		t.Fatalf("ResolveCutMode = %q, want %q (fully canonical source must stream-copy)", got, CutModeCopy)
	}
}

func TestResolveCutMode_CopyWhenAudioDropped(t *testing.T) {
	target := canonicalTarget()
	// No audio stream at all: with KeepAudio=false the video conformance
	// alone is enough to stream-copy.
	got := ResolveCutMode(CutEligibilityInput{
		Source: MediaFacts{
			VideoCodec:  "h264",
			PixelFormat: "yuv420p",
			Width:       target.Width,
			Height:      target.Height,
			FPSNum:      target.FPSNum,
			FPSDen:      target.FPSDen,
		},
		Target:         target,
		StartSec:       0,
		EndSec:         5,
		KeepAudio:      false,
		StreamCopySafe: true,
	})
	if got != CutModeCopy {
		t.Fatalf("ResolveCutMode = %q, want %q (audio dropped ⇒ video conformance suffices)", got, CutModeCopy)
	}
}

func TestResolveCutMode_NonCanonicalCodecNormalizes(t *testing.T) {
	target := canonicalTarget()
	got := ResolveCutMode(CutEligibilityInput{
		Source: MediaFacts{
			VideoCodec:  "avc1", // same family spelled differently — resolver is strict
			PixelFormat: "yuv420p",
			Width:       target.Width,
			Height:      target.Height,
			FPSNum:      target.FPSNum,
			FPSDen:      target.FPSDen,
			HasAudio:    true,
			AudioCodec:  "aac",
			SampleRate:  target.SampleRate,
			Channels:    target.Channels,
		},
		Target:         target,
		StartSec:       0,
		EndSec:         4,
		KeepAudio:      true,
		StreamCopySafe: true,
	})
	if got != CutModeNormalize {
		t.Fatalf("ResolveCutMode = %q, want %q (non-h264 codec must normalize)", got, CutModeNormalize)
	}
}

func TestResolveCutMode_NonYUV420PNormalizes(t *testing.T) {
	target := canonicalTarget()
	got := ResolveCutMode(CutEligibilityInput{
		Source: MediaFacts{
			VideoCodec:  "h264",
			PixelFormat: "yuv444p",
			Width:       target.Width,
			Height:      target.Height,
			FPSNum:      target.FPSNum,
			FPSDen:      target.FPSDen,
			HasAudio:    true,
			AudioCodec:  "aac",
			SampleRate:  target.SampleRate,
			Channels:    target.Channels,
		},
		Target:         target,
		StartSec:       0,
		EndSec:         4,
		KeepAudio:      true,
		StreamCopySafe: true,
	})
	if got != CutModeNormalize {
		t.Fatalf("ResolveCutMode = %q, want %q (non-yuv420p pixel format must normalize)", got, CutModeNormalize)
	}
}

func TestResolveCutMode_DimensionMismatchNormalizes(t *testing.T) {
	target := canonicalTarget()
	got := ResolveCutMode(CutEligibilityInput{
		Source: MediaFacts{
			VideoCodec:  "h264",
			PixelFormat: "yuv420p",
			Width:       1280,
			Height:      720,
			FPSNum:      target.FPSNum,
			FPSDen:      target.FPSDen,
			HasAudio:    true,
			AudioCodec:  "aac",
			SampleRate:  target.SampleRate,
			Channels:    target.Channels,
		},
		Target:         target,
		StartSec:       0,
		EndSec:         4,
		KeepAudio:      true,
		StreamCopySafe: true,
	})
	if got != CutModeNormalize {
		t.Fatalf("ResolveCutMode = %q, want %q (720p source against 1080p target must normalize)", got, CutModeNormalize)
	}
}

func TestResolveCutMode_FPSRationalMismatchNormalizes(t *testing.T) {
	target := canonicalTarget()
	got := ResolveCutMode(CutEligibilityInput{
		Source: MediaFacts{
			VideoCodec:  "h264",
			PixelFormat: "yuv420p",
			Width:       target.Width,
			Height:      target.Height,
			FPSNum:      target.FPSNum,
			FPSDen:      target.FPSDen + 1, // 23.97-style vs exact 24
			HasAudio:    true,
			AudioCodec:  "aac",
			SampleRate:  target.SampleRate,
			Channels:    target.Channels,
		},
		Target:         target,
		StartSec:       0,
		EndSec:         4,
		KeepAudio:      true,
		StreamCopySafe: true,
	})
	if got != CutModeNormalize {
		t.Fatalf("ResolveCutMode = %q, want %q (fps rational must match exactly)", got, CutModeNormalize)
	}
}

func TestResolveCutMode_AudioMismatchNormalizesWhenKept(t *testing.T) {
	target := canonicalTarget()
	cases := []struct {
		name   string
		mutate func(*MediaFacts)
	}{
		{"no audio stream", func(f *MediaFacts) { f.HasAudio = false }},
		{"mp3 audio", func(f *MediaFacts) { f.AudioCodec = "mp3" }},
		{"wrong sample rate", func(f *MediaFacts) { f.SampleRate = 44100 }},
		{"wrong channels", func(f *MediaFacts) { f.Channels = 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := MediaFacts{
				VideoCodec:  "h264",
				PixelFormat: "yuv420p",
				Width:       target.Width,
				Height:      target.Height,
				FPSNum:      target.FPSNum,
				FPSDen:      target.FPSDen,
				HasAudio:    true,
				AudioCodec:  "aac",
				SampleRate:  target.SampleRate,
				Channels:    target.Channels,
			}
			tc.mutate(&f)
			got := ResolveCutMode(CutEligibilityInput{
				Source: f, Target: target, StartSec: 0, EndSec: 4,
				KeepAudio: true, StreamCopySafe: true,
			})
			if got != CutModeNormalize {
				t.Fatalf("ResolveCutMode = %q, want %q (audio kept ⇒ audio must conform)", got, CutModeNormalize)
			}
		})
	}
}

func TestResolveCutMode_UnsafeBoundaryNormalizes(t *testing.T) {
	target := canonicalTarget()
	got := ResolveCutMode(CutEligibilityInput{
		Source: MediaFacts{
			VideoCodec:  "h264",
			PixelFormat: "yuv420p",
			Width:       target.Width,
			Height:      target.Height,
			FPSNum:      target.FPSNum,
			FPSDen:      target.FPSDen,
			HasAudio:    true,
			AudioCodec:  "aac",
			SampleRate:  target.SampleRate,
			Channels:    target.Channels,
		},
		Target:         target,
		StartSec:       12.34,
		EndSec:         16.34,
		KeepAudio:      true,
		StreamCopySafe: false,
	})
	if got != CutModeNormalize {
		t.Fatalf("ResolveCutMode = %q, want %q (unproven keyframe boundary must normalize)", got, CutModeNormalize)
	}
}

func TestResolveCutMode_ZeroTargetDefaultsToFrozenContract(t *testing.T) {
	// A zero-value Target must fall back to the frozen assembly contract,
	// producing a deterministic (not panicking) decision.
	got := ResolveCutMode(CutEligibilityInput{
		Source: MediaFacts{
			VideoCodec:  "h264",
			PixelFormat: "yuv420p",
			Width:       1920,
			Height:      1080,
			FPSNum:      24,
			FPSDen:      1,
			HasAudio:    true,
			AudioCodec:  "aac",
			SampleRate:  48000,
			Channels:    2,
		},
		StartSec: 0, EndSec: 4, KeepAudio: true, StreamCopySafe: true,
	})
	if got != CutModeCopy {
		t.Fatalf("ResolveCutMode with zero target = %q, want %q", got, CutModeCopy)
	}
}

func TestResolveCutMode_InterfaceSSOT(t *testing.T) {
	// The interface + default resolver must agree (one canonical decision
	// owner per fact).
	in := CutEligibilityInput{
		Source: MediaFacts{
			VideoCodec: "h264", PixelFormat: "yuv420p",
			Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
			HasAudio: true, AudioCodec: "aac", SampleRate: 48000, Channels: 2,
		},
		Target: VideoProfile{Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1, SampleRate: 48000, Channels: 2},
		StartSec: 0, EndSec: 4, KeepAudio: true, StreamCopySafe: true,
	}
	if got := (DefaultCutModeResolver{}).ResolveCutMode(in); got != ResolveCutMode(in) {
		t.Fatalf("interface resolver = %q, package helper = %q — must agree", got, ResolveCutMode(in))
	}
}