package job

import "testing"

func TestAggregateStageProgressByStage(t *testing.T) {
	statuses := []StageLanguageStatus{
		{Stage: StageScript, Language: "en", Status: StageCompleted, JobID: "script-en"},
		{Stage: StageTranslation, Language: "it", Status: StageCompleted, JobID: "translation-it"},
		{Stage: StageTranslation, Language: "en", Status: StageRunning, JobID: "translation-en"},
		{Stage: StageVoiceover, Language: "it", Status: StageFailed, JobID: "voiceover-it", Error: "tts failed"},
		{Stage: StageUpload, Language: "it", Status: StageQueued, JobID: "upload-it"},
		{Stage: StagePersistence, Language: "it", Status: StageCompleted, JobID: "persist-it"},
	}

	got := AggregateStageProgressByStage(statuses)
	if got[string(StageTranslation)].Completed != 1 || got[string(StageTranslation)].Total != 2 {
		t.Fatalf("translation progress = %+v, want completed=1 total=2", got[string(StageTranslation)])
	}
	if got[string(StageVoiceover)].Completed != 0 || got[string(StageVoiceover)].Total != 1 {
		t.Fatalf("voiceover progress = %+v, want completed=0 total=1", got[string(StageVoiceover)])
	}
	if got[string(StagePersistence)].Completed != 1 || got[string(StagePersistence)].Total != 1 {
		t.Fatalf("persistence progress = %+v, want completed=1 total=1", got[string(StagePersistence)])
	}
}

func TestFlattenStageProgressUsesCanonicalStageOrder(t *testing.T) {
	progress := AggregateStageProgressByStage([]StageLanguageStatus{
		{Stage: StagePersistence, Language: "it", Status: StageCompleted},
		{Stage: StageUpload, Language: "it", Status: StageCompleted},
		{Stage: StageScript, Language: "en", Status: StageCompleted},
	})
	got := FlattenStageProgress(progress)
	if len(got) != 3 || got[0].Stage != StageScript || got[1].Stage != StageUpload || got[2].Stage != StagePersistence {
		t.Fatalf("flattened progress = %+v, want script then upload then persistence", got)
	}
}
