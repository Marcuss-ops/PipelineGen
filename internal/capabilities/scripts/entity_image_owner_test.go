package scriptgeneration

import (
	"testing"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

func TestResolveEntityImageDecisionOwnerUsesRequestedSurface(t *testing.T) {
	enabled := mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
		EntityImages: mediadomain.EntityImagePolicy{Enabled: true},
	}}
	if got := ResolveEntityImageDecisionOwner(enabled); got != EntityImageDecisionMediaPipeline {
		t.Fatalf("enabled owner = %q, want %q", got, EntityImageDecisionMediaPipeline)
	}
	included := mediadomain.MediaPlanSpec{Extraction: mediadomain.MediaExtractionPolicy{
		Include: []string{mediadomain.ExtractionIncludeEntities},
	}}
	if got := ResolveEntityImageDecisionOwner(included); got != EntityImageDecisionMediaPipeline {
		t.Fatalf("include-entities owner = %q, want %q", got, EntityImageDecisionMediaPipeline)
	}
	if got := ResolveEntityImageDecisionOwner(mediadomain.MediaPlanSpec{}); got != EntityImageDecisionSemanticPlanner {
		t.Fatalf("no-surface owner = %q, want %q", got, EntityImageDecisionSemanticPlanner)
	}
}

func TestApplySemanticResolvedImagesStandsDownWhenMediaPipelineOwnsSurface(t *testing.T) {
	result := semanticBindingResult(nil)
	result.EntityImageDecisionOwner = EntityImageDecisionMediaPipeline
	if applied := ApplySemanticResolvedImages(result); applied != 0 {
		t.Fatalf("applied = %d, the semantic fallback wrote a competing media-pipeline binding", applied)
	}
	if result.Scenes[0].Annotations.PrimaryEntities[0].Image != nil {
		t.Fatal("semantic plan bound an image while the media pipeline owned the decision")
	}
}

func TestApplySemanticResolvedImagesBindsWhenSemanticPlannerOwnsSurface(t *testing.T) {
	result := semanticBindingResult(nil)
	result.EntityImageDecisionOwner = EntityImageDecisionSemanticPlanner
	if applied := ApplySemanticResolvedImages(result); applied != 1 {
		t.Fatalf("applied = %d, want the semantic owner to bind its verified hit", applied)
	}
	if result.Scenes[0].Annotations.PrimaryEntities[0].Image == nil {
		t.Fatal("semantic owner did not bind its verified hit")
	}
}
