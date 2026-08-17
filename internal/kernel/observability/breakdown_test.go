package observability

import (
	"math"
	"testing"
	"time"
)

func stageAt(name string, startMs, endMs int64) StageReport {
	base := time.Unix(1_700_000_000, 0)
	return StageReport{
		Name:       name,
		Status:     StageStatusCompleted,
		StartedAt:  base.Add(time.Duration(startMs) * time.Millisecond),
		FinishedAt: base.Add(time.Duration(endMs) * time.Millisecond),
		DurationMs: endMs - startMs,
	}
}

// TestBreakdown_TopLevelStagesExcludeNested builds the canonical
// script.generate stage tree and pins the top-level-only attribution:
// nested durations must never be summed into AttributedStageMs.
func TestBreakdown_TopLevelStagesExcludeNested(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 86721,
		Stages: []StageReport{
			stageAt("script.prepare", 0, 3000),
			stageAt("script.normalize", 10, 20),
			stageAt("source.resolve", 20, 2600),
			stageAt("script.plan", 2600, 3000),
			stageAt("script.engine", 3000, 82000),
			stageAt("script.postprocess", 82000, 85000),
			stageAt("persistence", 82010, 82300),
			stageAt("persistence.sqlite", 82020, 82290),
			stageAt("document.publish", 83000, 84000),
			stageAt("audio.pipeline", 85000, 86000),
		},
		Operations: []OperationReport{
			{Stage: "script.engine", Component: "ollama", Operation: "generate", DurationMs: 78000},
			{Stage: "source.resolve", Component: "qdrant", Operation: "search", DurationMs: 430},
			{Stage: "source.resolve", Component: "sqlite", Operation: "hydrate", DurationMs: 1200},
		},
	}

	bd := report.Breakdown()

	// Top-level stages only: prepare + engine + postprocess + audio.pipeline.
	wantAttributed := int64(3000 + 79000 + 3000 + 1000)
	if bd.AttributedStageMs != wantAttributed {
		t.Fatalf("AttributedStageMs = %d, want %d (nested durations must not be summed)", bd.AttributedStageMs, wantAttributed)
	}
	if bd.UnattributedMs != 721 {
		t.Fatalf("UnattributedMs = %d, want 721", bd.UnattributedMs)
	}
	wantPercent := float64(721) / float64(86721) * 100
	if math.Abs(bd.UnattributedPercent-wantPercent) > 1e-9 {
		t.Fatalf("UnattributedPercent = %v, want %v", bd.UnattributedPercent, wantPercent)
	}
	if bd.BottleneckStage != "script.engine" {
		t.Fatalf("BottleneckStage = %q, want script.engine", bd.BottleneckStage)
	}
	if bd.BottleneckOperation != "ollama.generate" {
		t.Fatalf("BottleneckOperation = %q, want ollama.generate", bd.BottleneckOperation)
	}
}

// TestBreakdown_UnattributedTime pins unattributed_ms = wall - top-level.
func TestBreakdown_UnattributedTime(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 10000,
		Stages: []StageReport{
			stageAt("script.prepare", 0, 2000),
			stageAt("script.engine", 2000, 7000),
		},
	}
	bd := report.Breakdown()
	if bd.AttributedStageMs != 7000 {
		t.Fatalf("AttributedStageMs = %d, want 7000", bd.AttributedStageMs)
	}
	if bd.UnattributedMs != 3000 {
		t.Fatalf("UnattributedMs = %d, want 3000", bd.UnattributedMs)
	}
	if math.Abs(bd.UnattributedPercent-30.0) > 1e-9 {
		t.Fatalf("UnattributedPercent = %v, want 30.0", bd.UnattributedPercent)
	}
}

// TestBreakdown_NoDoubleCountingNestedStages pins the spec rule: a parent
// stage's duration already includes its children, so children never count.
func TestBreakdown_NoDoubleCountingNestedStages(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 3000,
		Stages: []StageReport{
			stageAt("script.prepare", 0, 3000),
			stageAt("script.normalize", 0, 1000),
			stageAt("script.validate", 1000, 2000),
			stageAt("script.plan", 2000, 3000),
		},
	}
	bd := report.Breakdown()
	if bd.AttributedStageMs != 3000 {
		t.Fatalf("AttributedStageMs = %d, want 3000 (children must not double-count)", bd.AttributedStageMs)
	}
	if bd.UnattributedMs != 0 {
		t.Fatalf("UnattributedMs = %d, want 0", bd.UnattributedMs)
	}
}

// TestBreakdown_UnanchoredStagesAreTopLevel: a stage without an interval
// cannot be nested, so it is attributed at the top level.
func TestBreakdown_UnanchoredStagesAreTopLevel(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 500,
		Stages: []StageReport{
			{Name: "legacy.stage", Status: StageStatusCompleted, DurationMs: 400},
		},
	}
	bd := report.Breakdown()
	if bd.AttributedStageMs != 400 {
		t.Fatalf("AttributedStageMs = %d, want 400", bd.AttributedStageMs)
	}
	if bd.BottleneckStage != "legacy.stage" {
		t.Fatalf("BottleneckStage = %q, want legacy.stage", bd.BottleneckStage)
	}
	if bd.BottleneckOperation != "" {
		t.Fatalf("BottleneckOperation = %q, want empty", bd.BottleneckOperation)
	}
}

// TestBreakdown_ZeroWallIsSafe pins no NaN/negative on an empty run.
func TestBreakdown_ZeroWallIsSafe(t *testing.T) {
	bd := (&RunReport{}).Breakdown()
	if bd.UnattributedPercent != 0 {
		t.Fatalf("UnattributedPercent = %v, want 0", bd.UnattributedPercent)
	}
	if bd.AttributedStageMs != 0 || bd.UnattributedMs != 0 {
		t.Fatalf("empty report must attribute nothing, got %+v", bd)
	}
	if bd.BottleneckStage != "" || bd.BottleneckOperation != "" {
		t.Fatalf("empty report must have no bottleneck, got %+v", bd)
	}
	var nilReport *RunReport
	if bd := nilReport.Breakdown(); bd.UnattributedPercent != 0 || bd.BottleneckStage != "" {
		t.Fatalf("nil report breakdown must be zero, got %+v", bd)
	}
}

// TestBreakdown_DominantOperationPrefersLargestDuration pins that the
// dominant operation is the max-duration operation under the stage.
func TestBreakdown_DominantOperationPrefersLargestDuration(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 10000,
		Stages:     []StageReport{stageAt("script.engine", 0, 9000)},
		Operations: []OperationReport{
			{Stage: "script.engine", Component: "ollama", Operation: "generate", DurationMs: 1000},
			{Stage: "script.engine", Component: "ollama", Operation: "generate", DurationMs: 8000},
		},
	}
	bd := report.Breakdown()
	if bd.BottleneckOperation != "ollama.generate" {
		t.Fatalf("BottleneckOperation = %q, want ollama.generate", bd.BottleneckOperation)
	}
}
