package multilingual

import (
	"math"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func TestBuildCertification_Pass(t *testing.T) {
	summary := RunMetrics{
		WallMS:         162584,
		CPUUserMS:      2270,
		CPUSystemMS:    700,
		PeakRSSBytes:   88207360,
		ClipCount:      10,
		SuccessCount:   10,
		FailedCount:    0,
		CacheHits:      10,
		CacheMisses:    19,
		WorkerLimit:    4,
		SumOperationMS: 530403,
		RenderConcurrency: observability.ConcurrencyStats{
			Configured: 4, MaxObserved: 4, AvgObserved: 3.63,
		},
		Operations: ExecCounters{Translate: 9, ASS: 10, Render: 10, Validate: 10, Upload: 10},
	}

	rep := BuildCertification(summary, 10, 0)

	if rep.Result != "PASS" {
		t.Fatalf("result = %q, want PASS", rep.Result)
	}
	if rep.Inputs != 10 || rep.Completed != 10 || rep.Validated != 10 || rep.Failed != 0 {
		t.Fatalf("counts wrong: %+v", rep)
	}
	if rep.Parallelism.Configured != 4 || rep.Parallelism.MaxObserved != 4 {
		t.Fatalf("parallelism wrong: %+v", rep.Parallelism)
	}

	wantSpeedup := 530403.0 / 162584.0
	if math.Abs(rep.Performance.SpeedupVsSerial-wantSpeedup) > 1e-9 {
		t.Errorf("speedup_vs_serial = %.4f, want %.4f", rep.Performance.SpeedupVsSerial, wantSpeedup)
	}
	wantEff := wantSpeedup / 4
	if math.Abs(rep.Performance.ParallelEfficiency-wantEff) > 1e-9 {
		t.Errorf("parallel_efficiency = %.4f, want %.4f", rep.Performance.ParallelEfficiency, wantEff)
	}

	if rep.Operations.TranslationExecCount != 9 {
		t.Errorf("translation_exec_count = %d, want 9 (per-cue only, full-text eliminated)", rep.Operations.TranslationExecCount)
	}
	if rep.Operations.RenderExecCount != 10 || rep.Operations.ValidateExecCount != 10 || rep.Operations.UploadExecCount != 10 {
		t.Errorf("fan-out ops wrong: %+v", rep.Operations)
	}
	if rep.Cache.Hits != 10 || rep.Cache.Misses != 19 || rep.Cache.AvoidedWorkMS != 0 {
		t.Errorf("cache wrong: %+v", rep.Cache)
	}
}

func TestBuildCertification_Fail(t *testing.T) {
	base := RunMetrics{
		ClipCount: 10, SuccessCount: 10, FailedCount: 1, WorkerLimit: 4,
		RenderConcurrency: observability.ConcurrencyStats{Configured: 4, MaxObserved: 4},
	}
	// A failed variant → FAIL.
	if rep := BuildCertification(base, 9, 0); rep.Result != "FAIL" {
		t.Errorf("failed variant: result = %q, want FAIL", rep.Result)
	}
	// A validation shortfall (validated < inputs) → FAIL even with 0 failures.
	if rep := BuildCertification(RunMetrics{ClipCount: 10, SuccessCount: 10, FailedCount: 0}, 9, 0); rep.Result != "FAIL" {
		t.Errorf("validation shortfall: result = %q, want FAIL", rep.Result)
	}
	// Zero wall must not produce NaN speedup/efficiency.
	rep := BuildCertification(RunMetrics{ClipCount: 1, SuccessCount: 1, FailedCount: 0}, 1, 0)
	if rep.Result != "PASS" {
		t.Errorf("single-clip pass: result = %q, want PASS", rep.Result)
	}
	if math.IsNaN(rep.Performance.SpeedupVsSerial) || math.IsNaN(rep.Performance.ParallelEfficiency) {
		t.Errorf("zero wall must not yield NaN: %+v", rep.Performance)
	}
}
