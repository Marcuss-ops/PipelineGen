package performance

import (
	"context"
	"encoding/json"
	"testing"

	scriptgeneration "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	perf "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/performance"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

func TestAggregatorProjectsRunAndAudioEndToEnd(t *testing.T) {
	jobs, obs := testSourceDBs(t)
	defer jobs.Close()
	defer obs.Close()

	jobID := "job-e2e"
	report := kernobs.RunReport{
		RunID:       "run-e2e",
		JobID:       jobID,
		JobType:     "script.generate",
		Status:      kernobs.StatusSucceeded,
		WallTimeMs:  87431,
		QueueWaitMs: 1850,
		Operations: []kernobs.OperationReport{
			{Component: string(kernobs.ComponentOllama), Operation: string(kernobs.OperationGenerate), DurationMs: 18340, Items: 1},
			{Stage: "voiceover", Component: string(kernobs.ComponentTTS), Operation: string(kernobs.OperationSynthesize), DurationMs: 12410, Items: 14},
			{Stage: "audio_compile", Component: "audio", Operation: "timeline_compile", DurationMs: 900},
			{Stage: "audio_compile", Component: "audio", Operation: "clip_audio_prepare", DurationMs: 20},
			{Stage: "audio_compile", Component: "audio", Operation: "audio_plan_compile", DurationMs: 31},
			{Stage: "audio_compile", Component: "audio", Operation: "mix", DurationMs: 4120},
			{Stage: "audio_compile", Component: "audio", Operation: "aac_encode", DurationMs: 7130},
			{Stage: "audio_compile", Component: "audio", Operation: "probe", DurationMs: 281, OutputDurationMS: 93650},
			{Stage: "audio_compile", Component: "audio", Operation: "hash", DurationMs: 144},
			{Stage: "audio_compile", Component: "rust", Operation: "audio_render", DurationMs: 18400},
			{Component: string(kernobs.ComponentDrive), Stage: "audio_compile", Operation: string(kernobs.OperationUpload), DurationMs: 2380, Items: 1},
		},
		Waits: []kernobs.WaitReport{
			{Kind: kernobs.WaitChildDependency, DurationMs: 340},
		},
	}
	reportJSON, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := struct {
		Result *scriptgeneration.GenerateResult `json:"result,omitempty"`
	}{
		Result: &scriptgeneration.GenerateResult{
			AudioMetrics: &scriptgeneration.AudioPipelineMetrics{
				TTSMS:              12410,
				TimelineCompileMS:  900,
				ClipAudioPrepareMS: 20,
				AudioPlanCompileMS: 31,
				MixMS:              4120,
				AACEncodeMS:        7130,
				ProbeMS:            281,
				HashMS:             144,
				UploadMS:           2380,
				AudioDurationMS:    93650,
				AudioRTF:           0.2,
				TTSCalls:           14,
			},
		},
	}
	payloadJSON, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	now := "2026-08-14T10:00:00Z"
	if _, err := obs.Exec(`INSERT INTO run_observability (run_id,job_id,job_type,attempt_id,status,created_at,started_at,report_json,workflow_payload_json,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		report.RunID, jobID, report.JobType, "attempt-e2e", report.Status, now, now, string(reportJSON), string(payloadJSON), now); err != nil {
		t.Fatal(err)
	}
	for _, st := range []struct {
		id, name string
		dur      int64
	}{
		{"step-doc", "DOCUMENT", 1241},
		{"step-enq", "VELOX_ENQUEUE", 115},
	} {
		if _, err := jobs.Exec(`INSERT INTO job_steps (step_id,job_id,step_name,step_type,status,started_at,completed_at,duration_ms,input_count,output_count,input_bytes,output_bytes,metrics_json,error_code,error_message,created_at) VALUES (?,?,?,?,?,?,?,?,0,0,0,0,'{}','','',?)`,
			st.id, jobID, st.name, "phase", "COMPLETED", now, now, st.dur, now); err != nil {
			t.Fatalf("insert step %s: %v", st.id, err)
		}
	}

	src, err := NewSource(jobs, obs)
	if err != nil {
		t.Fatal(err)
	}
	agg := perf.NewAggregator(src, perf.DefaultPhaseResolver{})

	got, err := agg.BuildJobReport(context.Background(), jobID)
	if err != nil {
		t.Fatal(err)
	}

	if got.JobID != jobID || got.WallTimeMS != 87431 {
		t.Fatalf("envelope = %+v", got)
	}
	if len(got.Phases) != len(perf.Phases()) {
		t.Fatalf("phases = %d, want %d", len(got.Phases), len(perf.Phases()))
	}
	if got.Waits.QueueMS != 1850 || got.Waits.CompletionMS != 340 {
		t.Fatalf("waits = %+v", got.Waits)
	}
	if got.Audio.RTF != 18400.0/93650.0 || got.Audio.TTSCalls != 14 {
		t.Fatalf("audio = %+v", got.Audio)
	}

	want := map[perf.PerformancePhase]int64{
		perf.PhaseScriptGemma:   18340,
		perf.PhaseEdgeTTS:       12410,
		perf.PhaseAudioPrepare:  920,
		perf.PhaseAudioPlan:     31,
		perf.PhaseRustMix:       4120,
		perf.PhaseRustEncode:    7130,
		perf.PhaseProbe:         281,
		perf.PhaseHash:          144,
		perf.PhaseUpload:        2380,
		perf.PhaseGoogleDoc:     1241,
		perf.PhaseRenderEnqueue: 115,
	}
	for _, m := range got.Phases {
		expected, ok := want[m.Phase]
		if !ok {
			t.Fatalf("unexpected phase %q", m.Phase)
		}
		if !m.Measured {
			t.Errorf("phase %q should be measured, got unmeasured", m.Phase)
		}
		if m.DurationMS != expected {
			t.Errorf("phase %q = %d, want %d", m.Phase, m.DurationMS, expected)
		}
	}
}

func TestAggregatorComparesAcrossJobs(t *testing.T) {
	jobs, obs := testSourceDBs(t)
	defer jobs.Close()
	defer obs.Close()

	insert := func(jobID string, wall, mix int64) {
		t.Helper()
		report := kernobs.RunReport{RunID: "run-" + jobID, JobID: jobID, JobType: "script.generate", Status: kernobs.StatusSucceeded, WallTimeMs: wall, Operations: []kernobs.OperationReport{{Stage: "audio_compile", Component: "audio", Operation: "mix", DurationMs: mix}}}
		reportJSON, err := report.JSON()
		if err != nil {
			t.Fatal(err)
		}
		cp := struct {
			Result *scriptgeneration.GenerateResult `json:"result,omitempty"`
		}{Result: &scriptgeneration.GenerateResult{AudioMetrics: &scriptgeneration.AudioPipelineMetrics{MixMS: mix}}}
		payloadJSON, err := json.Marshal(cp)
		if err != nil {
			t.Fatal(err)
		}
		now := "2026-08-14T10:00:00Z"
		if _, err := obs.Exec(`INSERT INTO run_observability (run_id,job_id,job_type,attempt_id,status,created_at,started_at,report_json,workflow_payload_json,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			report.RunID, jobID, report.JobType, "attempt-"+jobID, report.Status, now, now, string(reportJSON), string(payloadJSON), now); err != nil {
			t.Fatal(err)
		}
	}
	insert("j1", 1000, 100)
	insert("j2", 2000, 300)

	src, err := NewSource(jobs, obs)
	if err != nil {
		t.Fatal(err)
	}
	agg := perf.NewAggregator(src, perf.DefaultPhaseResolver{})

	got, err := agg.Compare(context.Background(), []string{"j1", "j2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.JobIDs) != 2 || got.JobIDs[0] != "j1" || got.JobIDs[1] != "j2" {
		t.Fatalf("job ids = %v", got.JobIDs)
	}
	var mix perf.PhaseStats
	for _, p := range got.Phases {
		if p.Phase == perf.PhaseRustMix {
			mix = p
		}
	}
	if mix.MeasuredJobs != 2 || mix.MinMS != 100 || mix.MaxMS != 300 || mix.AvgMS != 200 {
		t.Fatalf("mix stats = %+v", mix)
	}
}
