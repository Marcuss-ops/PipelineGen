package scriptgeneration

import (
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"
	"testing"
)

func identityImageItem(name string) capmodel.OverlayItem {
	return capmodel.OverlayItem{
		Kind:     string(capmodel.KindEntityImage),
		EntityID: "ent-" + name,
		EntityRef: &capmodel.OverlayEntityRef{
			EntityID: "ent-" + name, Type: "PERSON", Name: name,
		},
		EntityCaption: name,
		AssetRefs:     []capmodel.OverlayAssetRef{{URL: "https://assets.invalid/" + name + ".jpg"}},
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
	textOnly := capmodel.OverlayItem{Kind: string(capmodel.KindEntityCard), EntityCaption: "Andrea Sempio"}
	items := []capmodel.OverlayItem{
		identityImageItem("Alberto Stasi"),
		identityImageItem("Andrea Sempio"),
		textOnly,
	}
	got := admitIdentityImagesByProductionPlan(items, plan)
	if len(got) != 3 {
		t.Fatalf("a non-admitted identity keeps its text card instead of disappearing: %+v", got)
	}
	if got[0].EntityRef == nil || got[0].EntityRef.Name != "Alberto Stasi" || len(got[0].AssetRefs) == 0 {
		t.Fatalf("the searched identity must keep its image: %+v", got[0])
	}
	if len(got[1].AssetRefs) != 0 || got[1].Kind != string(capmodel.KindEntityCard) || got[1].Text != "Andrea Sempio" {
		t.Fatalf("an unverified non-admitted image must degrade to a text card: %+v", got[1])
	}
	if len(got[2].AssetRefs) != 0 || got[2].EntityCaption != "Andrea Sempio" {
		t.Fatalf("a text-only card spends no search and stays: %+v", got[2])
	}
}

// The bundle is the primary, already-verified asset source. A semantic search
// budget that did not allocate a media unit to this identity must not delete
// produced bytes: the overlay remains an image card.
func TestVerifiedIdentityImageSurvivesUnadmittedProductionPlan(t *testing.T) {
	plan := &SemanticProductionPlan{Schema: SemanticProductionPlanSchema, Identity: []SemanticPlannedIdentity{{
		ID: "person_02", Surface: "Andrea Sempio", Kind: "PERSON", Media: false,
	}}}
	item := identityImageItem("Andrea Sempio")
	sha := strings.Repeat("a", 64)
	item.AssetRefs[0].AssetID = sha
	item.AssetRefs[0].SHA256 = sha
	got := admitIdentityImagesByProductionPlan([]capmodel.OverlayItem{item}, plan)
	if len(got) != 1 || got[0].Kind != string(capmodel.KindEntityImage) || len(got[0].AssetRefs) != 1 {
		t.Fatalf("a verified bundle image must survive the semantic budget: %+v", got)
	}
}

func TestIdentityImagesAreUntouchedWithoutAPlan(t *testing.T) {
	items := []capmodel.OverlayItem{identityImageItem("Chiara Poggi")}
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
