package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

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
		RunID: "run-dedup",
		JobID: "job-dedup",
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
