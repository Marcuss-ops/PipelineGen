package vidrush

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func entityImagePlan(enabled, required bool) *scriptpkg.ResolvedGenerationPlan {
	return &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: mediadomain.MediaPlanSpec{
			Extraction: mediadomain.MediaExtractionPolicy{
				EntityImages: mediadomain.EntityImagePolicy{Enabled: enabled, Required: required},
			},
		},
	}
}

func TestMaterializeSkipsImageAcquisitionWhenContractDisabled(t *testing.T) {
	processor := &VidRushMaterializationProcessor{}
	plan := &scriptpkg.ResolvedGenerationPlan{MediaRequirements: &scriptpkg.MediaRequirements{}}
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "scene-01", TextHash: "hash-1"}

	got, err := processor.Materialize(context.Background(), plan, segment)
	if err != nil {
		t.Fatalf("Materialize with no image lane: %v", err)
	}
	if got.SegmentID != segment.SegmentID {
		t.Fatalf("materialized segment = %q, want unchanged %q", got.SegmentID, segment.SegmentID)
	}
}

func TestEntityImageCatalogStatusFailureIsFatalOnlyWhenRequired(t *testing.T) {
	optional := entityImagePlan(true, false)
	required := entityImagePlan(true, true)
	transition := entitycatalog.ErrInvalidCandidateTransition

	if entityImageCatalogStatusFailureFatal(optional, transition) {
		t.Fatal("an optional entity-image transition failure must degrade, not fail the run")
	}
	if !entityImageCatalogStatusFailureFatal(required, transition) {
		t.Fatal("a required entity-image transition failure must fail the run")
	}
	if !entityImageCatalogStatusFailureFatal(optional, errors.New("catalog database unavailable")) {
		t.Fatal("non-transition catalog errors must remain fatal for optional imagery")
	}
}
