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

// TestRunner_SaveToDBAndDocsExposeDurableReferences protects the two
// operator-visible outputs of POST /api/script/generate. A successful run
// with save_to_db=true must expose the persisted script ID, and docs.enabled
// must produce a non-empty Google Docs reference; a run that silently drops
// either output is not accepted as completed.
func TestRunner_SaveToDBAndDocsExposeDurableReferences(t *testing.T) {
	runner, repo, _, _, _, docPub, _ := newTestRunner()
	persistence := &recordingScriptPersistence{}
	runner.SetScriptPersistence(persistence)

	req := defaultTestRequest()
	req.Audio = "NONE"
	req.Languages = []Language{"en"}
	req.Docs = DocumentsConfig{Enabled: true, Languages: []Language{"en"}}
	req.SaveToDB = true
	req.IdempotencyKey = "persist-script-and-docs"
	runID := "run-persist-script-and-docs"
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
	if persistence.calls != 1 || final.Result == nil || final.Result.ScriptID <= 0 {
		t.Fatalf("save_to_db output was not durable: calls=%d result=%+v", persistence.calls, final.Result)
	}
	if got, ok := final.Result.Documents[Language("en")]; !ok || got.ID == "" || got.Link == "" {
		t.Fatalf("docs.enabled output was not durable: documents=%+v", final.Result.Documents)
	}
	if len(docPub.records) != 1 {
		t.Fatalf("document publisher calls=%d, want 1", len(docPub.records))
	}
}
