package observability

// concurrency_sweep_test.go pins the deterministic concurrency derivation:
// the end-before-start tie-breaker at equal timestamps, per-phase peak/avg,
// slot_utilization and scaling_efficiency, and the empty/unknown-slots edge
// cases.

import (
	"testing"
	"time"
)

func TestDeriveBatchReport_TieBreakerIsDeterministic(t *testing.T) {
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	// Run A finishes EXACTLY when run B starts. Without the tie-breaker this
	// yields peak 2 or 1 (and avg 0.5 or 1.0) depending on input order; the
	// canonical rule (finish before start at equal timestamps) must always
	// yield peak 1, avg 1.0 — the two runs never overlap.
	a := RunReport{RunID: "a", StartedAt: base, FinishedAt: base.Add(10 * time.Millisecond), ExecutionWallMs: 10}
	b := RunReport{RunID: "b", StartedAt: base.Add(10 * time.Millisecond), FinishedAt: base.Add(20 * time.Millisecond), ExecutionWallMs: 10}

	x := DeriveBatchReport([]RunReport{a, b}, 3)
	y := DeriveBatchReport([]RunReport{b, a}, 3)
	if x.PeakConcurrency != 1 || y.PeakConcurrency != 1 {
		t.Fatalf("peak = %d/%d, want 1/1 (back-to-back runs must never overlap)", x.PeakConcurrency, y.PeakConcurrency)
	}
	if x.AverageConcurrency != y.AverageConcurrency {
		t.Fatalf("orders disagree: avg %f vs %f — tie-breaker not deterministic", x.AverageConcurrency, y.AverageConcurrency)
	}
	for _, got := range []BatchReport{x, y} {
		if got.AverageConcurrency < 0.99 || got.AverageConcurrency > 1.01 {
			t.Fatalf("avg = %f, want 1.0", got.AverageConcurrency)
		}
		if got.BatchWallMs != 20 || got.BatchWorkMs != 20 {
			t.Fatalf("wall/work = %d/%d, want 20/20", got.BatchWallMs, got.BatchWorkMs)
		}
		if got.ParallelismFactor < 0.99 || got.ParallelismFactor > 1.01 {
			t.Fatalf("parallelism_factor = %f, want 1.0", got.ParallelismFactor)
		}
		if got.SlotUtilization < 0.33 || got.SlotUtilization > 0.34 {
			t.Fatalf("slot_utilization = %f, want ~0.333 (peak 1 of 3 slots)", got.SlotUtilization)
		}
		if got.ScalingEfficiency < 0.33 || got.ScalingEfficiency > 0.34 {
			t.Fatalf("scaling_efficiency = %f, want ~0.333 (pf 1.0 / 3 slots)", got.ScalingEfficiency)
		}
	}
}

func TestDeriveBatchReport_OverlapPeakAvgAndEfficiency(t *testing.T) {
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	// Two runs overlapping 10..90ms: active = 1 for 10ms, 2 for 80ms, 1 for
	// 10ms → avg = (10 + 160 + 10)/100 = 1.8; work/wall = 180/100 = 1.8.
	reports := []RunReport{
		{RunID: "a", StartedAt: base, FinishedAt: base.Add(100 * time.Millisecond), ExecutionWallMs: 100},
		{RunID: "b", StartedAt: base.Add(10 * time.Millisecond), FinishedAt: base.Add(90 * time.Millisecond), ExecutionWallMs: 80},
	}
	got := DeriveBatchReport(reports, 2)
	if got.PeakConcurrency != 2 {
		t.Fatalf("peak = %d, want 2", got.PeakConcurrency)
	}
	if got.AverageConcurrency < 1.79 || got.AverageConcurrency > 1.81 {
		t.Fatalf("avg = %f, want 1.8", got.AverageConcurrency)
	}
	if got.ParallelismFactor < 1.79 || got.ParallelismFactor > 1.81 {
		t.Fatalf("parallelism_factor = %f, want 1.8", got.ParallelismFactor)
	}
	if got.SlotUtilization < 0.99 || got.SlotUtilization > 1.01 {
		t.Fatalf("slot_utilization = %f, want 1.0 (2/2)", got.SlotUtilization)
	}
	if got.ScalingEfficiency < 0.89 || got.ScalingEfficiency > 0.91 {
		t.Fatalf("scaling_efficiency = %f, want 0.9 (1.8/2)", got.ScalingEfficiency)
	}
}

