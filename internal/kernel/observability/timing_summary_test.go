package observability

import (
	"math"
	"testing"
)

// TestTimingSummary_WallAndBreakdown pins that the summary carries the
// canonical wall/attributed/unattributed/bottleneck values derived from
// top-level stages only (nested durations never counted).
func TestTimingSummary_WallAndBreakdown(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 86721,
		Stages: []StageReport{
			stageAt("script.prepare", 0, 3000),
			stageAt("source.resolve", 20, 2600),
			stageAt("script.engine", 3000, 82000),
			stageAt("script.postprocess", 82000, 85000),
			stageAt("audio.pipeline", 85000, 86000),
		},
		Operations: []OperationReport{
			{Stage: "script.engine", Component: "ollama", Operation: "generate", DurationMs: 78000},
		},
	}

	s := report.TimingSummary()

	if s.WallMs != 86721 {
		t.Fatalf("WallMs = %d, want 86721", s.WallMs)
	}
	wantAttributed := int64(3000 + 79000 + 3000 + 1000)
	if s.AttributedMs != wantAttributed {
		t.Fatalf("AttributedMs = %d, want %d", s.AttributedMs, wantAttributed)
	}
	if s.UnattributedMs != 721 {
		t.Fatalf("UnattributedMs = %d, want 721", s.UnattributedMs)
	}
	wantPercent := float64(721) / float64(86721) * 100
	if math.Abs(s.UnattributedPercent-wantPercent) > 1e-9 {
		t.Fatalf("UnattributedPercent = %v, want %v", s.UnattributedPercent, wantPercent)
	}
	if s.BottleneckStage != "script.engine" {
		t.Fatalf("BottleneckStage = %q, want script.engine", s.BottleneckStage)
	}
	if s.BottleneckOperation != "ollama.generate" {
		t.Fatalf("BottleneckOperation = %q, want ollama.generate", s.BottleneckOperation)
	}
}

// TestTimingSummary_NestedStagesIncluded pins that nested boundaries
// (persistence.sqlite / document.publish) still appear in the exhaustive
// stages list even though they are excluded from top-level attribution.
func TestTimingSummary_NestedStagesIncluded(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 85000,
		Stages: []StageReport{
			stageAt("script.postprocess", 82000, 85000),
			stageAt("persistence", 82010, 82300),
			stageAt("persistence.sqlite", 82020, 82290),
			stageAt("document.publish", 83000, 84000),
		},
	}

	s := report.TimingSummary()

	names := make(map[string]int64, len(s.Stages))
	for _, st := range s.Stages {
		names[st.Name] = st.DurationMs
	}
	if _, ok := names["persistence.sqlite"]; !ok {
		t.Fatalf("stages list must include nested persistence.sqlite, got %+v", s.Stages)
	}
	if _, ok := names["document.publish"]; !ok {
		t.Fatalf("stages list must include nested document.publish, got %+v", s.Stages)
	}
	// Nested stage durations are NOT part of top-level attribution.
	wantAttributed := int64(3000) // only script.postprocess is top-level
	if s.AttributedMs != wantAttributed {
		t.Fatalf("AttributedMs = %d, want %d", s.AttributedMs, wantAttributed)
	}
}

// TestTimingSummary_OperationsAggregateCallsAndWork pins call counts and
// accumulated work per component.operation, and that accumulated work is
// never reported as wall time.
func TestTimingSummary_OperationsAggregateCallsAndWork(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 5210,
		Stages: []StageReport{
			stageAt("voiceover.generate", 0, 5210),
		},
		Operations: []OperationReport{
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4000},
			{Stage: "voiceover.generate", Component: "edge_tts", Operation: "synthesize", DurationMs: 4400},
			{Stage: "source.resolve", Component: "qdrant", Operation: "search", DurationMs: 430},
			{Stage: "source.resolve", Component: "sqlite", Operation: "hydrate", DurationMs: 1200},
		},
	}

	s := report.TimingSummary()

	byKey := make(map[string]TimingOperation, len(s.Operations))
	for _, op := range s.Operations {
		byKey[op.Component+"."+op.Operation] = op
	}
	tts := byKey["edge_tts.synthesize"]
	if tts.Calls != 2 {
		t.Fatalf("edge_tts.synthesize Calls = %d, want 2", tts.Calls)
	}
	if tts.WorkMs != 8400 {
		t.Fatalf("edge_tts.synthesize WorkMs = %d, want 8400", tts.WorkMs)
	}
	// Accumulated parallel work must never be mistaken for wall time.
	if tts.WorkMs <= s.WallMs {
		t.Fatalf("parallel work (%d) is expected to exceed wall (%d)", tts.WorkMs, s.WallMs)
	}
	qdrant := byKey["qdrant.search"]
	if qdrant.Calls != 1 || qdrant.WorkMs != 430 {
		t.Fatalf("qdrant.search = %+v, want Calls=1 WorkMs=430", qdrant)
	}
	sqlite := byKey["sqlite.hydrate"]
	if sqlite.Calls != 1 || sqlite.WorkMs != 1200 {
		t.Fatalf("sqlite.hydrate = %+v, want Calls=1 WorkMs=1200", sqlite)
	}
	// Operations sorted deterministically by component then operation.
	if len(s.Operations) > 0 {
		prev := ""
		for _, op := range s.Operations {
			key := op.Component + "\x00" + op.Operation
			if prev != "" && key < prev {
				t.Fatalf("operations not sorted: %q before %q", key, prev)
			}
			prev = key
		}
	}
}

