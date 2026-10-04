package overlays

import (
	"testing"
)

func TestGroupNearbyEntities_DuoImages(t *testing.T) {
	candidates := []MultiEntityCandidate{
		{
			ID:       "ent_1",
			SceneID:  "scene_01",
			Kind:     MultiEntityKindImage,
			Name:     "Entity Alpha",
			StartMS:  1000,
			EndMS:    2500,
			AssetRef: &OverlayAssetRef{AssetID: "asset_alpha"},
		},
		{
			ID:       "ent_2",
			SceneID:  "scene_01",
			Kind:     MultiEntityKindImage,
			Name:     "Entity Beta",
			StartMS:  2800, // gap = 300ms <= 1800ms
			EndMS:    4500,
			AssetRef: &OverlayAssetRef{AssetID: "asset_beta"},
		},
	}

	groups := GroupNearbyEntities(candidates, 1800)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}

	g := groups[0]
	if g.Count != 2 {
		t.Errorf("expected count=2, got %d", g.Count)
	}
	if g.GroupType != MultiEntityKindImage {
		t.Errorf("expected group_type=image, got %s", g.GroupType)
	}
	if !IsMultiEntityMotion(g.MotionPreset) {
		t.Errorf("expected valid multi-entity motion, got %s", g.MotionPreset)
	}

	item := WireMultiEntityOverlay(g)
	if item.TemplateID != "MULTI_ENTITY" {
		t.Errorf("expected template_id MULTI_ENTITY, got %s", item.TemplateID)
	}
	if item.PresetID != "multi_entity_layout_v1" {
		t.Errorf("expected preset_id multi_entity_layout_v1, got %s", item.PresetID)
	}
	if len(item.ImageLayers) != 2 {
		t.Errorf("expected 2 image layers, got %d", len(item.ImageLayers))
	}
	if item.ImageLayers[0].Caption != "Entity Alpha" || item.ImageLayers[1].Caption != "Entity Beta" {
		t.Errorf("image caption names = %q/%q, want each entity name", item.ImageLayers[0].Caption, item.ImageLayers[1].Caption)
	}
	if item.ImageLayers[0].CaptionMotionID == "" || item.ImageLayers[1].CaptionMotionID == "" || item.ImageLayers[0].CaptionMotionID == item.ImageLayers[1].CaptionMotionID {
		t.Errorf("image captions should have varied motions: %q/%q", item.ImageLayers[0].CaptionMotionID, item.ImageLayers[1].CaptionMotionID)
	}
}

func TestGroupNearbyEntities_TrioQuadPenta(t *testing.T) {
	var candidates []MultiEntityCandidate
	for i := 0; i < 5; i++ {
		candidates = append(candidates, MultiEntityCandidate{
			ID:       string(rune('A' + i)),
			SceneID:  "scene_02",
			Kind:     MultiEntityKindImage,
			StartMS:  int64(i * 1200),
			EndMS:    int64((i + 1) * 1200),
			AssetRef: &OverlayAssetRef{AssetID: string(rune('A' + i))},
		})
	}

	groups := GroupNearbyEntities(candidates, 1500)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group of 5, got %d", len(groups))
	}

	g := groups[0]
	if g.Count != 5 {
		t.Errorf("expected count=5, got %d", g.Count)
	}

	motion := SelectMultiEntityMotion(MultiEntityKindImage, 5, 0)
	if motion != "penta_hero_plus_four" {
		t.Errorf("expected penta_hero_plus_four, got %s", motion)
	}

	item := WireMultiEntityOverlay(g)
	if len(item.ImageLayers) != 5 {
		t.Errorf("expected 5 image layers, got %d", len(item.ImageLayers))
	}
}

func TestGroupNearbyEntities_Phrases(t *testing.T) {
	phrases := []MultiEntityCandidate{
		{
			ID:      "ph_1",
			SceneID: "scene_phrase",
			Kind:    MultiEntityKindPhrase,
			Text:    "First Key Insight",
			StartMS: 500,
			EndMS:   1500,
		},
		{
			ID:      "ph_2",
			SceneID: "scene_phrase",
			Kind:    MultiEntityKindPhrase,
			Text:    "Second Key Insight",
			StartMS: 2000,
			EndMS:   3200,
		},
		{
			ID:      "ph_3",
			SceneID: "scene_phrase",
			Kind:    MultiEntityKindPhrase,
			Text:    "Third Key Insight",
			StartMS: 3800,
			EndMS:   5000,
		},
	}

	groups := GroupNearbyEntities(phrases, 1200)
	if len(groups) != 1 {
		t.Fatalf("expected 1 group of 3 phrases, got %d", len(groups))
	}

	g := groups[0]
	if g.Count != 3 {
		t.Errorf("expected count=3, got %d", g.Count)
	}
	if g.GroupType != MultiEntityKindPhrase {
		t.Errorf("expected phrase group, got %s", g.GroupType)
	}

	item := WireMultiEntityOverlay(g)
	if item.TemplateID != "MULTI_PHRASE" {
		t.Errorf("expected MULTI_PHRASE, got %s", item.TemplateID)
	}
	if item.PresetID != "multi_phrase_layout_v1" {
		t.Errorf("expected multi_phrase_layout_v1, got %s", item.PresetID)
	}
	if item.Text == "" {
		t.Errorf("expected non-empty combined text")
	}
}

func TestGroupNearbyEntities_IsolatedSeparation(t *testing.T) {
	candidates := []MultiEntityCandidate{
		{
			ID:      "c1",
			SceneID: "scene_1",
			StartMS: 1000,
			EndMS:   2000,
		},
		{
			ID:      "c2",
			SceneID: "scene_1",
			StartMS: 8000, // gap = 6000ms > 1800ms
			EndMS:   9000,
		},
	}

	groups := GroupNearbyEntities(candidates, 1800)
	if len(groups) != 2 {
		t.Fatalf("expected 2 isolated groups, got %d", len(groups))
	}
	if groups[0].Count != 1 || groups[1].Count != 1 {
		t.Errorf("expected count 1 each, got %d and %d", groups[0].Count, groups[1].Count)
	}
}
