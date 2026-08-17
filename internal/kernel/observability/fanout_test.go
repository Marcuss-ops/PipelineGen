package observability

import "testing"

// TestFanoutReports_ParallelWallTimeNotSummed pins the core fan-out rule:
// 10 parallel calls recorded under one stage must expose the batch wall time
// (5s) separately from the accumulated work (40s), never summed together.
func TestFanoutReports_ParallelWallTimeNotSummed(t *testing.T) {
	report := &RunReport{
		Stages: []StageReport{stageAt("voiceover.generate", 0, 5210)},
		Operations: []OperationReport{
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 4012},
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 4420},
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 3980},
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 4100},
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 4001},
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 4050},
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 3999},
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 4022},
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 3871},
			{Stage: "voiceover.generate", Component: "tts", Operation: "synthesize", DurationMs: 3665},
		},
	}

	reports := report.FanoutReports()
	if len(reports) != 1 {
		t.Fatalf("FanoutReports len = %d, want 1", len(reports))
	}
	f := reports[0]
	if f.Stage != "voiceover.generate" {
		t.Fatalf("Stage = %q, want voiceover.generate", f.Stage)
	}
	if f.WallMs != 5210 {
		t.Fatalf("WallMs = %d, want 5210 (batch wall, not summed work)", f.WallMs)
	}
	if f.Calls != 10 {
		t.Fatalf("Calls = %d, want 10", f.Calls)
	}
	if f.WorkMs != 40120 {
		t.Fatalf("WorkMs = %d, want 40120", f.WorkMs)
	}
	if f.MaxMs != 4420 {
		t.Fatalf("MaxMs = %d, want 4420", f.MaxMs)
	}
	if f.WorkMs <= f.WallMs {
		t.Fatalf("parallel work must exceed wall time (work=%d, wall=%d)", f.WorkMs, f.WallMs)
	}
}

// TestFanoutReports_GroupsByStageAndIsDeterministic pins grouping + ordering.
func TestFanoutReports_GroupsByStageAndIsDeterministic(t *testing.T) {
	report := &RunReport{
		Stages: []StageReport{
			stageAt("source.resolve", 0, 2600),
			stageAt("script.engine", 2600, 82000),
		},
		Operations: []OperationReport{
			{Stage: "source.resolve", Component: "qdrant", Operation: "search", DurationMs: 430},
			{Stage: "source.resolve", Component: "sqlite", Operation: "hydrate", DurationMs: 1200},
			{Stage: "source.resolve", Component: "sqlite", Operation: "hydrate", DurationMs: 800},
			{Stage: "script.engine", Component: "ollama", Operation: "generate", DurationMs: 78000},
		},
	}

	a := report.FanoutReports()
	b := report.FanoutReports()
	if len(a) != 2 || len(b) != 2 {
		t.Fatalf("FanoutReports len = %d/%d, want 2", len(a), len(b))
	}
	if a[0].Stage != "script.engine" || a[1].Stage != "source.resolve" {
		t.Fatalf("expected deterministic (sorted) order, got %q, %q", a[0].Stage, a[1].Stage)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("FanoutReports not deterministic: %+v vs %+v", a, b)
		}
	}

	engine := a[0]
	if engine.Calls != 1 || engine.WorkMs != 78000 || engine.MaxMs != 78000 || engine.WallMs != 79400 {
		t.Fatalf("engine fan-out = %+v", engine)
	}
	resolve := a[1]
	if resolve.Calls != 3 || resolve.WorkMs != 2430 || resolve.MaxMs != 1200 || resolve.WallMs != 2600 {
		t.Fatalf("resolve fan-out = %+v", resolve)
	}
}

// TestFanoutReports_SkipsUnstagedOperations pins that operations without a
// Stage are not attributed to any fan-out boundary.
func TestFanoutReports_SkipsUnstagedOperations(t *testing.T) {
	report := &RunReport{
		Stages: []StageReport{stageAt("script.engine", 0, 1000)},
		Operations: []OperationReport{
			{Stage: "script.engine", Component: "ollama", Operation: "generate", DurationMs: 900},
			{Component: "mystery", Operation: "orphan", DurationMs: 999},
		},
	}
	reports := report.FanoutReports()
	if len(reports) != 1 || reports[0].Stage != "script.engine" || reports[0].Calls != 1 {
		t.Fatalf("FanoutReports = %+v, want only the staged script.engine group", reports)
	}
}

// TestFanoutReports_NilAndEmptyIsSafe pins nil/empty handling.
func TestFanoutReports_NilAndEmptyIsSafe(t *testing.T) {
	var nilReport *RunReport
	if got := nilReport.FanoutReports(); got != nil {
		t.Fatalf("nil report FanoutReports = %+v, want nil", got)
	}
	if got := (&RunReport{}).FanoutReports(); len(got) != 0 {
		t.Fatalf("empty report FanoutReports = %+v, want empty", got)
	}
}
