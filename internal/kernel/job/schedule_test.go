// Package job — schedule_test.go: pins the two kernel facts that deferred
// scheduling and per-stage status depend on.
//
//  1. StatusScheduled is a VALID broker status that is neither terminal nor
//     active. A regression here would either let a scheduled row be treated as
//     finished (dropping it) or as worker-owned (letting the reaper requeue a
//     job nobody was ever supposed to run yet).
//
//  2. JobStageStatus.Validate rejects anything outside the canonical stage
//     vocabulary declared in stage_progress.go. The durable projection must
//     not silently accept an invented stage name — godlike/07 no-fake-
//     availability: an advertised stage needs a producer, and the vocabulary
//     in stage_progress.go is the list of stages that have one.
package job

import (
	"testing"
	"time"
)

func TestStatusScheduledIsValidNonTerminalNonActive(t *testing.T) {
	if !StatusScheduled.Valid() {
		t.Fatal("StatusScheduled.Valid() = false; want true (a scheduled job is a real broker status)")
	}
	if StatusScheduled.IsTerminal() {
		t.Fatal("StatusScheduled.IsTerminal() = true; want false (it is waiting for its start time)")
	}
	if StatusScheduled.IsActive() {
		t.Fatal("StatusScheduled.IsActive() = true; want false (no worker owns the row before promotion)")
	}
}

func TestJobStageStatusValidate(t *testing.T) {
	base := func() JobStageStatus {
		return JobStageStatus{
			JobID:     "job-1",
			Stage:     StageOverlay,
			Status:    StageRunning,
			Progress:  40,
			UpdatedAt: time.Now().UTC(),
		}
	}

	if err := base().Validate(); err != nil {
		t.Fatalf("canonical projection must validate: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*JobStageStatus)
	}{
		{"missing job_id", func(s *JobStageStatus) { s.JobID = "  " }},
		{"non-canonical stage", func(s *JobStageStatus) { s.Stage = StageName("final_video_created") }},
		{"empty stage", func(s *JobStageStatus) { s.Stage = "" }},
		{"invalid status", func(s *JobStageStatus) { s.Status = StageStatus("in_progress") }},
		{"empty status", func(s *JobStageStatus) { s.Status = "" }},
		{"progress above 100", func(s *JobStageStatus) { s.Progress = 101 }},
		{"progress below 0", func(s *JobStageStatus) { s.Progress = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := base()
			tc.mutate(&rec)
			if err := rec.Validate(); err == nil {
				t.Fatalf("Validate() = nil for %s; want a typed rejection", tc.name)
			}
		})
	}
}

// TestJobStageStatusAcceptsEveryCanonicalStage is the vocabulary link between
// the stage contract (stage_progress.go) and the durable projection: every
// canonical stage MUST be storable, and an invented stage MUST NOT be. This is
// what keeps the per-stage sub-status surface honest — a stage that no
// producer emits cannot be reported through the API.
func TestJobStageStatusAcceptsEveryCanonicalStage(t *testing.T) {
	stages := CanonicalStageOrder()
	if len(stages) == 0 {
		t.Fatal("CanonicalStageOrder() is empty")
	}
	for _, stage := range stages {
		rec := JobStageStatus{JobID: "job-1", Stage: stage, Status: StageCompleted, Progress: 100}
		if err := rec.Validate(); err != nil {
			t.Fatalf("canonical stage %q must be storable: %v", stage, err)
		}
	}

	// Every stage status is accepted; nothing else is.
	for _, status := range []StageStatus{StageQueued, StageRunning, StageCompleted, StageFailed, StageSkipped} {
		rec := JobStageStatus{JobID: "job-1", Stage: StageScript, Status: status}
		if err := rec.Validate(); err != nil {
			t.Fatalf("canonical stage status %q must be storable: %v", status, err)
		}
	}
}

func TestStageStatusValid(t *testing.T) {
	for _, status := range []StageStatus{StageQueued, StageRunning, StageCompleted, StageFailed, StageSkipped} {
		if !status.Valid() {
			t.Fatalf("StageStatus(%q).Valid() = false; want true", status)
		}
	}
	for _, status := range []StageStatus{"", "done", "COMPLETED", "pending"} {
		if status.Valid() {
			t.Fatalf("StageStatus(%q).Valid() = true; want false", status)
		}
	}
}