func TestDeriveBatchReport_PerPhaseConcurrency(t *testing.T) {
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	reports := []RunReport{
		{
			RunID: "r1", StartedAt: base, FinishedAt: base.Add(200 * time.Millisecond), ExecutionWallMs: 200,
			Operations: []OperationReport{
				{Operation: "render", StartedAt: base, FinishedAt: base.Add(100 * time.Millisecond)},
				{Operation: "upload", StartedAt: base.Add(50 * time.Millisecond), FinishedAt: base.Add(150 * time.Millisecond)},
				// Owner-reported fact without anchors: never a concurrency
				// interval, must not appear in the phases.
				{Operation: "hash", DurationMs: 12},
			},
		},
		{
			RunID: "r2", StartedAt: base.Add(20 * time.Millisecond), FinishedAt: base.Add(220 * time.Millisecond), ExecutionWallMs: 200,
			Operations: []OperationReport{
				{Operation: "render", StartedAt: base.Add(20 * time.Millisecond), FinishedAt: base.Add(120 * time.Millisecond)},
				{Operation: "upload", StartedAt: base.Add(130 * time.Millisecond), FinishedAt: base.Add(200 * time.Millisecond)},
			},
		},
	}
	got := DeriveBatchReport(reports, 2)

	// render: overlap 20..100 → peak 2; avg = (1*20 + 2*80 + 1*20)/120 ≈ 1.667.
	render, ok := got.Phases["render"]
	if !ok {
		t.Fatalf("phases = %+v, want render", got.Phases)
	}
	if render.Count != 2 || render.PeakConcurrency != 2 {
		t.Fatalf("render phase = %+v, want count 2 peak 2", render)
	}
	if render.AverageConcurrency < 1.66 || render.AverageConcurrency > 1.67 {
		t.Fatalf("render avg = %f, want ~1.667", render.AverageConcurrency)
	}
	if render.WorkMs != 200 || render.WallMs != 120 {
		t.Fatalf("render work/wall = %d/%d, want 200/120", render.WorkMs, render.WallMs)
	}

	// upload: 50..150 vs 130..200 overlap 130..150 → peak 2;
	// avg = (1*80 + 2*20 + 1*50)/150 = 170/150 ≈ 1.133.
	upload, ok := got.Phases["upload"]
	if !ok {
		t.Fatalf("phases = %+v, want upload", got.Phases)
	}
	if upload.Count != 2 || upload.PeakConcurrency != 2 {
		t.Fatalf("upload phase = %+v, want count 2 peak 2", upload)
	}
	if upload.AverageConcurrency < 1.13 || upload.AverageConcurrency > 1.14 {
		t.Fatalf("upload avg = %f, want ~1.133", upload.AverageConcurrency)
	}

	if _, exists := got.Phases["hash"]; exists {
		t.Fatalf("untimed operation must not appear in phases: %+v", got.Phases)
	}
}

func TestDeriveBatchReport_EmptyAndUnknownSlots(t *testing.T) {
	// No reports → all-zero batch, no phases, no derived ratios.
	empty := DeriveBatchReport(nil, 4)
	if empty.BatchWallMs != 0 || empty.PeakConcurrency != 0 || empty.Phases != nil {
		t.Fatalf("empty batch = %+v", empty)
	}

	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	reports := []RunReport{
		{RunID: "a", StartedAt: base, FinishedAt: base.Add(50 * time.Millisecond), ExecutionWallMs: 50},
	}
	// configuredSlots unknown (0): derived ratios stay zero/absent — never a
	// fabricated utilization number.
	unknown := DeriveBatchReport(reports, 0)
	if unknown.SlotUtilization != 0 || unknown.ScalingEfficiency != 0 {
		t.Fatalf("unknown slots must not fabricate ratios: %+v", unknown)
	}
	if unknown.PeakConcurrency != 1 || unknown.AverageConcurrency < 0.99 || unknown.AverageConcurrency > 1.01 {
		t.Fatalf("batch basics still derived without slots: %+v", unknown)
	}

	// Invalid reports (no/backwards timestamps) → zero batch, no panic.
	invalid := DeriveBatchReport([]RunReport{
		{RunID: "bad1"},
		{RunID: "bad2", StartedAt: base.Add(10 * time.Millisecond), FinishedAt: base},
	}, 2)
	if invalid.PeakConcurrency != 0 || invalid.BatchWallMs != 0 || invalid.ParallelismFactor != 0 {
		t.Fatalf("invalid batch = %+v", invalid)
	}
}
