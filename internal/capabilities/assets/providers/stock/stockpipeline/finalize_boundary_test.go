package stockpipeline

import (
	"context"
	"os/exec"
	"testing"

	stockfinalize "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/finalize"
	capfinalization "github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

func TestFinalizeBoundaryDoesNotImportParent(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./finalize")
	cmd.Dir = "."
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list finalize deps: %v", err)
	}
	for _, dependency := range splitBoundaryLines(string(output)) {
		if dependency == "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline" {
			t.Fatal("finalize package imports parent stockpipeline package")
		}
	}
}

func splitBoundaryLines(value string) []string {
	lines := []string{}
	start := 0
	for i := 0; i < len(value); i++ {
		if value[i] == '\n' {
			lines = append(lines, value[start:i])
			start = i + 1
		}
	}
	if start < len(value) {
		lines = append(lines, value[start:])
	}
	return lines
}

func TestFinalizeAdapterCopiesArtifactProjection(t *testing.T) {
	legacy := []ChunkState{{Index: 2, ArtifactID: "a-2", SourceURL: "source", SHA256: "hash", Tags: []string{"tag"}}}
	neutral := fromLegacyChunks(legacy)
	if len(neutral) != 1 || neutral[0].ArtifactID != "a-2" || neutral[0].SourceURL != "source" || neutral[0].SHA256 != "hash" {
		t.Fatalf("neutral artifact = %#v", neutral)
	}
	neutral[0].Tags[0] = "changed"
	if legacy[0].Tags[0] != "tag" {
		t.Fatal("adapter leaked mutable tags into legacy state")
	}
}

func TestFinalizePortContractRemainsImplementable(t *testing.T) {
	var _ stockfinalize.Port = (*contractFinalizePort)(nil)
	var _ capfinalization.JobFinalizer = (*contractJobFinalizer)(nil)
}

type contractFinalizePort struct{}

func (*contractFinalizePort) Complete(context.Context, stockfinalize.Request) (stockfinalize.Result, error) {
	return stockfinalize.Result{}, nil
}

type contractJobFinalizer struct{}

func (*contractJobFinalizer) CompleteWithArtifacts(context.Context, capfinalization.FinalizationRequest) (*capfinalization.FinalizationResult, error) {
	return nil, nil
}