// TestTimingSummary_OperationsMergeMetadata pins the metadata_json merge:
// numeric facts (tokens, model_load_ms, inference_work_ms) accumulate across
// the fan-out, booleans (cold_start) count, uniform strings (model) stay,
// and queue_wait_ms accumulates — so the benchmark can split the coarse
// Ollama wall without a second timer.
func TestTimingSummary_OperationsMergeMetadata(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 5210,
		Stages: []StageReport{
			stageAt("generate", 0, 5210),
		},
		Operations: []OperationReport{
			{
				Stage: "generate", Component: "ollama", Operation: "generate",
				DurationMs: 4000, QueueWaitMs: 50,
				MetadataJSON: `{"model":"gemma3:1b","input_tokens":120,"output_tokens":340,"model_load_ms":45000,"inference_wall_ms":3900,"inference_work_ms":3600,"cold_start":true}`,
			},
			{
				Stage: "generate", Component: "ollama", Operation: "generate",
				DurationMs: 4400, QueueWaitMs: 120,
				MetadataJSON: `{"model":"gemma3:1b","input_tokens":130,"output_tokens":410,"model_load_ms":0,"inference_wall_ms":4300,"inference_work_ms":4100,"cold_start":false}`,
			},
		},
	}

	s := report.TimingSummary()
	if len(s.Operations) != 1 {
		t.Fatalf("operations = %d, want 1 aggregated", len(s.Operations))
	}
	op := s.Operations[0]
	if op.Calls != 2 || op.WorkMs != 8400 {
		t.Fatalf("calls/work = %d/%d, want 2/8400", op.Calls, op.WorkMs)
	}
	if op.QueueWaitMs != 170 {
		t.Fatalf("queue_wait_ms = %d, want 170", op.QueueWaitMs)
	}
	if report.TimingSummary().ExecutionWallMs != report.TimingSummary().WallMs {
		t.Fatalf("execution_wall_ms = %d, want wall_ms %d", report.TimingSummary().ExecutionWallMs, report.TimingSummary().WallMs)
	}
	m := op.Metadata
	if m == nil {
		t.Fatal("metadata must be merged")
	}
	if m["input_tokens"] != float64(250) {
		t.Errorf("input_tokens = %v, want 250", m["input_tokens"])
	}
	if m["output_tokens"] != float64(750) {
		t.Errorf("output_tokens = %v, want 750", m["output_tokens"])
	}
	if m["model_load_ms"] != float64(45000) {
		t.Errorf("model_load_ms = %v, want 45000 (cold load counts once)", m["model_load_ms"])
	}
	if m["inference_work_ms"] != float64(7700) {
		t.Errorf("inference_work_ms = %v, want 7700", m["inference_work_ms"])
	}
	if m["cold_start"] != float64(1) {
		t.Errorf("cold_start = %v, want 1 (one cold call)", m["cold_start"])
	}
	if m["model"] != "gemma3:1b" {
		t.Errorf("model = %v, want gemma3:1b (uniform string kept)", m["model"])
	}
}

// TestTimingSummary_NilSafe pins zero-value safety for nil and empty reports.
func TestTimingSummary_NilSafe(t *testing.T) {
	var nilReport *RunReport
	if s := nilReport.TimingSummary(); s.WallMs != 0 || s.BottleneckStage != "" || s.Stages != nil || s.Operations != nil {
		t.Fatalf("nil report summary must be zero, got %+v", s)
	}
	if s := (&RunReport{}).TimingSummary(); s.WallMs != 0 || s.UnattributedPercent != 0 {
		t.Fatalf("empty report summary must be zero, got %+v", s)
	}
}

// TestTimingSummary_CarriesCriticalPathAndBottleneckPercent pins that the
// summary surfaces the ordered critical path and the bottleneck percentage.
func TestTimingSummary_CarriesCriticalPathAndBottleneckPercent(t *testing.T) {
	report := &RunReport{
		WallTimeMs: 86721,
		Stages: []StageReport{
			stageAt("script.prepare", 0, 3000),
			stageAt("script.engine", 3000, 82000),
			stageAt("script.postprocess", 82000, 85000),
			stageAt("audio.pipeline", 85000, 86000),
		},
	}
	s := report.TimingSummary()
	if s.BottleneckStage != "script.engine" {
		t.Fatalf("BottleneckStage = %q, want script.engine", s.BottleneckStage)
	}
	want := float64(79000) / float64(86721) * 100
	if math.Abs(s.BottleneckPercent-want) > 1e-9 {
		t.Fatalf("BottleneckPercent = %v, want %v", s.BottleneckPercent, want)
	}
	if len(s.CriticalPath) != 4 || s.CriticalPath[1].Name != "script.engine" {
		t.Fatalf("CriticalPath = %+v, want 4 ordered stages with script.engine second", s.CriticalPath)
	}
}

// TestTimingSummary_FormatCriticalPath pins the compact single-line rendering
// used by logs, including one-decimal percentages and empty-path safety.
func TestTimingSummary_FormatCriticalPath(t *testing.T) {
	got := (TimingSummary{CriticalPath: []CriticalPathStage{
		{Name: "script.prepare", DurationMs: 3000, Percent: 3.5},
		{Name: "script.engine", DurationMs: 79000, Percent: 91.1},
		{Name: "audio.pipeline", DurationMs: 1000, Percent: 1.2},
	}}).FormatCriticalPath()
	want := "script.prepare(3.5%) > script.engine(91.1%) > audio.pipeline(1.2%)"
	if got != want {
		t.Fatalf("FormatCriticalPath = %q, want %q", got, want)
	}
	if got := (TimingSummary{}).FormatCriticalPath(); got != "" {
		t.Fatalf("empty summary FormatCriticalPath = %q, want empty", got)
	}
}
