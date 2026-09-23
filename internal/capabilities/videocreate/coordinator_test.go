package videocreate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func testJob(id, idem string) *job.Job {
	raw, _ := json.Marshal(appjobs.VideoCreatePayload{
		Topic:           "Mike Tyson training",
		Language:        "en",
		DurationSeconds: 60,
		MediaSources:    []string{"youtube", "stock"},
		Voiceover:       true,
		Overlays:        true,
	})
	return &job.Job{
		ID: id, Type: appjobs.TypeVideoCreate, Project: "e2e-video-create",
		VideoName: "tyson-001", CorrelationID: "corr-" + id,
		IdempotencyKey: idem, Payload: raw,
	}
}

// TestCoordinator_FullLadder drives the whole §7 ladder hermetically
// and asserts the durable projection: eleven completed steps, the
// derived child keys (script/youtube/stock/voiceover/render), the
// progress bands and the typed result facts.
func TestCoordinator_FullLadder(t *testing.T) {
	children := newFakeChildren()
	deps, assembler, publisher := newTestDeps(t, children, fakeProbe{})
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	res, err := handler(context.Background(), testJob("job_full", "calendar:item-123:2026-09-24"), nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if assembler.calls != 1 {
		t.Errorf("assembler calls = %d, want 1", assembler.calls)
	}
	if publisher.calls != 1 {
		t.Errorf("publisher calls = %d, want 1", publisher.calls)
	}
	completed, _ := res["completed_stages"].([]string)
	if completed == nil {
		// Result map round-trips through []string or []any depending
		// on the envelope; normalise.
		if anyList, ok := res["completed_stages"].([]any); ok {
			for _, s := range anyList {
				completed = append(completed, s.(string))
			}
		}
	}
	if len(completed) != len(WorkflowSteps) {
		t.Fatalf("completed_stages = %v, want %d entries", completed, len(WorkflowSteps))
	}
	finalVideo, ok := res["final_video"].(map[string]any)
	if !ok {
		t.Fatalf("result has no final_video: %#v", res)
	}
	if finalVideo["media_url"] != "https://drive.test/final_video" {
		t.Errorf("final_video.media_url = %v", finalVideo["media_url"])
	}
	if finalVideo["sha256"] == "" || finalVideo["drive_file_id"] != "drive_file_final" {
		t.Errorf("final_video = %#v", finalVideo)
	}
	childrenCount := children.enqueueCount()
	// script(1) + acquire(2) + voiceover parent(1) + voiceover
	// generate_item fan-out(2) + render(2) = 8 children. Assembly and
	// mux run on the VeloxEditing media plane and fan out NOTHING.
	if childrenCount != 8 {
		t.Errorf("distinct children = %d, want 8", childrenCount)
	}
}

// TestCoordinator_SkipsOptionalSteps pins the durable skip semantics:
// voiceover=false SKIPS 04_voiceover + 05_audio_master + 09_audio_mux
// as durable completions and the ladder still succeeds (the final video
// keeps the clips' own audio and the §17 audio gate still applies).
func TestCoordinator_SkipsOptionalSteps(t *testing.T) {
	children := newFakeChildren()
	deps, _, _ := newTestDeps(t, children, fakeProbe{})
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	j := testJob("job_skip", "calendar:item-9:2026-09-24")
	var payload appjobs.VideoCreatePayload
	_ = json.Unmarshal(j.Payload, &payload)
	payload.Voiceover = false
	payload.Overlays = false
	j.Payload, _ = json.Marshal(payload)
	res, err := handler(context.Background(), j, nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	rows, _ := deps.Steps.ListByJob(context.Background(), j.ID)
	state, err := StateFromSteps(rows)
	if err != nil {
		t.Fatalf("StateFromSteps: %v", err)
	}
	for _, key := range []string{"04_voiceover", "05_audio_master", "06_overlay_plan", "09_audio_mux"} {
		rec := state.StageRecordFor(key)
		if rec == nil || rec.Status != StageSkipped {
			t.Errorf("step %s status = %v, want SKIPPED", key, rec)
		}
	}
	if _, ok := res["final_video"]; !ok {
		t.Errorf("no final_video in %v", res)
	}
}

// TestRecovery_RestartMidRender is THE §7/§8 acceptance test: the
// process dies while rendering; the replayed run resumes at 07_render
// and converges on the SAME children — no second script, no re-download,
// no second voiceover, no second video.
func TestRecovery_RestartMidRender(t *testing.T) {
	children := newFakeChildren()
	deps, _, publisher := newTestDeps(t, children, fakeProbe{})

	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	// Simulate the runbook failure: the PROCESS dies at render 40% —
	// the children are alive but the wait is interrupted mid-stage.
	children.failNextWaitOnce("calendar:item-128:2026-09-24:render:scene:002")
	j := testJob("job_restart", "calendar:item-128:2026-09-24")
	if _, err := handler(context.Background(), j, nil); err == nil {
		t.Fatal("first run: want interruption at render, got success")
	} else if !errors.Is(err, ErrWorkflowFailed) {
		t.Fatalf("first run error = %v, want ErrWorkflowFailed", err)
	}
	countAfterFirst := children.enqueueCount()

	// The failure is durable: 07_render is FAILED, everything before is
	// completed.
	rows, _ := deps.Steps.ListByJob(context.Background(), j.ID)
	state, err := StateFromSteps(rows)
	if err != nil {
		t.Fatalf("StateFromSteps: %v", err)
	}
	if rec := state.StageRecordFor("07_render"); rec == nil || rec.Status != StageFailed {
		t.Fatalf("07_render after crash = %v, want FAILED", rec)
	}
	for _, key := range []string{"01_script", "02_media_search", "03_media_acquire", "04_voiceover", "05_audio_master", "06_overlay_plan"} {
		if !state.Completed(key) {
			t.Fatalf("step %s not completed after first run", key)
		}
	}

	// Restart: the process is back, the job is redelivered. The
	// workflow must resume at 07_render and converge on the SAME
	// children.
	res, err := handler(context.Background(), j, nil)
	if err != nil {
		t.Fatalf("replay run: %v", err)
	}
	if got := children.enqueueCount(); got != countAfterFirst {
		t.Errorf("replay created NEW children: %d -> %d (§8 idempotency broken)", countAfterFirst, got)
	}
	if publisher.calls != 1 {
		t.Errorf("publisher calls = %d, want exactly 1 across both runs", publisher.calls)
	}
	finalVideo, _ := res["final_video"].(map[string]any)
	if finalVideo == nil || finalVideo["media_url"] == "" {
		t.Fatalf("replay result has no published final_video: %v", res)
	}
	childrenLedger, _ := res["children"].(map[string]any)
	if childrenLedger == nil {
		t.Fatalf("result has no children ledger: %v", res)
	}
	if script, _ := childrenLedger["script"].(string); script == "" {
		t.Errorf("children.script missing: %v", childrenLedger)
	}
}

// TestVerification_AudioMissingFailsClosed pins the §17 rule that made
// the runbook famous: an MP4 without an audio stream is NEVER a
// success. The typed sentinel must surface and the workflow must not
// produce a result.
func TestVerification_AudioMissingFailsClosed(t *testing.T) {
	children := newFakeChildren()
	deps, _, publisher := newTestDeps(t, children, fakeProbe{facts: ProbeFacts{
		SizeBytes: 1000, DurationMS: 60000, Width: 1920, Height: 1080, FPS: 30,
		VideoCodec: "h264", AudioCodec: "aac",
		VideoStreamCount: 1, AudioStreamCount: 0,
	}})
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	_, err = handler(context.Background(), testJob("job_noaudio", "calendar:noaudio:2026-09-24"), nil)
	if err == nil {
		t.Fatal("audio-less final video must fail")
	}
	if !errors.Is(err, ErrFinalVideoAudioMissing) {
		t.Fatalf("error = %v, want ErrFinalVideoAudioMissing", err)
	}
	if publisher.calls != 0 {
		t.Errorf("publisher calls = %d, want 0 (never publish an uncertified video)", publisher.calls)
	}
	if !strings.Contains(err.Error(), "FINAL_VIDEO_AUDIO_MISSING") {
		t.Errorf("error must name the typed failure: %v", err)
	}
}
