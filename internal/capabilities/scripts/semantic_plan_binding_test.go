package scriptgeneration

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func semanticBindingResult(binding *scriptpkg.EntityImageBinding) *GenerateResult {
	return &GenerateResult{
		Scenes: []Scene{{
			ID: "scene-1",
			Annotations: &scriptpkg.SceneAnnotations{Language: "it", PrimaryEntities: []scriptpkg.AnnotatedEntity{{
				Text: "Alberto Garlasco", CanonicalName: "Alberto Garlasco", Type: "PERSON", Image: binding,
			}}},
		}},
		SemanticProductionPlan: &SemanticProductionPlan{
			Identity: []SemanticPlannedIdentity{{ID: "person:alberto-garlasco", Surface: "Alberto Garlasco", Kind: "person", Media: true}},
			Units: []SemanticProductionUnit{{
				CandidateID: "vc_0001", SceneID: "scene-1", EntityIDs: []string{"person:alberto-garlasco"},
				SearchStatus: ProductionSearchSatisfied, Status: ProductionUnitPlanned,
				ResolvedImage: &SemanticResolvedImage{
					AssetID: "asset-1", URL: "https://upload.wikimedia.org/garlasco.jpg",
					SHA256: strings.Repeat("a", 64), MediaType: "image/jpeg", Provider: "wikimedia_commons",
				},
			}},
		},
	}
}

func TestApplySemanticResolvedImagesBindsVerifiedHit(t *testing.T) {
	result := semanticBindingResult(nil)
	if applied := ApplySemanticResolvedImages(result); applied != 1 {
		t.Fatalf("applied = %d, want 1", applied)
	}
	binding := result.Scenes[0].Annotations.PrimaryEntities[0].Image
	if binding == nil || binding.Status != "bound" {
		t.Fatalf("binding = %+v, want a bound image", binding)
	}
	if binding.SHA256 != strings.Repeat("a", 64) || binding.PreviewURL != "https://upload.wikimedia.org/garlasco.jpg" {
		t.Fatalf("binding identity = %+v, want the verified hash and fetchable URL", binding)
	}
	if binding.Source != "wikimedia_commons" || binding.MediaType != "image/jpeg" {
		t.Fatalf("binding provenance = %+v", binding)
	}
}

func TestApplySemanticResolvedImagesRequiresSatisfiedSearch(t *testing.T) {
	result := semanticBindingResult(nil)
	result.SemanticProductionPlan.Units[0].SearchStatus = ProductionSearchFailed
	if applied := ApplySemanticResolvedImages(result); applied != 0 {
		t.Fatalf("applied = %d, a hit from a non-satisfied unit must not bind", applied)
	}
}

func TestApplySemanticResolvedImagesRequiresContentAddress(t *testing.T) {
	result := semanticBindingResult(nil)
	result.SemanticProductionPlan.Units[0].ResolvedImage.SHA256 = ""
	if applied := ApplySemanticResolvedImages(result); applied != 0 {
		t.Fatalf("applied = %d, a URL-only hit must not become a rendered card", applied)
	}
	if result.Scenes[0].Annotations.PrimaryEntities[0].Image != nil {
		t.Fatal("URL-only hit was promoted to an entity image")
	}
}

func TestApplySemanticResolvedImagesPreservesVerifiedBinding(t *testing.T) {
	existing := &scriptpkg.EntityImageBinding{
		Status: "bound", AssetID: "media-asset", SHA256: strings.Repeat("b", 64),
		PreviewURL: "https://cdn.invalid/media.jpg",
	}
	result := semanticBindingResult(existing)
	if applied := ApplySemanticResolvedImages(result); applied != 0 {
		t.Fatalf("applied = %d, want 0 when the media stage already bound the entity", applied)
	}
	if got := result.Scenes[0].Annotations.PrimaryEntities[0].Image; got != existing {
		t.Fatalf("binding replaced by the plan fallback: %+v", got)
	}
}

func TestApplySemanticResolvedImagesSkipsMultiIdentityComposition(t *testing.T) {
	result := semanticBindingResult(nil)
	result.SemanticProductionPlan.Units[0].EntityIDs = []string{"person:alberto-garlasco", "person:chiara-poggi"}
	if applied := ApplySemanticResolvedImages(result); applied != 0 {
		t.Fatalf("applied = %d, a one-query multi-identity composition cannot assert an identity match", applied)
	}
}

func TestApplySemanticResolvedImagesSkipsUnknownSceneOrIdentity(t *testing.T) {
	result := semanticBindingResult(nil)
	result.SemanticProductionPlan.Units[0].SceneID = "scene-missing"
	if applied := ApplySemanticResolvedImages(result); applied != 0 {
		t.Fatalf("applied = %d, want 0 for an unknown scene", applied)
	}
	result = semanticBindingResult(nil)
	result.SemanticProductionPlan.Identity = nil
	if applied := ApplySemanticResolvedImages(result); applied != 0 {
		t.Fatalf("applied = %d, want 0 when the plan cannot name the identity surface", applied)
	}
}

func TestApplySemanticResolvedImagesMatchesAnnotatedTextAlias(t *testing.T) {
	result := semanticBindingResult(nil)
	entity := &result.Scenes[0].Annotations.PrimaryEntities[0]
	entity.CanonicalName = ""
	entity.Text = "  alberto   garlasco "
	if applied := ApplySemanticResolvedImages(result); applied != 1 {
		t.Fatalf("applied = %d, want 1 for a normalized surface match", applied)
	}
}
