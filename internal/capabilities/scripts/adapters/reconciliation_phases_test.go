package adapters

import (
	"context"
	"errors"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestReconciliationReportPreservesPartialSceneAndWarnings(t *testing.T) {
	input := ProcessInput{SpecScene: scriptpkg.SpecSceneOutput{Version: 7}}
	scenes := []scriptpkg.SpecScene{{ID: "scene-1"}}
	report := reconciliationReport(input, scenes, true, []string{"one link failed"})
	if !report.Changed || !report.SpecSceneChanged {
		t.Fatal("partial reconciliation must report changed")
	}
	if len(report.UpdatedSpecScene.Scenes) != 1 || report.UpdatedSpecScene.Scenes[0].ID != "scene-1" {
		t.Fatalf("report scenes = %+v", report.UpdatedSpecScene.Scenes)
	}
	if len(report.Warnings) != 1 {
		t.Fatalf("warnings = %v", report.Warnings)
	}
}

func TestApplyReconcilePlanNoopsWithoutCommitter(t *testing.T) {
	processor := &AssetLocationReconciliationProcessor{}
	plan := buildReconcilePlan(map[string]scriptpkg.AssetLocationChange{
		"asset-1": {AssetID: "asset-1", DriveLink: "https://drive/file"},
	})
	if err := processor.applyReconcilePlan(context.Background(), plan); err != nil {
		t.Fatalf("nil committer should be a no-op: %v", err)
	}
}

func TestApplyReconcilePlanPropagatesCommitError(t *testing.T) {
	committer := &recordingAssetLocationCommitter{err: errors.New("commit failed")}
	processor := NewDurableAssetLocationReconciliationProcessor(nil, committer)
	plan := buildReconcilePlan(map[string]scriptpkg.AssetLocationChange{
		"asset-1": {AssetID: "asset-1", DriveLink: "https://drive/file"},
	})
	if err := processor.applyReconcilePlan(context.Background(), plan); !errors.Is(err, committer.err) {
		t.Fatalf("apply error = %v, want %v", err, committer.err)
	}
}
