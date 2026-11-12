package scriptgeneration

import (
	"context"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TestFinalJobKaufmannCheckpointRepair is the third mandatory regression: an
// incoherent Kaufmann-style checkpoint (plan and render disagreeing) is repaired
// without losing anything that is still valid.
//
// It also pins WHY a database edit is not enough: the repair names the stage
// that must be replayed, because a run resumed past StageCompilingAudio skips
// the overlay phase entirely.
func TestFinalJobKaufmannCheckpointRepair(t *testing.T) {
	plan := overlayPlanBindingFixture()
	result := &GenerateResult{
		OverlayPlan: plan,
		FinalAudio:  &FinalAudioReference{DurationMS: 5000},
		Scenes:      []Scene{{ID: "scene-1", Index: 0}},
		Segments:    []scriptpkg.VidRushSegmentResult{{SceneID: "scene-1"}},
		LocalizedOverlayPlans: map[Language]*capmodel.OverlayPlan{
			"en": {SchemaVersion: capmodel.SchemaVersionPlan, PlanID: "plan-kaufmann-en", Items: []capmodel.OverlayItem{
				{ID: "phrase-en-1", SceneID: "scene-1", Kind: "text_phrase", StartUS: 0, DurationUS: 500_000},
			}},
		},
	}
	// The failed checkpoint: a render of an OLDER plan, plus an ID the current
	// plan does not contain at all.
	result.OverlayRender = &RenderReference{
		JobID:      "job-stale",
		PlanDigest: strings.Repeat("c", 64),
		Manifest: &capmodel.OverlayManifest{Entries: []capmodel.OverlayManifestEntry{
			{ItemID: "ghost-item-from-old-plan", InUS: 0, OutUS: 1_000_000},
			{ItemID: "date__2007__date_number__01", InUS: 1_000_000, OutUS: 2_000_000},
		}},
	}
	result.LocalizedOverlayRenders = map[Language]RenderReference{
		"en": {JobID: "job-stale-en", PlanDigest: strings.Repeat("d", 64)},
	}

	report := RepairFinalJobOverlayCheckpoint(result)
	if !report.Repaired {
		t.Fatal("an incompatible overlay render must be reported as repaired")
	}
	if report.ResumeStage != StageCompilingAudio {
		t.Fatalf("resume stage = %q, want %q: clearing the render without replaying the stage skips the overlay phase",
			report.ResumeStage, StageCompilingAudio)
	}
	if len(report.Reasons) == 0 {
		t.Fatal("the repair must record why the reference was rejected")
	}
	if result.OverlayRender != nil {
		t.Fatal("the incompatible render must no longer be the checkpoint's overlay render")
	}
	if len(result.OverlayRenderHistory) != 2 || result.OverlayRenderHistory[0].JobID != "job-stale" || result.OverlayRenderHistory[1].JobID != "job-stale-en" {
		t.Fatalf("history = %#v, want both the archived source and translation renders", result.OverlayRenderHistory)
	}
	if report.Archived == nil || report.Archived.JobID != "job-stale" {
		t.Fatal("the report must name the archived reference")
	}
	if len(result.LocalizedOverlayRenders) != 0 {
		t.Fatalf("localized renders = %#v, want the incompatible translation render cleared", result.LocalizedOverlayRenders)
	}
	if len(report.DroppedLocalized) != 1 || report.DroppedLocalized[0] != "en" {
		t.Fatalf("dropped localized = %#v, want [en]", report.DroppedLocalized)
	}
	// Everything still valid survives untouched.
	if result.FinalAudio == nil || result.FinalAudio.DurationMS != 5000 {
		t.Fatal("the certified final audio must survive the repair")
	}
	if len(result.Scenes) != 1 || len(result.Segments) != 1 {
		t.Fatal("scenes and segments must survive the repair")
	}

	// Idempotent: repairing an already coherent result must not archive twice.
	again := RepairFinalJobOverlayCheckpoint(result)
	if again.Repaired || len(result.OverlayRenderHistory) != 2 {
		t.Fatal("a second repair must be a no-op")
	}
}

// TestFinalJobRepairArchivesLocalizedRenderWithoutSourceRender pins the
// per-language half of the repair. A valid source render must not hide an
// incompatible translation, and a missing source render must not skip the
// localized check at all; the archived translation stays addressable.
func TestFinalJobRepairArchivesLocalizedRenderWithoutSourceRender(t *testing.T) {
	plan := overlayPlanBindingFixture()
	localizedPlan := cloneOverlayPlan(plan)
	localizedPlan.Language = "en"
	localizedPlan.PlanID = "plan-kaufmann-en"
	result := &GenerateResult{
		OverlayPlan: plan,
		LocalizedOverlayPlans: map[Language]*capmodel.OverlayPlan{
			"en": localizedPlan,
		},
		LocalizedOverlayRenders: map[Language]RenderReference{
			"en": {JobID: "job-stale-en", PlanDigest: strings.Repeat("d", 64)},
		},
	}

	report := RepairFinalJobOverlayCheckpoint(result)
	if !report.Repaired {
		t.Fatal("an incompatible localized render must be repaired even when the source render is absent")
	}
	if report.Archived != nil {
		t.Fatalf("archived source = %#v, want nil (there was no source render)", report.Archived)
	}
	if len(result.LocalizedOverlayRenders) != 0 {
		t.Fatalf("localized renders = %#v, want the incompatible one cleared", result.LocalizedOverlayRenders)
	}
	if len(result.OverlayRenderHistory) != 1 || result.OverlayRenderHistory[0].JobID != "job-stale-en" {
		t.Fatalf("history = %#v, want the archived translation render", result.OverlayRenderHistory)
	}
	if len(report.DroppedLocalized) != 1 || report.DroppedLocalized[0] != "en" {
		t.Fatalf("dropped localized = %#v, want [en]", report.DroppedLocalized)
	}
}

// TestFinalJobRepairPersistsResumeStage proves the repair survives a crash
// before the replayed audio phase checkpoints again: the durable stage moves
// with the cleared reference, so the next resume still re-enters the overlay
// render instead of skipping it forever.
func TestFinalJobRepairPersistsResumeStage(t *testing.T) {
	const runID = "run-repair-stage"
	plan := overlayPlanBindingFixture()
	result := &GenerateResult{
		OverlayPlan:   plan,
		OverlayRender: manifestRefForPlan(plan, strings.Repeat("b", 64)),
	}
	runner, repo := newFinalJobRunner(nil, runID, result)
	if err := repo.FailRun(context.Background(), FailRunInput{
		RunID: runID, FailedStage: StagePublishingDocuments, ErrorCode: "FINAL_JOB_OVERLAY_MISMATCH",
	}); err != nil {
		t.Fatalf("FailRun: %v", err)
	}

	stage, repaired := runner.repairOverlayCheckpointOnResume(context.Background(), runID, StagePublishingDocuments, result)
	if !repaired || stage != StageCompilingAudio {
		t.Fatalf("repair = %v stage = %q, want a repair that replays %q", repaired, stage, StageCompilingAudio)
	}
	run, err := repo.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.Status != RunStatusRunning || run.CurrentStage != StageCompilingAudio {
		t.Fatalf("durable run = status %q stage %q, want RUNNING/%q", run.Status, run.CurrentStage, StageCompilingAudio)
	}
	if run.Result == nil || run.Result.OverlayRender != nil {
		t.Fatal("the durable checkpoint must no longer carry the incompatible render")
	}
}
