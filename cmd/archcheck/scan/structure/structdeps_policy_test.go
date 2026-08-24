package structure

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
)

func TestStructDepsThresholdConsumesClipIngestPolicy(t *testing.T) {
	pol := &policy.Policy{
		MaxStructDeps:               8,
		MaxClipIngestPipelineFields: 9,
	}
	if got := structDepsThreshold(pol, clipIngestPipelineDepsRelPath, "ClipIngestPipelineDeps"); got != 9 {
		t.Fatalf("canonical ClipIngestPipelineDeps threshold=%d, want 9", got)
	}
	if got := structDepsThreshold(pol, clipIngestPipelineDepsRelPath, "OtherDeps"); got != 8 {
		t.Fatalf("non-canonical type threshold=%d, want global 8", got)
	}
	if got := structDepsThreshold(pol, "internal/application/other.go", "ClipIngestPipelineDeps"); got != 8 {
		t.Fatalf("non-canonical path threshold=%d, want global 8", got)
	}
}
