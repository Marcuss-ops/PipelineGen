package job

import "testing"

func TestAggregateStageProgressByStage(t *testing.T) {
	statuses := []StageLanguageStatus{
		{Stage: StageScript, Language: "en", Status: StageCompleted, JobID: "script-en"},
		{Stage: StageTranslation, Language: "it", Status: StageCompleted, JobID: "translation-it"},
		{Stage: StageTranslation, Language: "en", Status: StageRunning, JobID: "translation-en"},
		{Stage: StageVoiceover, Language: "it", Status: StageFailed, JobID: "voiceover-it", Error: "tts failed"},
		{Stage: StageUpload, Language: "it", Status: StageQueued, JobID: "upload-it"},
	}

	got := AggregateStageProgressByStage(statuses)
	if got[string(StageTranslation)].Completed != 1 || got[string(StageTranslation)].Total != 2 {
		t.Fatalf("translation progress = %+v, want completed=1 total=2", got[string(StageTranslation)])
	}
	if got[string(StageVoiceover)].Completed != 0 || got[string(StageVoiceover)].Total != 1 {
		t.Fatalf("voiceover progress = %+v, want completed=0 total=1", got[string(StageVoiceover)])
	}
	if got[string(StageScript)].Languages[0].Language != "en" {
		t.Fatalf("script language = %+v, want en", got[string(StageScript)].Languages)
	}
}

func TestFlattenStageProgressUsesCanonicalStageOrder(t *testing.T) {
	progress := AggregateStageProgressByStage([]StageLanguageStatus{
		{Stage: StageUpload, Language: "it", Status: StageCompleted},
		{Stage: StageScript, Language: "en", Status: StageCompleted},
	})
	got := FlattenStageProgress(progress)
	if len(got) != 2 || got[0].Stage != StageScript || got[1].Stage != StageUpload {
		t.Fatalf("flattened progress = %+v, want script then upload", got)
	}
}
