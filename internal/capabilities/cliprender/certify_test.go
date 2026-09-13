package cliprender

import (
	"errors"
	"testing"
)

func certProbe() *OutputProbe {
	return &OutputProbe{
		Container:    "mp4",
		HasVideo:     true,
		VideoCodec:   "h264",
		PixelFormat:  "yuv420p",
		Width:        1920,
		Height:       1080,
		FPSNum:       24,
		FPSDen:       1,
		HasAudio:     true,
		AudioCodec:   "aac",
		AudioStreams: 1,
		VideoStreams: 1,
	}
}

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

// TestReconcileCertifiedFacts_FillsProfileFromCertified pins the core reason
// this reconciliation exists: the local Rust probe cannot report a codec
// profile, so the contract dimension was silently skipped. The certified
// artifact owns it and the value must be normalized to the contract casing.
func TestReconcileCertifiedFacts_FillsProfileFromCertified(t *testing.T) {
	probe := certProbe()
	probe.VideoProfile = "" // Rust probe reports no profile
	merged, err := ReconcileCertifiedFacts(probe, certOutcome())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.VideoProfile != "high" {
		t.Fatalf("VideoProfile = %q, want %q (certified \"High\" normalized)", merged.VideoProfile, "high")
	}
	// The caller's probe must not be mutated in place.
	if probe.VideoProfile != "" {
		t.Fatalf("input probe mutated: VideoProfile = %q", probe.VideoProfile)
	}
}

// TestReconcileCertifiedFacts_ContainerFamilyAgrees proves the ffprobe error
// family string and the normalized container are the same fact, not a
// disagreement.
func TestReconcileCertifiedFacts_ContainerFamilyAgrees(t *testing.T) {
	if _, err := ReconcileCertifiedFacts(certProbe(), certOutcome()); err != nil {
		t.Fatalf("family container must not be a mismatch: %v", err)
	}
}

func TestReconcileCertifiedFacts_FillsOnlyUnreported(t *testing.T) {
	probe := certProbe()
	probe.PixelFormat = ""
	probe.Width = 0
	merged, err := ReconcileCertifiedFacts(probe, certOutcome())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged.PixelFormat != "yuv420p" || merged.Width != 1920 {
		t.Fatalf("unreported dimensions not filled: %+v", merged)
	}
}

func TestReconcileCertifiedFacts_NilOutcomeLeavesProbeUntouched(t *testing.T) {
	probe := certProbe()
	merged, err := ReconcileCertifiedFacts(probe, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if merged != probe {
		t.Fatal("nil outcome must return the probe untouched")
	}
}

func TestReconcileCertifiedFacts_NilProbeFailsClosed(t *testing.T) {
	if _, err := ReconcileCertifiedFacts(nil, certOutcome()); !errors.Is(err, ErrCertifiedFactsMismatch) {
		t.Fatalf("nil probe error = %v, want ErrCertifiedFactsMismatch", err)
	}
}

func TestReconcileCertifiedFacts_DisagreementFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*OutputProbe, *RenderOutcome)
	}{
		{"container", func(p *OutputProbe, o *RenderOutcome) { p.Container = "webm" }},
		{"video-codec", func(p *OutputProbe, o *RenderOutcome) { p.VideoCodec = "hevc" }},
		{"pixel-format", func(p *OutputProbe, o *RenderOutcome) { p.PixelFormat = "yuv422p" }},
		{"width", func(p *OutputProbe, o *RenderOutcome) { p.Width = 1280 }},
		{"height", func(p *OutputProbe, o *RenderOutcome) { p.Height = 720 }},
		{"fps", func(p *OutputProbe, o *RenderOutcome) { p.FPSNum = 30 }},
		{"audio-streams", func(p *OutputProbe, o *RenderOutcome) { p.AudioStreams = 2 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probe := certProbe()
			outcome := certOutcome()
			tc.mutate(probe, outcome)
			_, err := ReconcileCertifiedFacts(probe, outcome)
			if !errors.Is(err, ErrCertifiedFactsMismatch) {
				t.Fatalf("err = %v, want ErrCertifiedFactsMismatch", err)
			}
		})
	}
}

// TestReconcileCertifiedFacts_UnreportedCertifiedFactIsNotADisagreement
// confirms a certified artifact that simply did not report a dimension cannot
// manufacture a mismatch — absence is not a contradiction.
func TestReconcileCertifiedFacts_UnreportedCertifiedFactIsNotADisagreement(t *testing.T) {
	outcome := &RenderOutcome{}
	if _, err := ReconcileCertifiedFacts(certProbe(), outcome); err != nil {
		t.Fatalf("empty certified facts must not fail: %v", err)
	}
}
