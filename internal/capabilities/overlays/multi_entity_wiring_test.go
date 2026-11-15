package overlays

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
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
			AssetRef: &capmodel.OverlayAssetRef{AssetID: "asset_alpha", SHA256: strings.Repeat("a", 64)},
		},
		{
			ID:       "ent_2",
			SceneID:  "scene_01",
			Kind:     MultiEntityKindImage,
			Name:     "Entity Beta",
			StartMS:  2800, // gap = 300ms <= 1800ms
			EndMS:    4500,
			AssetRef: &capmodel.OverlayAssetRef{AssetID: "asset_beta", SHA256: strings.Repeat("b", 64)},
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
		t.Fatalf("WireMultiEntityOverlays() = %d items, err=%v; want one sequenced image group", len(items), err)
	}
	if len(items[0].ImageLayers) != 2 || items[0].CameraMotionID != "" || items[0].GroupMotionID != "" {
		t.Fatalf("wiring must leave one shared window and no camera before the planner chooses the group choreography: %+v", items[0])
	}
	for i, want := range []struct {
		id, caption string
	}{
		{"ent_1", "Entity Alpha"}, {"ent_2", "Entity Beta"},
	} {
		item := items[0].ImageLayers[i]
		if item.ID != want.id || item.Caption != want.caption {
			t.Errorf("image %d identity/caption = %q/%q, want %q/%q", i, item.ID, item.Caption, want.id, want.caption)
		}
		if item.StartMS != 0 || item.EndMS != g.EndMS-g.StartMS {
			t.Errorf("image %d is not on the shared group window: %+v", i, item)
		}
		if item.CaptionMotionID == "" || item.PresetID == "" || item.MotionID != "" {
			t.Errorf("image member should keep caption/preset and leave motion to the group choreography: %+v", item)
		}
	}
}

