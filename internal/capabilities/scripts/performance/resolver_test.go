package performance

import (
	"testing"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func fullAudio() scriptgeneration.AudioPipelineMetrics {
	return scriptgeneration.AudioPipelineMetrics{
		TTSMS:               120,
		TimelineCompileMS:   30,
		AudioAssetResolveMS: 0,
		ClipAudioPrepareMS:  40,
		AudioPlanCompileMS:  25,
		MixMS:               5000,
		AACEncodeMS:         8000,
		ProbeMS:             390,
		HashMS:              280,
		UploadMS:            3700,
		TotalMS:             18000,
		AudioDurationMS:     90000,
		TTSCalls:            14,
		AudioRTF:            0.2,
		TTSScenes:           []scriptgeneration.TTSSSceneMetric{{SceneID: "s1"}},
	}
}

func TestResolveMapsCanonicalSources(t *testing.T) {
	run := kernobs.RunReport{
		QueueWaitMs: 1850,
		BlockedMs:   0,
		Operations: []kernobs.OperationReport{
			{Component: string(kernobs.ComponentOllama), Operation: string(kernobs.OperationGenerate), DurationMs: 9000},
			{Component: string(kernobs.ComponentOllama), Operation: string(kernobs.OperationGenerate), DurationMs: 7000},
		},
		Waits: []kernobs.WaitReport{
			{Kind: kernobs.WaitChildDependency, Component: string(kernobs.ComponentTTS), DurationMs: 340},
		},
	}
	steps := []scriptgeneration.ExecutionStep{
		{Name: "DOCUMENT", Status: "COMPLETED", DurationMS: 1241},
		{Name: "VELOX_ENQUEUE", Status: "COMPLETED", DurationMS: 115},
	}

	got := DefaultPhaseResolver{}.Resolve(run, fullAudio(), steps)
	byPhase := make(map[PerformancePhase]PhaseMeasurement, len(got))
	for _, m := range got {
		byPhase[m.Phase] = m
	}

	if len(got) != len(Phases()) {
		t.Fatalf("resolved %d phases, want %d", len(got), len(Phases()))
	}

	assertMeasured := func(phase PerformancePhase, wantMS int64) {
		t.Helper()
		m, ok := byPhase[phase]
		if !ok {
			t.Fatalf("phase %q missing from resolution", phase)
		}
		if !m.Measured {
			t.Errorf("phase %q not measured, want measured", phase)
		}
		if m.DurationMS != wantMS {
			t.Errorf("phase %q duration = %d, want %d", phase, m.DurationMS, wantMS)
		}
	}

	assertMeasured(PhaseScriptGemma, 16000) // 9000 + 7000 summed
	assertMeasured(PhaseEdgeTTS, 120)
	assertMeasured(PhaseAudioPrepare, 70) // 30 + 40
	assertMeasured(PhaseAudioPlan, 25)
	assertMeasured(PhaseRustMix, 5000)
	assertMeasured(PhaseRustEncode, 8000)
	assertMeasured(PhaseProbe, 390)
	assertMeasured(PhaseHash, 280)
	assertMeasured(PhaseUpload, 3700)
	assertMeasured(PhaseGoogleDoc, 1241)
	assertMeasured(PhaseRenderEnqueue, 115)

	tts := byPhase[PhaseEdgeTTS]
	if got := tts.Counters["tts_calls"]; got != 14 {
		t.Errorf("tts_calls counter = %v, want 14", got)
	}
	if got := tts.Counters["tts_scenes"]; got != 1 {
		t.Errorf("tts_scenes counter = %v, want 1", got)
	}

	w := waitSummary(run)
	if w.QueueMS != 1850 || w.BlockedMS != 0 || w.CompletionMS != 340 {
		t.Errorf("waits = %+v, want queue=1850 blocked=0 completion=340", w)
	}
	if len(w.Items) != 1 {
		t.Errorf("wait items = %d, want 1", len(w.Items))
	}
}

func TestWaitSummarySeparatesCompletionAndOutboxDelivery(t *testing.T) {
	run := kernobs.RunReport{
		QueueWaitMs: 1850,
		BlockedMs:   500,
		Waits: []kernobs.WaitReport{
			{Kind: kernobs.WaitChildDependency, DurationMs: 100},
			{Kind: kernobs.WaitCompletion, Component: string(kernobs.ComponentRenderQueue), DurationMs: 240},
			{Kind: kernobs.WaitOutboxDelivery, DurationMs: 75},
		},
	}

	w := waitSummary(run)
	if w.QueueMS != 1850 || w.BlockedMS != 500 {
		t.Errorf("queue/blocked = %d/%d, want 1850/500", w.QueueMS, w.BlockedMS)
	}
	if w.CompletionMS != 340 { // child_dependency 100 + completion 240
		t.Errorf("completion = %d, want 340", w.CompletionMS)
	}
	if w.OutboxDeliveryMS != 75 {
		t.Errorf("outbox_delivery = %d, want 75", w.OutboxDeliveryMS)
	}
	if len(w.Items) != 3 {
		t.Errorf("wait items = %d, want 3", len(w.Items))
	}
}

func TestResolveMarksMissingSourcesUnmeasured(t *testing.T) {
	run := kernobs.RunReport{}
	got := DefaultPhaseResolver{}.Resolve(run, scriptgeneration.AudioPipelineMetrics{}, nil)
	for _, m := range got {
		if m.Measured {
			t.Errorf("phase %q unexpectedly measured with empty inputs", m.Phase)
		}
		if m.DurationMS != 0 {
			t.Errorf("phase %q duration = %d, want 0 when unmeasured", m.Phase, m.DurationMS)
		}
	}
}

func TestResolveIgnoresNonCompletedSteps(t *testing.T) {
	steps := []scriptgeneration.ExecutionStep{
		{Name: "DOCUMENT", Status: "FAILED", DurationMS: 999},
		{Name: "DOCUMENT", Status: "COMPLETED", DurationMS: 1241},
	}
	got := DefaultPhaseResolver{}.Resolve(kernobs.RunReport{}, scriptgeneration.AudioPipelineMetrics{}, steps)
	for _, m := range got {
		if m.Phase == PhaseGoogleDoc {
			if m.DurationMS != 1241 || !m.Measured {
				t.Errorf("google_doc = %+v, want the COMPLETED step (1241)", m)
			}
		}
	}
}

func TestScriptSummaryBreaksDownTotalInferenceOverhead(t *testing.T) {
	run := kernobs.RunReport{
		Operations: []kernobs.OperationReport{
			{Component: string(kernobs.ComponentOllama), Operation: string(kernobs.OperationGenerate), DurationMs: 9000},
			{Component: string(kernobs.ComponentOllama), Operation: string(kernobs.OperationGenerate), DurationMs: 7000},
			// A non-generate operation must not inflate inference.
			{Component: string(kernobs.ComponentDrive), Operation: string(kernobs.OperationUpload), DurationMs: 999},
		},
	}
	steps := []scriptgeneration.ExecutionStep{
		{Name: "SCRIPT", Status: "COMPLETED", DurationMS: 24000},
		{Name: "SCRIPT", Status: "FAILED", DurationMS: 1},
	}

	s := scriptSummary(run, steps)
	if s.TotalMS != 24000 {
		t.Errorf("total = %d, want 24000", s.TotalMS)
	}
	if s.InferenceMS != 16000 {
		t.Errorf("inference = %d, want 16000", s.InferenceMS)
	}
	if s.OverheadMS != 8000 {
		t.Errorf("overhead = %d, want 8000", s.OverheadMS)
	}
}

func TestScriptSummaryClampsNegativeOverhead(t *testing.T) {
	run := kernobs.RunReport{
		Operations: []kernobs.OperationReport{
			{Component: string(kernobs.ComponentOllama), Operation: string(kernobs.OperationGenerate), DurationMs: 9000},
		},
	}
	steps := []scriptgeneration.ExecutionStep{
		{Name: "SCRIPT", Status: "COMPLETED", DurationMS: 5000},
	}
	s := scriptSummary(run, steps)
	if s.TotalMS != 5000 || s.InferenceMS != 9000 {
		t.Errorf("summary = %+v, want total=5000 inference=9000", s)
	}
	if s.OverheadMS != 0 {
		t.Errorf("overhead = %d, want 0 (clamped)", s.OverheadMS)
	}
}

func TestScriptSummaryEmptyWhenNoSources(t *testing.T) {
	s := scriptSummary(kernobs.RunReport{}, nil)
	if s.TotalMS != 0 || s.InferenceMS != 0 || s.OverheadMS != 0 {
		t.Errorf("summary = %+v, want all zero", s)
	}
}

func TestAudioSummaryProjectsOnly(t *testing.T) {
	a := audioSummary(fullAudio())
	if a.DurationMS != 90000 || a.TTSCalls != 14 || a.TTSScenes != 1 || a.RTF != 0.2 {
		t.Errorf("audio summary = %+v", a)
	}

	// A zero-value audio must not derive an RTF out of thin air.
	empty := audioSummary(scriptgeneration.AudioPipelineMetrics{TotalMS: 1000, AudioDurationMS: 100})
	if empty.RTF != 0 {
		t.Errorf("audio summary RTF = %v, want 0 (project-only, no derivation)", empty.RTF)
	}
}
