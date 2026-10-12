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

func TestOverlayCategoryFilterRecognizesSceneTitlesAndBullets(t *testing.T) {
	items := []overlays.OverlayItem{
		{ID: "title", SceneID: "scene-0", Kind: "text_phrase", Params: map[string]any{"editorial_role": "scene_title"}},
		{ID: "bullet", SceneID: "scene-0", Kind: "text_phrase", Params: map[string]any{"editorial_role": "scene_bullet"}},
	}
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryTitles:  scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryBullets: scriptpkg.ToggleDisabled,
	}}
	got := filterOverlayItemsByCategory(items, []Scene{{ID: "scene-0", Index: 0}}, canvas)
	if len(got) != 0 {
		t.Fatalf("filtered items = %#v, want scene title and bullet excluded", got)
	}
}
