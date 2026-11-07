package stockpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
)

func TestOrchestratorStageSnapshots_MapsLatestRowsInCanonicalOrder(t *testing.T) {
	store := steps.NewInMemoryStore()
	ctx := context.Background()
	jobID := "stage-snapshot-job"
	rows := []struct {
		name   string
		status steps.StepStatus
		errMsg string
	}{
		{StepKeyStockPlan, steps.StatusCompleted, ""},
		{StepKeyStockStageSources, steps.StatusCompleted, ""},
		{StepKeyStockExtractClips, steps.StatusFailed, "source failed"},
		{StepKeyStockPublish, steps.StatusRunning, ""},
		{StepKeyStockFinalize, steps.StatusCompleted, ""},
	}
	for _, item := range rows {
		key := steps.StepKey{JobID: jobID, StepKey: item.name, InputFingerprint: item.name + "-fingerprint"}
		if err := store.MarkStarted(ctx, key); err != nil {
			t.Fatalf("MarkStarted(%s): %v", item.name, err)
		}
		if item.status == steps.StatusRunning {
			if err := store.MarkStarted(ctx, key); err != nil {
				t.Fatalf("MarkStarted running(%s): %v", item.name, err)
			}
		}
		switch item.status {
		case steps.StatusCompleted:
			if err := store.MarkCompleted(ctx, key, json.RawMessage(`{"ok":true}`), nil); err != nil {
				t.Fatalf("MarkCompleted(%s): %v", item.name, err)
			}
		case steps.StatusFailed:
			if err := store.MarkFailed(ctx, key, item.errMsg); err != nil {
				t.Fatalf("MarkFailed(%s): %v", item.name, err)
			}
		}
	}

	o := &Orchestrator{cfg: OrchestratorConfig{JobId: jobID}, stepStore: store}
	stages, err := o.stageSnapshots(ctx, &RunInput{})
	if err != nil {
		t.Fatalf("stageSnapshots: %v", err)
	}
	if len(stages) != 5 {
		t.Fatalf("stage count = %d, want 5", len(stages))
	}
	wantNames := []string{
		StepKeyStockPlan, StepKeyStockStageSources, StepKeyStockExtractClips,
		StepKeyStockPublish, StepKeyStockFinalize,
	}
	for i, want := range wantNames {
		if stages[i].Name != want {
			t.Fatalf("stage[%d].Name = %q, want %q", i, stages[i].Name, want)
		}
	}
	if stages[0].Status != string(steps.StatusCompleted) || stages[0].Attempt != 1 {
		t.Fatalf("plan snapshot = %+v", stages[0])
	}
	if stages[2].Status != string(steps.StatusFailed) || stages[2].LastError != "source failed" {
		t.Fatalf("extract snapshot = %+v", stages[2])
	}
	if stages[3].Status != string(steps.StatusPending) || stages[3].Attempt != 2 {
		t.Fatalf("publish retry snapshot = %+v", stages[3])
	}
}

func TestStageSnapshot_JSONOmitsZeroTimestampsAndPreservesOrder(t *testing.T) {
	stages := []StageSnapshot{
		{Name: StepKeyStockPlan, Status: string(steps.StatusCompleted), Attempt: 1, Applicable: true},
	}
	raw, err := json.Marshal(map[string]any{"stages": stages})
	if err != nil {
		t.Fatalf("marshal stages: %v", err)
	}
	var envelope struct {
		Stages []map[string]any `json:"stages"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal stages: %v", err)
	}
	if len(envelope.Stages) != 1 || envelope.Stages[0]["name"] != StepKeyStockPlan {
		t.Fatalf("serialized stages = %s", raw)
	}
}

func TestOrchestratorStageSnapshots_FailsClosedWhenStoreMissing(t *testing.T) {
	o := &Orchestrator{cfg: OrchestratorConfig{JobId: "stage-snapshot-missing-store"}}
	_, err := o.stageSnapshots(context.Background(), &RunInput{})
	if !errors.Is(err, steps.ErrStoreNotWired) {
		t.Fatalf("err = %v, want steps.ErrStoreNotWired", err)
	}
}
