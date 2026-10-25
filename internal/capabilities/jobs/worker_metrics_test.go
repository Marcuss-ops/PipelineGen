package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobregistry"
	kernjob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// TestPayloadHash_EmptyAndWhitespaceMapToEmptyObject pins the SSOT edge case:
// worker_metrics.payloadHash must map an empty or whitespace-only payload to
// the canonical "{}" object before hashing, exactly like the canonical
// jobregistry.hashPayload. Before the guard was aligned, payloadHash("") and
// hashPayload("") produced different hashes for the same logical empty payload.
func TestPayloadHash_EmptyAndWhitespaceMapToEmptyObject(t *testing.T) {
	sum := sha256.Sum256([]byte("{}"))
	emptyObjectHash := hex.EncodeToString(sum[:])

	for _, input := range []string{"", "   ", "\n\t", "{}"} {
		if got := payloadHash(input); got != emptyObjectHash {
			t.Fatalf("payloadHash(%q) = %s, want canonical empty-object hash %s", input, got, emptyObjectHash)
		}
	}
}

// TestPayloadHash_CanonicalizesKeys guards the pre-existing canonicalization:
// equivalent JSON objects must hash identically regardless of key order.
func TestPayloadHash_CanonicalizesKeys(t *testing.T) {
	a := payloadHash(`{"video_id":"v1","n":1}`)
	b := payloadHash(`{"n":1,"video_id":"v1"}`)
	if a != b {
		t.Fatalf("payloadHash must canonicalize key order: got %s vs %s", a, b)
	}
	if a == "" || len(a) != 64 {
		t.Fatalf("payloadHash returned unexpected value %q", a)
	}
}

// TestJobRegistryRecorder_DedupsRunLevelStageSteps pins the single-owner
// contract for script-generation jobs: a run-level business phase is recorded
// ONCE, as the runner's outer execution step (NORMALIZE/SCRIPT/.../AUDIO_COMPILE/
// DOCUMENT). The matching RunReport stage must NOT be written a second time as a
// `stage` job_steps row, because that duplicate pollutes every step metric and
// audit. Nested technical stages (tts, overlay_render, ...) have no execution
// step and must still be recorded, and a non-script job keeps every stage row.
func TestJobRegistryRecorder_DedupsRunLevelStageSteps(t *testing.T) {
	report := &kernobs.RunReport{
		RunID:  "run-dedup",
		JobID:  "job-dedup",
		Status: kernobs.StatusSucceeded,
		Stages: []kernobs.StageReport{
			{Name: "normalize", Status: kernobs.StageStatusCompleted, DurationMs: 100},
			{Name: "voiceover", Status: kernobs.StageStatusCompleted, DurationMs: 5000},
			{Name: "tts", Status: kernobs.StageStatusCompleted, DurationMs: 6000},
			{Name: "audio_compile", Status: kernobs.StageStatusCompleted, DurationMs: 7000},
			{Name: "overlay_render", Status: kernobs.StageStatusCompleted, DurationMs: 8000},
			{Name: "document", Status: kernobs.StageStatusCompleted, DurationMs: 9000},
		},
	}

	scriptFake := &jobRegistryRecorderFake{}
	scriptRecorder := NewJobRegistryRecorder(scriptFake, nil)
	scriptJob := &kernjob.Job{ID: "job-dedup", Type: kernjob.TypeScriptGenerate, Status: kernjob.StatusRunning, Revision: 1}
	scriptRecorder.Finish(context.Background(), scriptJob, "", "worker-1", "attempt-1", "SUCCEEDED", nil, nil, report)

	gotStages := map[string]bool{}
	for _, step := range scriptFake.steps {
		if step.StepType == "stage" {
			gotStages[step.StepName] = true
		}
	}
	for _, want := range []string{"tts", "overlay_render"} {
		if !gotStages[want] {
			t.Errorf("nested stage %q must still be recorded; stages=%v", want, gotStages)
		}
	}
	for _, dropped := range []string{"normalize", "voiceover", "audio_compile", "document"} {
		if gotStages[dropped] {
			t.Errorf("run-level stage %q must be deduped against its execution step; stages=%v", dropped, gotStages)
		}
	}

	// A non-script job keeps every stage row: the run-level names are only
	// owned by the scripts runner for script-generation jobs.
	otherFake := &jobRegistryRecorderFake{}
	otherRecorder := NewJobRegistryRecorder(otherFake, nil)
	otherJob := &kernjob.Job{ID: "job-other", Type: "clip.render", Status: kernjob.StatusRunning, Revision: 1}
	otherRecorder.Finish(context.Background(), otherJob, "", "worker-1", "attempt-1", "SUCCEEDED", nil, nil, report)

	otherStages := map[string]bool{}
	for _, step := range otherFake.steps {
		if step.StepType == "stage" {
			otherStages[step.StepName] = true
		}
	}
	for _, want := range []string{"normalize", "voiceover", "audio_compile", "document"} {
		if !otherStages[want] {
			t.Errorf("non-script job must keep stage %q; stages=%v", want, otherStages)
		}
	}
}

