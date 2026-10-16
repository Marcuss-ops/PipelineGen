package scriptgeneration

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestOverlayCategoryFilterAppliesRunDefaultsAndSceneOverrides(t *testing.T) {
	items := []overlays.OverlayItem{
		{ID: "date", SceneID: "scene-0", Kind: "number", TemplateID: "TIMELINE_DATE_CARD"},
		{ID: "image", SceneID: "scene-1", Kind: "entity_image", TemplateID: "person_default"},
	}
	canvas := OverlayCanvasSpec{
		OverlayCategories:       map[scriptpkg.OverlayCategory]scriptpkg.Toggle{scriptpkg.OverlayCategoryNumbers: scriptpkg.ToggleDisabled, scriptpkg.OverlayCategoryImages: scriptpkg.ToggleDisabled},
		OverlayCategorySegments: []scriptpkg.ScriptSegment{{ID: "scene-0", OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{scriptpkg.OverlayCategoryNumbers: scriptpkg.ToggleEnabled}}},
	}
	got := filterOverlayItemsByCategory(items, []Scene{{ID: "scene-0", Index: 0}, {ID: "scene-1", Index: 1}}, canvas)
	if len(got) != 1 || got[0].ID != "date" {
		t.Fatalf("filtered items = %#v, want only enabled scene date", got)
	}
}

func TestOverlayCategoryFilterUsesPositionForIDLessSegments(t *testing.T) {
	items := []overlays.OverlayItem{{ID: "phrase", SceneID: "scene-1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE"}}
	canvas := OverlayCanvasSpec{OverlayCategorySegments: []scriptpkg.ScriptSegment{{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{scriptpkg.OverlayCategoryPhrases: scriptpkg.ToggleDisabled}}}}
	got := filterOverlayItemsByCategory(items, []Scene{{ID: "scene-1", Index: 0}}, canvas)
	if len(got) != 0 {
		t.Fatalf("filtered items = %#v, want positional segment exclusion", got)
	}
}

// A text-only named entity card is the entities_with_text lane, not imagery:
// an `images: disabled` toggle removes photo overlays (including image-backed
// entity cards) and must NOT delete the name cards the entity timeline
// derived — those are bounded by the entities_with_text group limit instead.
// The GPE/LOCATION alias matters here: the normalized place type produces the
// same text-only `location` card as an entity_card, and both must survive.
func TestOverlayCategoryFilterKeepsTextOnlyNamedEntityCards(t *testing.T) {
	items := []overlays.OverlayItem{
		{ID: "person-card", SceneID: "scene-0", Kind: "entity_card", TemplateID: "person_default", Text: "Ada Lovelace"},
		{ID: "place-card", SceneID: "scene-0", Kind: "location", TemplateID: "gpe_default", Text: "Roma"},
		{ID: "org-card", SceneID: "scene-0", Kind: "organization", TemplateID: "org_default", Text: "ESA"},
		{ID: "photo", SceneID: "scene-0", Kind: "entity_image", TemplateID: "person_default",
			AssetRefs: []overlays.OverlayAssetRef{{SHA256: "hash-photo"}}},
	}
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryImages: scriptpkg.ToggleDisabled,
	}}
	got := filterOverlayItemsByCategory(items, []Scene{{ID: "scene-0", Index: 0}}, canvas)
	ids := make([]string, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.ID)
	}
	want := []string{"person-card", "place-card", "org-card"}
	if len(ids) != len(want) {
		t.Fatalf("kept %v, want the three text-only name cards %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("kept %v, want %v", ids, want)
		}
	}
}

// The entities_with_text group limit is the documented control for the
// text-only name cards: a present zero drops them, so the lane is still
// fully controllable from the payload.
func TestOverlayGroupLimitsDisableTextOnlyNamedEntityCards(t *testing.T) {
	items := []overlays.OverlayItem{
		{ID: "person-card", SceneID: "scene-0", Kind: "entity_card", TemplateID: "person_default", Text: "Ada Lovelace"},
		{ID: "photo", SceneID: "scene-0", Kind: "entity_image", TemplateID: "person_default",
			AssetRefs: []overlays.OverlayAssetRef{{SHA256: "hash-photo"}}},
	}
	got := applyOverlayGroupLimits(append([]overlays.OverlayItem(nil), items...), map[string]int{"entities_with_text": 0})
	if len(got) != 1 || got[0].ID != "photo" {
		t.Fatalf("kept %#v, want only the image-backed card", got)
	}
}

func TestOverlayCategoryFilterRecognizesSceneTitlesAndBullets(t *testing.T) {
	items := []overlays.OverlayItem{
		{ID: "title", SceneID: "scene-0", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Params: map[string]any{"editorial_role": "scene_title"}},
		{ID: "bullet", SceneID: "scene-0", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Params: map[string]any{"editorial_role": "scene_bullet"}},
	}
	scenes := []Scene{{ID: "scene-0", Index: 0}}
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryTitles:  scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryBullets: scriptpkg.ToggleEnabled,
		scriptpkg.OverlayCategoryPhrases: scriptpkg.ToggleDisabled,
	}}
	got := filterOverlayItemsByCategory(items, scenes, canvas)
	if len(got) != 1 || got[0].ID != "bullet" {
		t.Fatalf("filtered items = %#v, want the explicit scene bullet despite its shared phrase template", got)
	}
	if category, ok := overlayItemCategory(got[0]); !ok || category != scriptpkg.OverlayCategoryBullets {
		t.Fatalf("scene bullet category = %q, %v; want bullets", category, ok)
	}

	canvas.OverlayCategories[scriptpkg.OverlayCategoryBullets] = scriptpkg.ToggleDisabled
	got = filterOverlayItemsByCategory(items, scenes, canvas)
	if len(got) != 0 {
		t.Fatalf("filtered items = %#v, want scene title and bullet excluded when bullets are disabled", got)
	}
}
