package usecase

import (
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestProgressTrackerTrackStageAggregatesByLanguage(t *testing.T) {
	var events []map[string]any
	tracker := NewProgressTracker(nil, "item-1")
	tracker.SetEventFn(func(_ string, _ string, data map[string]any) {
		events = append(events, data)
	})

	tracker.TrackStage(string(job.StageTranslation), "it", string(job.StageRunning), "translation-it", "")
	tracker.TrackStage(string(job.StageTranslation), "en", string(job.StageCompleted), "translation-en", "")
	tracker.TrackStage(string(job.StageTranslation), "it", string(job.StageCompleted), "translation-it", "")

	got := tracker.StageProgress()[string(job.StageTranslation)]
	if got.Total != 2 || got.Completed != 2 {
		t.Fatalf("translation progress = %+v, want completed=2 total=2", got)
	}
	if len(got.Languages) != 2 {
		t.Fatalf("translation language observations = %+v, want 2", got.Languages)
	}
	if len(events) != 3 {
		t.Fatalf("stage events = %d, want 3", len(events))
	}
	last, ok := events[len(events)-1]["stage_progress"].(map[string]job.StageProgress)
	if !ok || last[string(job.StageTranslation)].Completed != 2 {
		t.Fatalf("last event aggregate = %#v, want completed=2", events[len(events)-1]["stage_progress"])
	}
}

func TestProgressTrackerPostprocessUsesDynamicPercent(t *testing.T) {
	var percents []int
	tracker := NewProgressTracker(func(percent int, _ string) { percents = append(percents, percent) }, "item-1")
	tracker.PhasePostprocessProgress(0, 4, "translation")
	tracker.PhasePostprocessProgress(1, 4, "voiceover")
	tracker.PhasePostprocessProgress(3, 4, "persistence")
	if len(percents) != 3 || percents[0] == percents[1] || percents[1] == percents[2] {
		t.Fatalf("postprocess percents = %v, want distinct phase progress", percents)
	}
}
