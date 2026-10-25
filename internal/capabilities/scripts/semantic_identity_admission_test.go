package scriptgeneration

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func identityImageItem(name string) capabilityoverlay.OverlayItem {
	return capabilityoverlay.OverlayItem{
		Kind:     string(capabilityoverlay.KindEntityImage),
		EntityID: "ent-" + name,
		EntityRef: &capabilityoverlay.OverlayEntityRef{
			EntityID: "ent-" + name, Type: "PERSON", Name: name,
		},
		EntityCaption: name,
		AssetRefs:     []capabilityoverlay.OverlayAssetRef{{URL: "https://assets.invalid/" + name + ".jpg"}},
	}
}

func admissionPlan(t *testing.T) *SemanticProductionPlan {
	t.Helper()
	consolidation := planConsolidation([]SemanticProductionCandidate{
		planCandidate("vc_0001", "entity_search", "s01", 1, []string{"person_01"}, "entity:person_01",
			SemanticSearchQuery{Kind: "exact_mention", Text: "Alberto Stasi"}),
		planCandidate("vc_0002", "entity_card", "s02", 2, []string{"person_02"}, "entity:person_02"),
	}, nil)
	plan, err := BuildSemanticProductionPlan(consolidation, planIdentities(), DefaultSemanticProductionPlanConfig())
	if err != nil {
		t.Fatalf("plan build failed: %v", err)
	}
	return plan
}

func TestIdentityImagesAreAdmittedByTheProductionPlan(t *testing.T) {
	plan := admissionPlan(t)
	textOnly := capabilityoverlay.OverlayItem{Kind: string(capabilityoverlay.KindEntityCard), EntityCaption: "Andrea Sempio"}
	items := []capabilityoverlay.OverlayItem{
		identityImageItem("Alberto Stasi"),
		identityImageItem("Andrea Sempio"),
		textOnly,
	}
	got := admitIdentityImagesByProductionPlan(items, plan)
	if len(got) != 2 {
		t.Fatalf("only the admitted identity keeps its image: %+v", got)
	}
	if got[0].EntityRef == nil || got[0].EntityRef.Name != "Alberto Stasi" {
		t.Fatalf("the searched identity must survive: %+v", got[0])
	}
	if len(got[1].AssetRefs) != 0 || got[1].EntityCaption != "Andrea Sempio" {
		t.Fatalf("a text-only card spends no search and stays: %+v", got[1])
	}
}

func TestIdentityImagesAreUntouchedWithoutAPlan(t *testing.T) {
	items := []capabilityoverlay.OverlayItem{identityImageItem("Chiara Poggi")}
	if got := admitIdentityImagesByProductionPlan(items, nil); len(got) != 1 {
		t.Fatalf("a nil plan must not gate identities: %+v", got)
	}
	empty := &SemanticProductionPlan{Schema: SemanticProductionPlanSchema}
	if got := admitIdentityImagesByProductionPlan(items, empty); len(got) != 1 {
		t.Fatalf("an empty plan carries no identity decision: %+v", got)
	}
}

func TestIdentityImagesCannotBeAdmittedByAnUnknownSurface(t *testing.T) {
	plan := admissionPlan(t)
	if plan.AdmitsIdentity("Chiara Poggi") {
		t.Fatal("an identity outside the plan must never be admitted")
	}
	if !plan.AdmitsIdentity("  alberto   STASI ") {
		t.Fatal("admission compares the normalized canonical surface")
	}
}
