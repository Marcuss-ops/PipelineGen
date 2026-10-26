package scriptgeneration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// TestStageHeartbeatLogsNameTheCurrentOperation: the batch operator must be
// able to tell a slow stage from a stuck one from the run log alone — every
// heartbeat names the operation the stage is inside and how long it has been
// there.
func TestStageHeartbeatLogsNameTheCurrentOperation(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	hb := newStageHeartbeat(zap.New(core), "run-hb", StageGeneratingSceneText, 10*time.Millisecond)
	defer hb.close()

	hb.setOp("generate_scene_text")
	deadline := time.Now().Add(2 * time.Second)
	for logs.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if logs.Len() == 0 {
		t.Fatal("the heartbeat must emit periodic progress logs")
	}
	entry := logs.All()[0]
	if entry.Message != "stage heartbeat: still working" {
		t.Fatalf("heartbeat message = %q", entry.Message)
	}
	fields := entry.ContextMap()
	if fields["stage"] != string(StageGeneratingSceneText) || fields["operation"] != "generate_scene_text" {
		t.Fatalf("heartbeat fields = %v, want the stage and its current operation named", fields)
	}
	if fields["run_id"] != "run-hb" {
		t.Fatalf("heartbeat run_id = %v", fields["run_id"])
	}
}

// TestStageHeartbeatAnnotateNamesTheBlockedOperation is the timeout/error path
// of the stage: whatever interrupts it, the surfaced error identifies the
// operation that was blocked, not only the stage name.
func TestStageHeartbeatAnnotateNamesTheBlockedOperation(t *testing.T) {
	hb := newStageHeartbeat(zap.NewNop(), "run-hb", StageGeneratingSceneText, time.Hour)
	defer hb.close()

	hb.setOp("scene_ready_wait")
	err := hb.annotate(context.DeadlineExceeded)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("annotate error = %v, want the cause preserved", err)
	}
	if !strings.Contains(err.Error(), `blocked in operation "scene_ready_wait"`) {
		t.Fatalf("error = %v, want the blocked operation named", err)
	}

	plain := newStageHeartbeat(zap.NewNop(), "run-hb", StageGeneratingSceneText, time.Hour)
	defer plain.close()
	err = plain.annotate(errors.New("boom"))
	if err == nil || !strings.Contains(err.Error(), "stage GENERATING_SCENE_TEXT failed") {
		t.Fatalf("error = %v, want the stage named before any operation is set", err)
	}
	if got := hb.annotate(nil); got != nil {
		t.Fatalf("annotate(nil) = %v, want nil", got)
	}
}

// TestStageHeartbeatCloseIsIdempotent: the phase defers close(); a second
// close (or a close after the loop exited) must not panic.
func TestStageHeartbeatCloseIsIdempotent(t *testing.T) {
	hb := newStageHeartbeat(zap.NewNop(), "run-hb", StageGeneratingSceneText, time.Millisecond)
	hb.close()
	hb.close()
}
