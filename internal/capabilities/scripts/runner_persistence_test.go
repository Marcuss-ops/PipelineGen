package scriptgeneration

import (
	"context"
	"testing"
	"time"
)

type recordingScriptPersistence struct {
	calls int
	input ScriptPersistenceInput
}

func (p *recordingScriptPersistence) Persist(_ context.Context, input ScriptPersistenceInput) (int64, error) {
	p.calls++
	p.input = input
	return 77, nil
}

func TestRunner_SaveToDBPersistsAndExposesScriptID(t *testing.T) {
	runner, repo, _, _, _, _, _ := newTestRunner()
	persistence := &recordingScriptPersistence{}
	runner.SetScriptPersistence(persistence)

	req := defaultTestRequest()
	req.Audio = "NONE"
	req.Languages = nil
	req.Docs = DocumentsConfig{}
	req.SaveToDB = true
	req.IdempotencyKey = "persist-script-id"
	runID := "run-persist-script-id"
	if err := repo.Create(context.Background(), &GenerationRun{
		ID: runID, Request: req, Status: RunStatusPending, CurrentStage: StageNormalizing,
	}); err != nil {
		t.Fatal(err)
	}

	runner.Execute(context.Background(), runID, req)
	final := awaitCompletion(t, repo, runID, 5*time.Second)
	if final.Status != RunStatusCompleted {
		t.Fatalf("run status=%s error=%s", final.Status, final.ErrorMessage)
	}
	if persistence.calls != 1 {
		t.Fatalf("persistence calls=%d, want 1", persistence.calls)
	}
	if persistence.input.Request.SaveToDB != true {
		t.Fatal("persistence input lost SaveToDB=true")
	}
	if final.Result == nil {
		t.Fatal("final result is nil")
	}
	if final.Result.ScriptID != 77 {
		t.Fatalf("script_id=%d, want 77", final.Result.ScriptID)
	}
}