// TestJobRegistryRecorder_DeferredAttemptIsNotAFailedStep pins the deferral
// classification: a handler attempt that ends in a WAIT (kernel/job.ErrDeferred)
// must project its worker.execution step as DEFERRED, never FAILED. The live
// ledger showed why this matters: 5.730 of 6.671 "failed" worker.execution
// rows were deferrals (re-polls of long runs), inflating every failure metric.
// The classification is by ERROR: a real failure whose job retry is scheduled
// by finalizeJob still ends in RETRY_WAIT but must stay FAILED.
func TestJobRegistryRecorder_DeferredAttemptIsNotAFailedStep(t *testing.T) {
	deferredErr := kernjob.DeferredAfter(30*time.Second, "durable run run-x failed at COMPILING_AUDIO and retries at later (run attempt 1/3): boom")

	deferredFake := &jobRegistryRecorderFake{}
	deferredRecorder := NewJobRegistryRecorder(deferredFake, nil)
	deferredJob := &kernjob.Job{ID: "job-deferred", Type: "script.generate", Status: kernjob.StatusRunning, Revision: 1}
	deferredRecorder.Finish(context.Background(), deferredJob, "step-deferred", "worker-1", "attempt-1", "RETRY_WAIT", nil, deferredErr, nil)

	if len(deferredFake.steps) != 1 {
		t.Fatalf("expected 1 step row, got %d", len(deferredFake.steps))
	}
	if got := deferredFake.steps[0].Status; got != statusDeferred {
		t.Fatalf("deferred attempt step status = %q, want %q", got, statusDeferred)
	}
	if !strings.Contains(deferredFake.steps[0].ErrorMessage, "job deferred") {
		t.Fatalf("deferred attempt must keep its reason in error_message, got %q", deferredFake.steps[0].ErrorMessage)
	}

	realFake := &jobRegistryRecorderFake{}
	realRecorder := NewJobRegistryRecorder(realFake, nil)
	realJob := &kernjob.Job{ID: "job-real-retry", Type: "script.generate", Status: kernjob.StatusRunning, Revision: 1}
	realRecorder.Finish(context.Background(), realJob, "step-real", "worker-1", "attempt-1", "RETRY_WAIT", nil, errors.New("vidrush semantic certification failed: CERTIFIED=false"), nil)

	if len(realFake.steps) != 1 {
		t.Fatalf("expected 1 step row, got %d", len(realFake.steps))
	}
	if got := realFake.steps[0].Status; got != "FAILED" {
		t.Fatalf("real retryable failure step status = %q, want FAILED (a retry scheduled by finalizeJob is still a failed attempt)", got)
	}
}

// TestJobRegistryRecorder_ProjectsStageErrorMessage pins the attribution
// contract for nested technical stages (scene_analysis, post_writer_finalize,
// overlay_render): the failed stage's cause must land in the job_steps row's
// error_message next to the error_code, so analytics can answer "why" without
// parsing run report JSON.
func TestJobRegistryRecorder_ProjectsStageErrorMessage(t *testing.T) {
	report := &kernobs.RunReport{
		RunID:  "run-stage-msg",
		JobID:  "job-stage-msg",
		Status: kernobs.StatusFailed,
		Stages: []kernobs.StageReport{
			{Name: "scene_analysis", Status: kernobs.StageStatusFailed, DurationMs: 19000, ErrorCode: "error", ErrorMessage: `vidrush scene "sao-paulo" (index 0) enrichment: vidrush provider not registered`},
		},
	}
	fake := &jobRegistryRecorderFake{}
	recorder := NewJobRegistryRecorder(fake, nil)
	j := &kernjob.Job{ID: "job-stage-msg", Type: "script.generate", Status: kernjob.StatusRunning, Revision: 1}
	recorder.Finish(context.Background(), j, "step-stage-msg", "worker-1", "attempt-1", "FAILED", nil, errors.New("boom"), report)

	stageRow := capregistryStepByName(fake.steps, "scene_analysis")
	if stageRow == nil {
		t.Fatalf("scene_analysis stage row missing; steps=%+v", fake.steps)
	}
	if !strings.Contains(stageRow.ErrorMessage, "sao-paulo") {
		t.Fatalf("stage error_message = %q, want the verbatim cause", stageRow.ErrorMessage)
	}
}

// capregistryStepByName returns the first step row with the given name.
func capregistryStepByName(steps []capregistry.Step, name string) *capregistry.Step {
	for i := range steps {
		if steps[i].StepName == name {
			return &steps[i]
		}
	}
	return nil
}
