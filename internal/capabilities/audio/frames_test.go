package audio

import "testing"

func TestFrameResolverSupportsExactRationalRates(t *testing.T) {
	cases := []struct {
		name      string
		rate      FrameRate
		timestamp int64
		want      int64
	}{
		{name: "23976", rate: FrameRate{Numerator: 24000, Denominator: 1001}, timestamp: 1_000_000, want: 24},
		{name: "24", rate: FrameRate{Numerator: 24, Denominator: 1}, timestamp: 1_000_000, want: 24},
		{name: "25", rate: FrameRate{Numerator: 25, Denominator: 1}, timestamp: 1_000_000, want: 25},
		{name: "2997", rate: FrameRate{Numerator: 30000, Denominator: 1001}, timestamp: 1_000_000, want: 30},
		{name: "30", rate: FrameRate{Numerator: 30, Denominator: 1}, timestamp: 1_000_000, want: 30},
		{name: "50", rate: FrameRate{Numerator: 50, Denominator: 1}, timestamp: 1_000_000, want: 50},
		{name: "5994", rate: FrameRate{Numerator: 60000, Denominator: 1001}, timestamp: 1_000_000, want: 60},
		{name: "60", rate: FrameRate{Numerator: 60, Denominator: 1}, timestamp: 1_000_000, want: 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver, err := NewFrameResolver(tc.rate)
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolver.FrameAt(tc.timestamp)
			if err != nil || got != tc.want {
				t.Fatalf("FrameAt() = %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestFrameResolverUsesDurationForStableRangeCounts(t *testing.T) {
	resolver, err := NewFrameResolver(FrameRate{Numerator: 30000, Denominator: 1001})
	if err != nil {
		t.Fatal(err)
	}
	durationCount, err := resolver.FrameCountForDuration(5_600_000)
	if err != nil {
		t.Fatal(err)
	}
	_, destinationCount, err := resolver.FrameRange(0, 5_600_000)
	if err != nil {
		t.Fatal(err)
	}
	_, sourceCount, err := resolver.FrameRange(33_200_000, 5_600_000)
	if err != nil {
		t.Fatal(err)
	}
	if durationCount != 168 || destinationCount != sourceCount || destinationCount != 168 {
		t.Fatalf("destination/source counts = %d/%d, want both 168", destinationCount, sourceCount)
	}
}

func TestFrameResolverRejectsInvalidAndOverflowingInputs(t *testing.T) {
	if _, err := NewFrameResolver(FrameRate{Numerator: 0, Denominator: 1}); err == nil {
		t.Fatal("zero numerator must be rejected")
	}
	resolver, err := NewFrameResolver(FrameRate{Numerator: 1, Denominator: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.FrameAt(-1); err == nil {
		t.Fatal("negative timestamp must be rejected")
	}
	if _, _, err := resolver.FrameRange(1, -1); err == nil {
		t.Fatal("negative duration must be rejected")
	}
	if _, err := resolver.FrameAt(^int64(0)); err == nil {
		t.Fatal("overflowing rounded timestamp must be rejected")
	}
}

func TestFrameAlignedDurationUSCoversFrameRoundedVideo(t *testing.T) {
	// scene-8 edge case: a 17.52s narration at 30fps rounds the video up to
	// 526 frames (17.533s); the audio master must pad to at least that.
	resolver, err := NewFrameResolver(IntegerFrameRate(30))
	if err != nil {
		t.Fatal(err)
	}
	aligned, err := resolver.FrameAlignedDurationUS(17_520_000)
	if err != nil {
		t.Fatal(err)
	}
	if aligned < 17_520_000 || aligned < 17_533_333 {
		t.Fatalf("aligned = %dus, want >= 17533333us (526 frames @30fps)", aligned)
	}
	// A duration already on a frame boundary stays unchanged.
	exact, err := resolver.FrameAlignedDurationUS(30_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if exact != 30_000_000 {
		t.Fatalf("aligned = %dus, want 30000000us", exact)
	}
	// A duration that rounds down (17.48s → 524 frames) keeps the timeline
	// duration so the audio master is never shortened.
	resolver29, err := NewFrameResolver(IntegerFrameRate(30))
	if err != nil {
		t.Fatal(err)
	}
	roundedDown, err := resolver29.FrameAlignedDurationUS(17_480_000)
	if err != nil {
		t.Fatal(err)
	}
	if roundedDown != 17_480_000 {
		t.Fatalf("aligned = %dus, want the timeline 17480000us (never shortened)", roundedDown)
	}
}
