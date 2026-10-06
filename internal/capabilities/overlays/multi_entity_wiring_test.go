package overlays

import (
	"strings"
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
			AssetRef: &OverlayAssetRef{AssetID: "asset_alpha", SHA256: strings.Repeat("a", 64)},
		},
		{
			ID:       "ent_2",
			SceneID:  "scene_01",
			Kind:     MultiEntityKindImage,
			Name:     "Entity Beta",
			StartMS:  2800, // gap = 300ms <= 1800ms
			EndMS:    4500,
			AssetRef: &OverlayAssetRef{AssetID: "asset_beta", SHA256: strings.Repeat("b", 64)},
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
	items, err := WireMultiEntityOverlays(g, 1920, 1080)
	if err != nil || len(items) != 1 {
		t.Fatalf("WireMultiEntityOverlays() = %d items, err=%v; want one canonical image-stack item", len(items), err)
	}
	item := items[0]
	if item.Kind != string(KindEntityImage) || item.TemplateID != "IMAGE_OVERLAY" {
		t.Errorf("group must lower to the canonical entity_image primitive, got kind/template %q/%q", item.Kind, item.TemplateID)
	}
	if len(item.ImageLayers) != 2 {
		t.Errorf("expected 2 image layers, got %d", len(item.ImageLayers))
	}
	if item.ImageLayers[0].Caption != "Entity Alpha" || item.ImageLayers[1].Caption != "Entity Beta" {
		t.Errorf("image caption names = %q/%q, want each entity name", item.ImageLayers[0].Caption, item.ImageLayers[1].Caption)
	}
	for _, layer := range item.ImageLayers {
		if layer.MotionID == "" || !containsString(CertifiedSingleImageMotions(), layer.MotionID) || layer.PresetID == "" {
			t.Errorf("child layer must have a certified independent motion and preset: %+v", layer)
		}
	}
	if item.ImageLayers[0].MotionID == item.ImageLayers[1].MotionID {
		t.Errorf("adjacent images should use independent motions: %q", item.ImageLayers[0].MotionID)
	}
}

