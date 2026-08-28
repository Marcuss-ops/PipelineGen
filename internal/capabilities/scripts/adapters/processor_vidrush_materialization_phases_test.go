package adapters

import (
	"context"
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestMaterializationMetadataOnlyFastPathClonesInput(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{}
	plan.MediaPlan.Materialization.Mode = mediadomain.MaterializationMetadataOnly
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{SegmentID: "segment-1"}}}
	processor := &VidRushMaterializationProcessor{}
	result, handled := processor.metadataOnlyResult(plan, input)
	if !handled || result == nil || len(result.VidRushSegments) != 1 {
		t.Fatalf("fast path result = %#v, handled=%t", result, handled)
	}
	if result.VidRushSegments[0].SegmentID != "segment-1" {
		t.Fatalf("segment = %+v", result.VidRushSegments[0])
	}
	if _, handled := processor.metadataOnlyResult(&scriptpkg.ResolvedGenerationPlan{}, input); handled {
		t.Fatal("non metadata-only plan must not use fast path")
	}
}

func TestMaterializationDependencyPolicyFailsOnlyWhenRequested(t *testing.T) {
	plan := &scriptpkg.ResolvedGenerationPlan{}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{SegmentID: "segment-1"}}}
	if err := materializationDependenciesError(plan, input, nil, nil); err != nil {
		t.Fatalf("unrequested materialization should be a no-op: %v", err)
	}
	plan.MediaPlan.ProviderPolicy.Artlist = "enabled"
	if err := materializationDependenciesError(plan, input, nil, nil); err == nil {
		t.Fatal("requested materialization must fail without dependencies")
	}
	_ = context.Background()
}
