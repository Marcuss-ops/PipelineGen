package adapters

import (
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func semanticPlanSpec(binding *scriptpkg.EntityImageBinding) scriptpkg.SpecSceneOutput {
	return scriptpkg.SpecSceneOutput{Version: 1, Scenes: []scriptpkg.SpecScene{{
		ID: "scene-garlasco", SegmentID: "scene-garlasco", Index: 0,
		Text: "Alberto Garlasco parlò del caso.",
		Annotations: &scriptpkg.SceneAnnotations{PrimaryEntities: []scriptpkg.AnnotatedEntity{{
			ID: "entity-garlasco", Text: "Alberto Garlasco", CanonicalName: "Alberto Garlasco", Type: "PERSON", Image: binding,
		}}},
	}}}
}

// TestProjectEntityImageBindingsKeepsPlanBindingWithoutSegment pins the
// consumption contract of the semantic production plan: when the media stage
// produced no segment at all, the verified plan hit must survive. Resetting it
// to not_found is what made a resolved, content-addressed search invisible to
// the overlay.
func TestProjectEntityImageBindingsKeepsPlanBindingWithoutSegment(t *testing.T) {
	planBinding := &scriptpkg.EntityImageBinding{
		Status: "bound", AssetID: "asset-garlasco", SHA256: strings.Repeat("a", 64),
		PreviewURL: "https://upload.wikimedia.org/garlasco.jpg", Source: "wikimedia_commons",
	}
	out := projectEntityImageBindings(semanticPlanSpec(planBinding), nil, entityImagePolicyForTest())
	got := out.Scenes[0].Annotations.PrimaryEntities[0].Image
	if got == nil || got.Status != "bound" || got.AssetID != "asset-garlasco" || got.SHA256 != planBinding.SHA256 ||
		got.PreviewURL != planBinding.PreviewURL || got.Source != "wikimedia_commons" {
		t.Fatalf("binding = %+v, want the verified plan binding preserved", got)
	}
}

// TestProjectEntityImageBindingsKeepsPlanBindingWhenSegmentHasNoCandidate
// covers the more common shape: the segment exists but its fan-out found no
// usable portrait. Coverage stays with the planner instead of regressing to
// not_found.
func TestProjectEntityImageBindingsKeepsPlanBindingWhenSegmentHasNoCandidate(t *testing.T) {
	planBinding := &scriptpkg.EntityImageBinding{
		Status: "bound", AssetID: "asset-garlasco", SHA256: strings.Repeat("a", 64),
		PreviewURL: "https://upload.wikimedia.org/garlasco.jpg", Source: "wikimedia_commons",
	}
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "scene-garlasco", SceneID: "scene-garlasco",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			readyEntityImageCandidate("asset-other", "Chiara Poggi", "Chiara Poggi"),
		}},
	}}
	out := projectEntityImageBindings(semanticPlanSpec(planBinding), segments, entityImagePolicyForTest())
	got := out.Scenes[0].Annotations.PrimaryEntities[0].Image
	if got == nil || got.Status != "bound" || got.AssetID != "asset-garlasco" || got.SHA256 != planBinding.SHA256 {
		t.Fatalf("binding = %+v, want the verified plan binding preserved over a wrong-identity candidate", got)
	}
}

// TestProjectEntityImageBindingsMediaCandidateWinsOverPlan pins the precedence:
// a durable candidate discovered by the media stage is the authority and must
// replace the plan fallback.
func TestProjectEntityImageBindingsMediaCandidateWinsOverPlan(t *testing.T) {
	planBinding := &scriptpkg.EntityImageBinding{
		Status: "bound", AssetID: "asset-plan", SHA256: strings.Repeat("a", 64),
		PreviewURL: "https://upload.wikimedia.org/plan.jpg",
	}
	segments := []scriptpkg.VidRushSegmentResult{{
		SegmentID: "scene-garlasco", SceneID: "scene-garlasco",
		Assets: scriptpkg.SegmentAssetSelection{Candidates: []scriptpkg.SegmentAssetCandidate{
			readyEntityImageCandidate("asset-media", "Alberto Garlasco", "Alberto Garlasco"),
		}},
	}}
	out := projectEntityImageBindings(semanticPlanSpec(planBinding), segments, entityImagePolicyForTest())
	got := out.Scenes[0].Annotations.PrimaryEntities[0].Image
	if got == nil || got.Status != "resolved" || got.AssetID != "asset-media" {
		t.Fatalf("binding = %+v, want the media-stage candidate to win", got)
	}
}

// TestProjectEntityImageBindingsDoesNotPreserveUnhashedBinding guards the
// admission rule: a URL-only or unresolved binding cannot become a card asset,
// so the media stage still reports not_found.
func TestProjectEntityImageBindingsDoesNotPreserveUnhashedBinding(t *testing.T) {
	unhashed := &scriptpkg.EntityImageBinding{Status: "bound", PreviewURL: "https://upload.wikimedia.org/x.jpg"}
	out := projectEntityImageBindings(semanticPlanSpec(unhashed), nil, entityImagePolicyForTest())
	if got := out.Scenes[0].Annotations.PrimaryEntities[0].Image; got == nil || got.Status != "not_found" {
		t.Fatalf("binding = %+v, want not_found for an unhashed binding", got)
	}
}
