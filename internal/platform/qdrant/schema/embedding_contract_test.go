package schema

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

func TestEmbeddingContract_SameDimensionDifferentModelFails(t *testing.T) {
	want := DefaultV3Schema().GetDense("text")
	got := want.Contract()
	got.Model = "multilingual-e5-base"
	if want.MatchesContract(got) {
		t.Fatal("same-dimensional different-model contract must fail closed")
	}
}

func TestEmbeddingContract_ExactContractMatches(t *testing.T) {
	want := DefaultV3Schema().GetDense("text")
	if !want.MatchesContract(want.Contract()) {
		t.Fatal("an embedding spec must match its own contract")
	}
}

func TestDefaultV3Schema_ModelIdentityMatchesRegistry(t *testing.T) {
	sch := DefaultV3Schema()
	for _, channel := range []string{"text", "transcript"} {
		spec := sch.GetDense(channel)
		if spec == nil {
			t.Fatalf("missing %s channel", channel)
		}
		if spec.Model != models.E5.ID || spec.ModelVersion != models.E5.Revision || spec.Dimensions != models.E5.Dimensions {
			t.Fatalf("%s channel drifted from registry: got model=%q revision=%q dimensions=%d, want model=%q revision=%q dimensions=%d",
				channel, spec.Model, spec.ModelVersion, spec.Dimensions,
				models.E5.ID, models.E5.Revision, models.E5.Dimensions)
		}
	}

	visual := sch.GetDense("visual")
	if visual == nil {
		t.Fatal("missing visual channel")
	}
	if visual.Model != models.SigLIP.ID || visual.ModelVersion != models.SigLIP.Revision || visual.Dimensions != models.SigLIP.Dimensions {
		t.Fatalf("visual channel drifted from registry: got model=%q revision=%q dimensions=%d, want model=%q revision=%q dimensions=%d",
			visual.Model, visual.ModelVersion, visual.Dimensions,
			models.SigLIP.ID, models.SigLIP.Revision, models.SigLIP.Dimensions)
	}
}