func TestGroupNearbyEntities_TrioQuadPenta(t *testing.T) {
	var candidates []MultiEntityCandidate
	for i := 0; i < 5; i++ {
		candidates = append(candidates, MultiEntityCandidate{
			ID:       string(rune('A' + i)),
			SceneID:  "scene_02",
			Kind:     MultiEntityKindImage,
			StartMS:  int64(i * 900),
			EndMS:    int64((i + 1) * 900),
			AssetRef: &OverlayAssetRef{AssetID: string(rune('A' + i)), SHA256: strings.Repeat("a", 64)},
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

	items, err := WireMultiEntityOverlays(g, 1920, 1080)
	if err != nil || len(items) != 1 {
		t.Fatalf("WireMultiEntityOverlays() = %d items, err=%v; want one image stack", len(items), err)
	}
	if len(items[0].ImageLayers) != 5 {
		t.Errorf("expected 5 image layers, got %d", len(items[0].ImageLayers))
	}
	if items[0].ImageLayers[0].Params["position_x"] != float64(-0.32)*1920 || items[0].ImageLayers[4].Params["position_y"] != float64(0.24)*1080 {
		t.Errorf("five-image layout positions do not match the canonical x5 editorial grid: first=%v last=%v", items[0].ImageLayers[0].Params, items[0].ImageLayers[4].Params)
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

	items, err := WireMultiEntityOverlays(g, 1920, 1080)
	if err != nil || len(items) != 3 {
		t.Fatalf("WireMultiEntityOverlays() = %d items, err=%v; want three canonical phrase items", len(items), err)
	}
	for i, want := range []struct {
		text       string
		start, end int64
	}{
		{"First Key Insight", 500, 1500},
		{"Second Key Insight", 2000, 3200},
		{"Third Key Insight", 3800, 5000},
	} {
		item := items[i]
		if item.Kind != "text_phrase" || item.TemplateID != "IMPORTANT_PHRASE" || item.Text != want.text || item.StartMs != want.start || item.EndMs != want.end || item.MotionID == "" {
			t.Errorf("phrase[%d] did not preserve canonical phrase text/timing/motion: %+v", i, item)
		}
	}
	if EditorialSectionForItem(items[0]) != EditorialSectionShortPhrase {
		t.Fatalf("three-word grouped phrase section=%q, want short important phrase", EditorialSectionForItem(items[0]))
	}
}

func TestGroupNearbyEntitiesKeepsMixedScenesAndImageWindowSeparate(t *testing.T) {
	candidates := []MultiEntityCandidate{
		{ID: "one", SceneID: "scene-one", Kind: MultiEntityKindPhrase, Text: "First insight", StartMS: 0, EndMS: 1000},
		{ID: "two", SceneID: "scene-two", Kind: MultiEntityKindPhrase, Text: "Second insight", StartMS: 1000, EndMS: 2000},
		{ID: "image-a", SceneID: "scene-images", Kind: MultiEntityKindImage, StartMS: 0, EndMS: 1200, AssetRef: &OverlayAssetRef{AssetID: "a", SHA256: strings.Repeat("a", 64)}},
		{ID: "image-b", SceneID: "scene-images", Kind: MultiEntityKindImage, StartMS: 4500, EndMS: 5700, AssetRef: &OverlayAssetRef{AssetID: "b", SHA256: strings.Repeat("b", 64)}},
	}
	groups := GroupNearbyEntities(candidates, 5000)
	if len(groups) != 4 {
		t.Fatalf("groups=%d, want scene changes and >5s image span to produce 4 groups: %+v", len(groups), groups)
	}
	for _, group := range groups {
		if len(group.Items) > 1 && group.Items[0].SceneID != group.Items[1].SceneID {
			t.Fatalf("cross-scene group accepted: %+v", group.Items)
		}
		if group.GroupType == MultiEntityKindImage {
			items, err := WireMultiEntityOverlays(group, 1280, 720)
			if err != nil {
				t.Fatal(err)
			}
			if len(items) != 1 || items[0].EndMs-items[0].StartMs > MaxImageOverlayDurationMS {
				t.Fatalf("image group violates five-second image ceiling: %+v", items)
			}
		}
	}
}

func TestGroupNearbyEntities_IsolatedSeparation(t *testing.T) {
	candidates := []MultiEntityCandidate{
		{
			ID:      "c1",
			SceneID: "scene_1",
			Kind:    MultiEntityKindPhrase,
			Text:    "One grounded thought",
			StartMS: 1000,
			EndMS:   2000,
		},
		{
			ID:      "c2",
			SceneID: "scene_1",
			Kind:    MultiEntityKindPhrase,
			Text:    "Another grounded thought",
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

func TestBuildPlanLowersEditorialGroupsToCanonicalRenderPrimitives(t *testing.T) {
	group := MultiEntityGroup{
		GroupID: "plan-group", SceneID: "scene-plan", GroupType: MultiEntityKindImage,
		Count: 2, StartMS: 100, EndMS: 5100,
		Items: []MultiEntityCandidate{
			{ID: "image-a", SceneID: "scene-plan", Kind: MultiEntityKindImage, StartMS: 100, EndMS: 3000, AssetRef: &OverlayAssetRef{AssetID: "a", SHA256: strings.Repeat("a", 64)}},
			{ID: "image-b", SceneID: "scene-plan", Kind: MultiEntityKindImage, StartMS: 2200, EndMS: 5100, AssetRef: &OverlayAssetRef{AssetID: "b", SHA256: strings.Repeat("b", 64)}},
		},
	}
	plan, err := BuildPlan(PlanInput{
		PlanID: "plan-editorial-groups", VideoID: "video-editorial-groups", Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		Scenes: []SceneInput{{ID: "scene-plan", MultiEntityGroups: []MultiEntityGroup{group}}},
	}, AllCandidatesPlannerConfig([]SceneInput{{ID: "scene-plan", MultiEntityGroups: []MultiEntityGroup{group}}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("canonical grouped plan failed validation: %v", err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Kind != string(KindEntityImage) || len(plan.Items[0].ImageLayers) != 2 {
		t.Fatalf("group plan emitted a noncanonical image primitive: %+v", plan.Items)
	}
	if plan.Items[0].MotionID != "" {
		t.Fatalf("stack parent must not duplicate its independently selected layer motions: %+v", plan.Items[0])
	}
	for _, layer := range plan.Items[0].ImageLayers {
		if !containsString(CertifiedSingleImageMotions(), layer.MotionID) {
			t.Fatalf("image layer motion %q is outside the existing certified image pool", layer.MotionID)
		}
	}
	if EditorialSectionForItem(plan.Items[0]) != EditorialSectionImageStack {
		t.Fatalf("stack editorial section=%q, want %q", EditorialSectionForItem(plan.Items[0]), EditorialSectionImageStack)
	}
}

func TestWireMultiEntityOverlaysSeparatesMixedContentIntoExistingKinds(t *testing.T) {
	group := MultiEntityGroup{
		GroupID: "mixed-group", SceneID: "scene-mixed", GroupType: MultiEntityKindMixed,
		Count: 2, StartMS: 0, EndMS: 5000,
		Items: []MultiEntityCandidate{
			{ID: "photo", SceneID: "scene-mixed", Kind: MultiEntityKindImage, Name: "Ada", StartMS: 0, EndMS: 2500, AssetRef: &OverlayAssetRef{AssetID: "portrait", SHA256: strings.Repeat("a", 64)}},
			{ID: "phrase", SceneID: "scene-mixed", Kind: MultiEntityKindPhrase, Text: "A remarkable idea", StartMS: 2500, EndMS: 5000},
		},
	}
	items, err := WireMultiEntityOverlays(group, 1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("mixed group produced %d items, want one image + one phrase", len(items))
	}
	if items[0].Kind != string(KindEntityImage) || items[1].Kind != "text_phrase" || items[0].EntityCaption != "Ada" {
		t.Fatalf("mixed group kinds/caption = %q/%q/%q, want entity_image with caption + canonical text_phrase", items[0].Kind, items[1].Kind, items[0].EntityCaption)
	}
	if EditorialSectionForItem(items[0]) != EditorialSectionEntityTextImage || EditorialSectionForItem(items[1]) != EditorialSectionShortPhrase {
		t.Fatalf("mixed group items did not join their editorial sections: %+v", items)
	}
}
