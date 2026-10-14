package overlays

import (
	"fmt"
	"testing"
)

// A text-only named entity card (entity_card / organization / location /
// concept with no AssetRefs) is a first-class editorial category: the entity
// resolver derives it from the certified EntityTimeline and the planner caps it
// per scene, so the editorial budget must admit it instead of dropping it.
func TestEditorialBudgetKeepsTextOnlyNamedEntityCards(t *testing.T) {
	items := []OverlayItem{
		{ID: "card-person", SceneID: "scene-0", Kind: string(KindEntityCard), TemplateID: "person_default", Text: "Ada", StartMs: 0, EndMs: 2000},
		{ID: "card-location", SceneID: "scene-1", Kind: string(KindLocation), TemplateID: "gpe_default", Text: "Roma", StartMs: 2000, EndMs: 4000},
		{ID: "card-org", SceneID: "scene-2", Kind: string(KindOrganization), TemplateID: "org_default", Text: "ESA", StartMs: 4000, EndMs: 6000},
		{ID: "card-concept", SceneID: "scene-3", Kind: string(KindConcept), TemplateID: "concept_default", Text: "Orbita", StartMs: 6000, EndMs: 8000},
	}
	got, _ := ApplyEditorialOverlayBudgetWithImageLimit(items, 0, 0, 0)
	if len(got) != len(items) {
		t.Fatalf("text-only named entity cards kept = %d, want all %d (the budget must not drop the category)", len(got), len(items))
	}
	for _, item := range got {
		if !namedEntityCardKind(item.Kind) {
			t.Fatalf("kept unexpected kind %q", item.Kind)
		}
	}
}

// An image-backed entity card is promoted to KindEntityImage by the plan
// projection and must be admitted by the IMAGE arm, never double-counted by the
// named-entity arm.
func TestNamedEntityCardArmIgnoresImageBackedCards(t *testing.T) {
	imageBacked := OverlayItem{
		ID: "card-with-image", SceneID: "scene-0", Kind: string(KindEntityCard), TemplateID: "person_default",
		Text: "Ada", StartMs: 0, EndMs: 2000,
		AssetRefs: []OverlayAssetRef{{AssetID: "a", URL: "https://example.test/a.jpg", SHA256: "deadbeef"}},
	}
	if isTextOnlyNamedEntityCard(imageBacked) {
		t.Fatal("an entity card with a verified asset must not be admitted by the named-entity arm")
	}
	if got := rankedNamedEntityCardIndices([]OverlayItem{imageBacked}); len(got) != 0 {
		t.Fatalf("named-entity arm admitted %d image-backed cards, want 0", len(got))
	}
}

// A run that narrates more scenes than the run allowance has cards keeps one
// card per scene before repeating a scene: a fifteen-scene script must render a
// name card for every scene it narrates, never only for the first ten.
func TestNamedEntityCardArmSpendsAllowanceAcrossScenes(t *testing.T) {
	items := make([]OverlayItem, 0, 30)
	for scene := 0; scene < 15; scene++ {
		for slot := 0; slot < 2; slot++ {
			items = append(items, OverlayItem{
				ID:         fmt.Sprintf("card-%02d-%d", scene, slot),
				SceneID:    fmt.Sprintf("scene-%02d", scene),
				Kind:       string(KindLocation),
				TemplateID: "gpe_default",
				Text:       fmt.Sprintf("Place %d-%d", scene, slot),
			})
		}
	}
	got := rankedNamedEntityCardIndices(items)
	if len(got) != MaxNamedEntityCardsPerRun {
		t.Fatalf("admitted = %d, want the run allowance %d", len(got), MaxNamedEntityCardsPerRun)
	}
	firstPass := got[:15]
	seen := make(map[string]struct{}, len(firstPass))
	for _, index := range firstPass {
		scene := items[index].SceneID
		if _, duplicate := seen[scene]; duplicate {
			t.Fatalf("scene %s repeated before every scene was covered: %v", scene, firstPass)
		}
		seen[scene] = struct{}{}
	}
	if len(seen) != 15 {
		t.Fatalf("scenes covered = %d, want 15", len(seen))
	}
}

// The run allowance bounds the category, and the scene-scoped identity keeps one
// card per entity per scene.
func TestNamedEntityCardArmCapsAndDeduplicates(t *testing.T) {
	items := make([]OverlayItem, 0, MaxNamedEntityCardsPerRun+6)
	for i := 0; i < MaxNamedEntityCardsPerRun+5; i++ {
		items = append(items, OverlayItem{
			ID: fmt.Sprintf("card-%02d", i), SceneID: fmt.Sprintf("scene-%02d", i),
			Kind: string(KindLocation), TemplateID: "gpe_default", Text: fmt.Sprintf("Place %d", i),
		})
	}
	if got := rankedNamedEntityCardIndices(items); len(got) != MaxNamedEntityCardsPerRun {
		t.Fatalf("ranked = %d, want the run allowance %d", len(got), MaxNamedEntityCardsPerRun)
	}
	duplicate := []OverlayItem{
		{ID: "first", SceneID: "scene-0", Kind: string(KindEntityCard), Text: "Ada", EntityRef: &OverlayEntityRef{CanonicalEntityID: "person:ada"}},
		{ID: "again", SceneID: "scene-0", Kind: string(KindEntityCard), Text: "Ada Lovelace", EntityRef: &OverlayEntityRef{CanonicalEntityID: "person:ada"}},
	}
	if got := rankedNamedEntityCardIndices(duplicate); len(got) != 1 {
		t.Fatalf("same scene + identity admitted %d times, want 1", len(got))
	}
	otherScene := append([]OverlayItem(nil), duplicate...)
	otherScene[1].SceneID = "scene-1"
	if got := rankedNamedEntityCardIndices(otherScene); len(got) != 2 {
		t.Fatalf("one card per scene expected, got %d", len(got))
	}
}
