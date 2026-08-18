package multilingual

import (
	"math"
	"testing"
)

func TestComputeSpeedup_SerialBaselineAndEfficiency(t *testing.T) {
	// Serial run (concurrency 1) is the baseline; speedup and efficiency are
	// computed relative to it. 200ms work spread across N workers.
	samples := []BenchmarkSample{
		{Concurrency: 1, WallMS: 800, WorkMS: 800},
		{Concurrency: 2, WallMS: 410, WorkMS: 800},
		{Concurrency: 4, WallMS: 220, WorkMS: 800},
		{Concurrency: 8, WallMS: 200, WorkMS: 800},
	}
	ComputeSpeedup(samples)

	want := []struct {
		conc       int
		speedup    float64
		efficiency float64
	}{
		{1, 1.0, 1.0},
		{2, 800.0 / 410.0, 800.0 / 410.0 / 2},
		{4, 800.0 / 220.0, 800.0 / 220.0 / 4},
		{8, 800.0 / 200.0, 800.0 / 200.0 / 8},
	}
	for i, w := range want {
		got := samples[i]
		if got.Concurrency != w.conc {
			t.Fatalf("sample %d: concurrency %d want %d", i, got.Concurrency, w.conc)
		}
		if math.Abs(got.Speedup-w.speedup) > 1e-9 {
			t.Errorf("concurrency %d: speedup %.4f want %.4f", w.conc, got.Speedup, w.speedup)
		}
		if math.Abs(got.Efficiency-w.efficiency) > 1e-9 {
			t.Errorf("concurrency %d: efficiency %.4f want %.4f", w.conc, got.Efficiency, w.efficiency)
		}
	}
}

func TestComputeSpeedup_EmptyAndDegenerateInputs(t *testing.T) {
	// Empty slice is a no-op.
	if got := ComputeSpeedup(nil); got != nil {
		t.Fatalf("nil slice: got %v want nil", got)
	}

	// Zero wall (no work) must not produce NaN/Inf speedup.
	samples := []BenchmarkSample{
		{Concurrency: 1, WallMS: 0, WorkMS: 0},
		{Concurrency: 4, WallMS: 0, WorkMS: 0},
	}
	ComputeSpeedup(samples)
	for _, s := range samples {
		if s.Speedup != 0 || s.Efficiency != 0 {
			t.Errorf("degenerate sample %+v: speedup=%v efficiency=%v want 0/0", s, s.Speedup, s.Efficiency)
		}
	}

	// Non-positive serial wall keeps later speedups at 0.
	samples = []BenchmarkSample{
		{Concurrency: 1, WallMS: 0, WorkMS: 10},
		{Concurrency: 2, WallMS: 100, WorkMS: 10},
	}
	ComputeSpeedup(samples)
	if samples[1].Speedup != 0 || samples[1].Efficiency != 0 {
		t.Errorf("zero serial wall: sample[1]=%+v want speedup/efficiency 0", samples[1])
	}
}
