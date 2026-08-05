package job

import "testing"

func TestMergeStageProgressUpsertsChildObservation(t *testing.T) {
	progress := AggregateStageProgressByStage([]StageLanguageStatus{{
		Stage: StageTranslation, Language: "it", JobID: "child-it", Status: StageRunning,
	}})
	updated := AggregateStageProgressByStage([]StageLanguageStatus{{
		Stage: StageTranslation, Language: "it", JobID: "child-it", Status: StageCompleted,
	}})

	got := MergeStageProgress(progress, updated)[string(StageTranslation)]
	if got.Total != 1 || got.Completed != 1 {
		t.Fatalf("merged translation progress = %+v, want total=1 completed=1", got)
	}
	if len(got.Languages) != 1 || got.Languages[0].Status != StageCompleted {
		t.Fatalf("merged observations = %+v, want one completed child observation", got.Languages)
	}
}