func TestWireMultiEntityLeavesGroupChoreographyToPlanner(t *testing.T) {
	group := MultiEntityGroup{
		GroupID: "group-window", SceneID: "scene-window", GroupType: MultiEntityKindImage,
		Count: 2, StartMS: 100, EndMS: 5100,
		Items: []MultiEntityCandidate{
			{ID: "a", SceneID: "scene-window", Kind: MultiEntityKindImage, StartMS: 100, EndMS: 3000, AssetRef: &capmodel.OverlayAssetRef{AssetID: "a", SHA256: strings.Repeat("a", 64)}},
			{ID: "b", SceneID: "scene-window", Kind: MultiEntityKindImage, StartMS: 2200, EndMS: 5100, AssetRef: &capmodel.OverlayAssetRef{AssetID: "b", SHA256: strings.Repeat("b", 64)}},
		},
	}
	items, err := WireMultiEntityOverlays(group, 1280, 720)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(items[0].ImageLayers) != 2 {
		t.Fatalf("image group did not become one composite: %+v", items)
	}
	if items[0].CameraMotionID != "" || items[0].GroupMotionID != "" {
		t.Fatalf("wiring pre-empted the planner's group choreography: camera=%q group=%q", items[0].CameraMotionID, items[0].GroupMotionID)
	}
	for _, layer := range items[0].ImageLayers {
		if layer.StartMS != 0 || layer.EndMS != 5000 {
			t.Fatalf("layer %q is not on the shared group window: %+v", layer.ID, layer)
		}
	}

	single := group
	single.Items = group.Items[:1]
	single.Count = 1
	single.GroupID = "single-window"
	singleItems, err := WireMultiEntityOverlays(single, 1280, 720)
	if err != nil {
		t.Fatal(err)
	}
	if len(singleItems) != 1 || len(singleItems[0].ImageLayers) != 0 || singleItems[0].CameraMotionID != "" {
		t.Fatalf("single image must stay a standalone image: %+v", singleItems)
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
			AssetRef: &capmodel.OverlayAssetRef{AssetID: string(rune('A' + i)), SHA256: strings.Repeat("a", 64)},
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
		t.Fatalf("WireMultiEntityOverlays() = %d items, err=%v; want one timed image group", len(items), err)
	}
	if len(items[0].ImageLayers) != 5 || len(items[0].AssetRefs) != 5 {
		t.Fatalf("image group has %d layers/%d assets, want five: %+v", len(items[0].ImageLayers), len(items[0].AssetRefs), items[0])
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
		{ID: "image-a", SceneID: "scene-images", Kind: MultiEntityKindImage, StartMS: 0, EndMS: 1200, AssetRef: &capmodel.OverlayAssetRef{AssetID: "a", SHA256: strings.Repeat("a", 64)}},
		{ID: "image-b", SceneID: "scene-images", Kind: MultiEntityKindImage, StartMS: 4500, EndMS: 5700, AssetRef: &capmodel.OverlayAssetRef{AssetID: "b", SHA256: strings.Repeat("b", 64)}},
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
			if len(items) != 1 || len(items[0].ImageLayers) != 0 || items[0].EndMs-items[0].StartMs != 1200 {
				t.Fatalf("grouped source must stay one independent image with its original window: %+v", items)
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
			{ID: "image-a", SceneID: "scene-plan", Kind: MultiEntityKindImage, StartMS: 100, EndMS: 3000, AssetRef: &capmodel.OverlayAssetRef{AssetID: "a", SHA256: strings.Repeat("a", 64)}},
			{ID: "image-b", SceneID: "scene-plan", Kind: MultiEntityKindImage, StartMS: 2200, EndMS: 5100, AssetRef: &capmodel.OverlayAssetRef{AssetID: "b", SHA256: strings.Repeat("b", 64)}},
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
	if len(plan.Items) != 1 {
		t.Fatalf("group plan emitted %d items, want one sequenced image group", len(plan.Items))
	}
	item := plan.Items[0]
	if item.Kind != string(capmodel.KindEntityImage) || len(item.ImageLayers) != 2 || item.GroupMotionID == "" {
		t.Fatalf("group plan did not preserve its sequenced image group: %+v", item)
	}
	if item.CameraMotionID != "" || item.MotionID != "" {
		t.Fatalf("group plan must have one owner: camera=%q parent=%q", item.CameraMotionID, item.MotionID)
	}
	if !AnimationSelectable(item.GroupMotionID, "image.group.count_2") {
		t.Fatalf("group motion %q is not certified for a two-image composite", item.GroupMotionID)
	}
	for _, layer := range item.ImageLayers {
		if layer.MotionID != "" || layer.StartMS != 0 || layer.EndMS != 5000 {
			t.Fatalf("group-owned layer %q must be motion-free on the shared window: %+v", layer.ID, layer)
		}
	}
	if EditorialSectionForItem(item) != EditorialSectionImageStack {
		t.Fatalf("image editorial section=%q, want %q", EditorialSectionForItem(item), EditorialSectionImageStack)
	}
}

func TestWireMultiEntityOverlaysSeparatesMixedContentIntoExistingKinds(t *testing.T) {
	group := MultiEntityGroup{
		GroupID: "mixed-group", SceneID: "scene-mixed", GroupType: MultiEntityKindMixed,
		Count: 2, StartMS: 0, EndMS: 5000,
		Items: []MultiEntityCandidate{
			{ID: "photo", SceneID: "scene-mixed", Kind: MultiEntityKindImage, Name: "Ada", StartMS: 0, EndMS: 2500, AssetRef: &capmodel.OverlayAssetRef{AssetID: "portrait", SHA256: strings.Repeat("a", 64)}},
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
	if items[0].Kind != string(capmodel.KindEntityImage) || items[1].Kind != "text_phrase" || items[0].EntityCaption != "Ada" {
		t.Fatalf("mixed group kinds/caption = %q/%q/%q, want entity_image with caption + canonical text_phrase", items[0].Kind, items[1].Kind, items[0].EntityCaption)
	}
	if EditorialSectionForItem(items[0]) != EditorialSectionEntityTextImage || EditorialSectionForItem(items[1]) != EditorialSectionShortPhrase {
		t.Fatalf("mixed group items did not join their editorial sections: %+v", items)
	}
}

// Group choreography is seeded by group identity: the same group plans
// bit-identically on retries, while distinct groups draw different entrances
// from the certified image.group.count_N pool.
func TestBuildPlanGroupMotionsSeededByGroup(t *testing.T) {
	mkGroup := func(groupID string) MultiEntityGroup {
		return MultiEntityGroup{
			GroupID: groupID, SceneID: "scene-1", StartMS: 0, EndMS: 5000,
			Items: []MultiEntityCandidate{
				{ID: "a", Kind: MultiEntityKindImage, StartMS: 0, EndMS: 4000, AssetRef: &capmodel.OverlayAssetRef{AssetID: "img-a", SHA256: strings.Repeat("a", 64)}, Name: "Ada"},
				{ID: "b", Kind: MultiEntityKindImage, StartMS: 0, EndMS: 4000, AssetRef: &capmodel.OverlayAssetRef{AssetID: "img-b", SHA256: strings.Repeat("b", 64)}, Name: "Grace"},
			},
		}
	}
	planFor := func(groupID string) capmodel.OverlayItem {
		t.Helper()
		group := mkGroup(groupID)
		scenes := []SceneInput{{ID: "scene-1", EndUS: 5_000_000, MultiEntityGroups: []MultiEntityGroup{group}}}
		plan, err := BuildPlan(PlanInput{
			PlanID: "plan-" + groupID, VideoID: "video", MotionSeed: "group-seed",
			Width: 1280, Height: 720, FPSNum: 24, FPSDen: 1, Scenes: scenes,
		}, AllCandidatesPlannerConfig(scenes))
		if err != nil {
			t.Fatalf("plan %s: %v", groupID, err)
		}
		if len(plan.Items) != 1 || len(plan.Items[0].ImageLayers) != 2 {
			t.Fatalf("plan %s = %+v, want one two-image composite", groupID, plan.Items)
		}
		item := plan.Items[0]
		if item.GroupMotionID == "" || item.CameraMotionID != "" || item.MotionID != "" {
			t.Fatalf("plan %s owners = group:%q camera:%q parent:%q", groupID, item.GroupMotionID, item.CameraMotionID, item.MotionID)
		}
		if !AnimationSelectable(item.GroupMotionID, "image.group.count_2") {
			t.Fatalf("plan %s group motion %q is not certified", groupID, item.GroupMotionID)
		}
		for _, layer := range item.ImageLayers {
			if layer.MotionID != "" {
				t.Fatalf("plan %s layer %q has a competing motion %q", groupID, layer.ID, layer.MotionID)
			}
			if layer.CaptionMotionID == "" {
				t.Fatalf("plan %s layer %q lost its caption entrance", groupID, layer.ID)
			}
		}
		return item
	}
	first, again := planFor("group-1"), planFor("group-1")
	if first.GroupMotionID != again.GroupMotionID {
		t.Fatalf("same group selected %q then %q", first.GroupMotionID, again.GroupMotionID)
	}
	seen := map[string]bool{}
	for i := 1; i <= 8; i++ {
		seen[planFor(fmt.Sprintf("group-%d", i)).GroupMotionID] = true
	}
	if len(seen) < 2 {
		t.Errorf("eight distinct groups share one group motion %v: group seed has no effect", seen)
	}
}
