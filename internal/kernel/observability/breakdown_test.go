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
			stageAt("audio.pipeline", 85000, 86000),
		},
		Operations: []OperationReport{
			{Stage: "script.engine", Component: "ollama", Operation: "generate", DurationMs: 78000},
			{Stage: "source.resolve", Component: "pgvector", Operation: "search", DurationMs: 430},
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

func TestBreakdown_IndependentContainedSiblingIsNotNested(t *testing.T) {
	audio := stageAt("audio_compile", 0, 100)
	overlay := stageAt("overlay_render", 10, 40)
	overlay.Independent = true
	report := &RunReport{WallTimeMs: 100, Stages: []StageReport{audio, overlay, stageAt("nested_probe", 20, 25)}}
	bd := report.Breakdown()
	if len(bd.CriticalPath) != 2 || bd.AttributedStageMs != 100 || bd.OverlappedMs != 30 {
		t.Fatalf("independent sibling lost or double-counted: %+v", bd)
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

func TestBreakdown_EqualIntervalsRemainTopLevel(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 1000,
		Stages: []StageReport{
			stageAt("parallel.a", 0, 1000),
			stageAt("parallel.b", 0, 1000),
		},
	}
	bd := report.Breakdown()
	if bd.AttributedStageMs != 1000 {
		t.Fatalf("AttributedStageMs = %d, want 1000 (equal intervals share the covered wall)", bd.AttributedStageMs)
	}
	if bd.OverlappedMs != 1000 {
		t.Fatalf("OverlappedMs = %d, want 1000 (equal intervals are concurrent top-level stages)", bd.OverlappedMs)
	}
	if len(bd.CriticalPath) != 2 {
		t.Fatalf("CriticalPath = %+v, want both equal-interval stages", bd.CriticalPath)
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

// TestBreakdown_AttributedIsWallBoundedUnderOverlap pins the honest-overlap
// contract: when top-level phases run concurrently, attributed_ms is the WALL
// TIME they covered (the union), never the sum of their durations, so it can
// never exceed total_wall_ms. The concurrency excess is reported as
// OverlappedMs instead of hiding in an inflated attributed budget.
func TestBreakdown_AttributedIsWallBoundedUnderOverlap(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 10000,
		Stages: []StageReport{
			// A(0..6000) and B(4000..10000) overlap by 2000ms: the streaming
			// pipeline overlaps generate with translate/TTS/render exactly like
			// this. Summed durations = 12000 > wall = 10000.
			stageAt("generate", 0, 6000),
			stageAt("voiceover", 4000, 10000),
		},
	}
	bd := report.Breakdown()
	if bd.AttributedStageMs != 10000 {
		t.Fatalf("AttributedStageMs = %d, want 10000 (union of the wall, not the 12000 sum)", bd.AttributedStageMs)
	}
	if bd.AttributedStageMs > report.WallTimeMs {
		t.Fatalf("AttributedStageMs = %d must never exceed wall %d", bd.AttributedStageMs, report.WallTimeMs)
	}
	if bd.OverlappedMs != 2000 {
		t.Fatalf("OverlappedMs = %d, want 2000 (the concurrent excess)", bd.OverlappedMs)
	}
	if bd.UnattributedMs != 0 {
		t.Fatalf("UnattributedMs = %d, want 0 (the wall is fully owned)", bd.UnattributedMs)
	}
}

// TestBreakdown_SequentialStagesHaveNoOverlap pins that a strictly sequential
// pipeline reports zero overlap — the honest-overlap change must not invent
// concurrency where there is none.
func TestBreakdown_SequentialStagesHaveNoOverlap(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 10000,
		Stages: []StageReport{
			stageAt("a", 0, 4000),
			stageAt("b", 4000, 9000),
		},
	}
	bd := report.Breakdown()
	if bd.OverlappedMs != 0 {
		t.Fatalf("OverlappedMs = %d, want 0 for sequential stages", bd.OverlappedMs)
	}
	if bd.AttributedStageMs != 9000 {
		t.Fatalf("AttributedStageMs = %d, want 9000", bd.AttributedStageMs)
	}
	if bd.UnattributedMs != 1000 {
		t.Fatalf("UnattributedMs = %d, want 1000", bd.UnattributedMs)
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

// TestBreakdown_BottleneckPercent pins that the bottleneck stage's share of
// total wall time is reported as a percentage, and stays 0 when wall is 0.
func TestBreakdown_BottleneckPercent(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 86721,
		Stages: []StageReport{
			stageAt("script.prepare", 0, 3000),
			stageAt("script.engine", 3000, 82000),
			stageAt("audio.pipeline", 85000, 86000),
		},
	}
	bd := report.Breakdown()
	if bd.BottleneckStage != "script.engine" {
		t.Fatalf("BottleneckStage = %q, want script.engine", bd.BottleneckStage)
	}
	want := float64(79000) / float64(86721) * 100
	if math.Abs(bd.BottleneckPercent-want) > 1e-9 {
		t.Fatalf("BottleneckPercent = %v, want %v", bd.BottleneckPercent, want)
	}
	if (&RunReport{}).Breakdown().BottleneckPercent != 0 {
		t.Fatal("zero-wall report must have BottleneckPercent = 0")
	}
}

// TestBreakdown_CriticalPathOrderedByWallStart pins that the critical path is
// the top-level stages ordered by wall-time start (execution order), each with
// its percentage of total wall.
func TestBreakdown_CriticalPathOrderedByWallStart(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 100000,
		Stages: []StageReport{
			// Deliberately out of execution order: the critical path must
			// re-order by StartedAt, not preserve input order.
			stageAt("audio.pipeline", 85000, 90000),
			stageAt("script.engine", 3000, 82000),
			stageAt("script.prepare", 0, 3000),
			stageAt("script.postprocess", 82000, 85000),
		},
	}
	bd := report.Breakdown()
	if len(bd.CriticalPath) != 4 {
		t.Fatalf("CriticalPath len = %d, want 4", len(bd.CriticalPath))
	}
	wantOrder := []string{"script.prepare", "script.engine", "script.postprocess", "audio.pipeline"}
	for i, want := range wantOrder {
		if bd.CriticalPath[i].Name != want {
			t.Fatalf("CriticalPath[%d] = %q, want %q (order %+v)", i, bd.CriticalPath[i].Name, want, bd.CriticalPath)
		}
	}
	engine := bd.CriticalPath[1]
	if engine.DurationMs != 79000 {
		t.Fatalf("engine DurationMs = %d, want 79000", engine.DurationMs)
	}
	wantPct := float64(79000) / float64(100000) * 100
	if math.Abs(engine.Percent-wantPct) > 1e-9 {
		t.Fatalf("engine Percent = %v, want %v", engine.Percent, wantPct)
	}
}

// TestBreakdown_CriticalPathCarriesOffsetsAndGaps pins the timeline half of the
// critical path: a strictly sequential chain is back-to-back (gap 0) and a real
// idle stretch between phases is reported as a measured gap, not inferred.
func TestBreakdown_CriticalPathCarriesOffsetsAndGaps(t *testing.T) {
	report := &RunReport{
		StartedAt:  time.Unix(1_700_000_000, 0),
		WallTimeMs: 10_000,
		Stages: []StageReport{
			stageAt("generate", 0, 4_000),
			stageAt("audio_compile", 4_500, 7_000),
			stageAt("remote_final_job", 7_000, 10_000),
		},
	}
	bd := report.Breakdown()
	if len(bd.CriticalPath) != 3 {
		t.Fatalf("CriticalPath len = %d, want 3", len(bd.CriticalPath))
	}
	for i, cp := range bd.CriticalPath {
		if !cp.Anchored {
			t.Fatalf("CriticalPath[%d] (%s) is not anchored", i, cp.Name)
		}
	}
	if got := bd.CriticalPath[0]; got.StartOffsetMs != 0 || got.EndOffsetMs != 4_000 || got.GapBeforeMs != 0 {
		t.Fatalf("generate offsets = %d..%d gap %d, want 0..4000 gap 0", got.StartOffsetMs, got.EndOffsetMs, got.GapBeforeMs)
	}
	if got := bd.CriticalPath[1]; got.StartOffsetMs != 4_500 || got.EndOffsetMs != 7_000 || got.GapBeforeMs != 500 {
		t.Fatalf("audio offsets = %d..%d gap %d, want 4500..7000 gap 500", got.StartOffsetMs, got.EndOffsetMs, got.GapBeforeMs)
	}
	if got := bd.CriticalPath[2]; got.StartOffsetMs != 7_000 || got.GapBeforeMs != 0 {
		t.Fatalf("final job offsets = %d gap %d, want 7000 gap 0 (back-to-back)", got.StartOffsetMs, got.GapBeforeMs)
	}
}

// TestBreakdown_CriticalPathOverlapIsNotAGap pins the concurrency signal: two
// top-level phases whose intervals overlap produce OverlappedMs and a zero gap,
// so a reader can tell parallel work from serial work.
func TestBreakdown_CriticalPathOverlapIsNotAGap(t *testing.T) {
	parallel := stageAt("overlay_render", 3_000, 6_000)
	parallel.Independent = true
	report := &RunReport{
		StartedAt:  time.Unix(1_700_000_000, 0),
		WallTimeMs: 6_000,
		Stages:     []StageReport{stageAt("generate", 0, 4_000), parallel},
	}
	bd := report.Breakdown()
	if bd.OverlappedMs != 1_000 {
		t.Fatalf("OverlappedMs = %d, want 1000 (4000 + 3000 work over 6000 wall)", bd.OverlappedMs)
	}
	if len(bd.CriticalPath) != 2 {
		t.Fatalf("CriticalPath len = %d, want 2", len(bd.CriticalPath))
	}
	second := bd.CriticalPath[1]
	if second.StartOffsetMs != 3_000 || second.GapBeforeMs != 0 {
		t.Fatalf("parallel stage = start %d gap %d, want start 3000 gap 0 (overlap, not idle)", second.StartOffsetMs, second.GapBeforeMs)
	}
}

// TestBreakdown_UnanchoredStageHasNoOffsets pins the no-fake-availability rule:
// a stage without a start/finish pair is not placed at offset 0 and cannot
// fabricate a gap.
func TestBreakdown_UnanchoredStageHasNoOffsets(t *testing.T) {
	report := &RunReport{
		StartedAt:  time.Unix(1_700_000_000, 0),
		WallTimeMs: 5_000,
		Stages: []StageReport{
			stageAt("generate", 0, 5_000),
			{Name: "legacy_stage", DurationMs: 1_000},
		},
	}
	bd := report.Breakdown()
	if len(bd.CriticalPath) != 2 {
		t.Fatalf("CriticalPath len = %d, want 2", len(bd.CriticalPath))
	}
	legacy := bd.CriticalPath[1]
	if legacy.Name != "legacy_stage" {
		t.Fatalf("CriticalPath[1] = %q, want the unanchored stage last", legacy.Name)
	}
	if legacy.Anchored || legacy.StartOffsetMs != 0 || legacy.EndOffsetMs != 0 || legacy.GapBeforeMs != 0 {
		t.Fatalf("unanchored stage invented a position: %+v", legacy)
	}
	if got := bd.CriticalPath[0]; !got.Anchored || got.EndOffsetMs != 5_000 {
		t.Fatalf("anchored stage lost its offsets: %+v", got)
	}
}

// TestBreakdown_CriticalPathNilAndEmptyIsSafe pins nil/empty safety: a report
// with no top-level stages yields no critical path.
func TestBreakdown_CriticalPathNilAndEmptyIsSafe(t *testing.T) {
	var nilReport *RunReport
	if bd := nilReport.Breakdown(); bd.CriticalPath != nil || bd.BottleneckPercent != 0 {
		t.Fatalf("nil report breakdown must be zero, got %+v", bd)
	}
	if bd := (&RunReport{WallTimeMs: 1000}).Breakdown(); bd.CriticalPath != nil {
		t.Fatalf("empty report must have nil critical path, got %+v", bd.CriticalPath)
	}
}
