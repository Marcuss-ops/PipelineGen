package observability

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunClipTimeline_PreservesIdentityOrderingAndFailure(t *testing.T) {
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{
		RunID: "run-clip-1", JobID: "job-clip-1", AttemptID: "attempt-clip-1",
	})
	ctx := WithRun(context.Background(), run)

	RecordClipPhase(ctx, ClipPhaseSubmitted, base, base.Add(2*time.Millisecond), StageStatusCompleted, nil)
	RecordClipPhase(ctx, ClipPhaseClaimed, base.Add(2*time.Millisecond), base.Add(5*time.Millisecond), StageStatusCompleted, nil)
	RecordClipPhase(ctx, ClipPhasePrepare, base.Add(5*time.Millisecond), base.Add(15*time.Millisecond), StageStatusCompleted, nil)
	RecordClipPhase(ctx, ClipPhaseRenderSlot, base.Add(15*time.Millisecond), base.Add(19*time.Millisecond), StageStatusCompleted, nil)
	RecordClipPhase(ctx, ClipPhaseFFmpeg, base.Add(19*time.Millisecond), base.Add(39*time.Millisecond), StageStatusCompleted, nil)
	RecordClipPhase(ctx, ClipPhaseHashProbe, base.Add(39*time.Millisecond), base.Add(45*time.Millisecond), StageStatusCompleted, nil)
	RecordClipPhase(ctx, ClipPhaseUploadSlot, base.Add(45*time.Millisecond), base.Add(48*time.Millisecond), StageStatusCompleted, nil)
	RecordClipPhase(ctx, ClipPhaseDrive, base.Add(48*time.Millisecond), base.Add(70*time.Millisecond), StageStatusCompleted, nil)
	recordErr := errors.New("drive upload failed")
	RecordClipPhase(ctx, ClipPhaseFinalize, base.Add(70*time.Millisecond), base.Add(75*time.Millisecond), StageStatusFailed, recordErr)

	report := run.Report()
	if report.RunID != "run-clip-1" || report.JobID != "job-clip-1" || report.AttemptID != "attempt-clip-1" {
		t.Fatalf("identity = run=%q job=%q attempt=%q", report.RunID, report.JobID, report.AttemptID)
	}
	if report.ClipTimeline == nil || len(report.ClipTimeline.Entries) != 9 {
		t.Fatalf("timeline = %+v, want 9 entries", report.ClipTimeline)
	}
	want := []ClipTimelinePhase{
		ClipPhaseSubmitted, ClipPhaseClaimed, ClipPhasePrepare, ClipPhaseRenderSlot,
		ClipPhaseFFmpeg, ClipPhaseHashProbe, ClipPhaseUploadSlot, ClipPhaseDrive, ClipPhaseFinalize,
	}
	for i, entry := range report.ClipTimeline.Entries {
		if entry.Phase != want[i] {
			t.Errorf("entry[%d].phase = %q, want %q", i, entry.Phase, want[i])
		}
		if i > 0 && entry.StartedAt.Before(report.ClipTimeline.Entries[i-1].StartedAt) {
			t.Errorf("timeline is not ordered at index %d", i)
		}
		if entry.DurationMs <= 0 {
			t.Errorf("entry[%d].duration_ms = %d, want positive", i, entry.DurationMs)
		}
	}
	failed := report.ClipTimeline.Entries[len(report.ClipTimeline.Entries)-1]
	if failed.Status != StageStatusFailed || failed.ErrorCode == "" {
		t.Fatalf("failed entry = %+v", failed)
	}
	if report.ClipTimeline.Entries[3].QueueWaitMs != 4 || report.ClipTimeline.Entries[6].QueueWaitMs != 3 {
		t.Fatalf("slot waits = render=%d upload=%d", report.ClipTimeline.Entries[3].QueueWaitMs, report.ClipTimeline.Entries[6].QueueWaitMs)
	}
}

func TestRunClipTimeline_RejectsInvalidIntervalsAndFinishedRuns(t *testing.T) {
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{JobID: "job", AttemptID: "attempt"})
	run.RecordClipPhase(ClipPhasePrepare, time.Time{}, base, StageStatusCompleted, nil)
	run.RecordClipPhase(ClipPhasePrepare, base.Add(time.Second), base, StageStatusCompleted, nil)
	if got := run.ClipTimeline(); got != nil {
		t.Fatalf("invalid entries = %+v, want none", got)
	}
	run.Finish()
	run.RecordClipPhase(ClipPhaseFinalize, base, base.Add(time.Second), StageStatusCompleted, nil)
	if got := run.ClipTimeline(); got != nil {
		t.Fatalf("entry after finish = %+v, want none", got)
	}
}

func TestClipTimeline_JSONProjection(t *testing.T) {
	run := NewRunObserver(nil).StartRun(context.Background(), RunInfo{RunID: "run", JobID: "job", AttemptID: "attempt"})
	now := time.Now().UTC()
	run.RecordClipPhase(ClipPhaseDrive, now, now.Add(time.Millisecond), StageStatusCompleted, nil)
	body, err := run.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 || !containsBytes(body, []byte(`"clip_timeline"`)) || !containsBytes(body, []byte(`"phase":"drive"`)) {
		t.Fatalf("JSON = %s", body)
	}
}

func containsBytes(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
