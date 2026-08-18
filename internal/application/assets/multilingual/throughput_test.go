package multilingual

import (
	"math"
	"testing"
)

func TestRTF(t *testing.T) {
	if got := RTF(15000, 60000); math.Abs(got-0.25) > 1e-9 {
		t.Fatalf("RTF(15s,60s) = %f, want 0.25", got)
	}
	if got := RTF(40000, 55000); math.Abs(got-0.727272727) > 1e-6 {
		t.Fatalf("RTF(40s,55s) = %f, want ~0.727", got)
	}
	if got := RTF(100, 0); got != 0 {
		t.Fatalf("RTF with zero duration = %f, want 0", got)
	}
}

func TestComputeThroughput(t *testing.T) {
	// 10 clips, each 55s (55000ms), wall 280s (280000ms), render work 407000ms.
	tp := ComputeThroughput(10, 55000, 280000, 407000)
	if math.Abs(tp.ClipsPerMinute-10/(280.0/60.0)) > 1e-6 {
		t.Fatalf("clips/min = %f, want ~2.14", tp.ClipsPerMinute)
	}
	// media-minutes/min = (10*55/60) / (280/60) = 550/280 ≈ 1.96
	if math.Abs(tp.MediaMinutesPerMinute-550.0/280.0) > 1e-6 {
		t.Fatalf("media-min/min = %f, want ~1.96", tp.MediaMinutesPerMinute)
	}
	// render RTF = 407000 / (10*55000) = 0.74
	if math.Abs(tp.RenderRTF-407000.0/550000.0) > 1e-6 {
		t.Fatalf("render RTF = %f, want ~0.74", tp.RenderRTF)
	}
}

func TestComputeThroughput_ZeroWallIsSafe(t *testing.T) {
	tp := ComputeThroughput(10, 55000, 0, 100)
	if tp.ClipsPerMinute != 0 || tp.MediaMinutesPerMinute != 0 || tp.RenderRTF != 0 {
		t.Fatalf("zero wall must yield zero throughput: %+v", tp)
	}
}
