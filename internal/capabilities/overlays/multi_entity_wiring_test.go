package overlays

import (
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
	if err != nil || len(items) != 2 {
		t.Fatalf("WireMultiEntityOverlays() = %d items, err=%v; want two timed image items", len(items), err)
	}
	for i, want := range []struct {
		id, caption string
		start, end  int64
	}{
		{"ent_1", "Entity Alpha", 1000, 2500}, {"ent_2", "Entity Beta", 2800, 4500},
	} {
		item := items[i]
		if item.Kind != string(capmodel.KindEntityImage) || item.TemplateID != "IMAGE_OVERLAY" || len(item.ImageLayers) != 0 {
			t.Errorf("image %d must remain an independent entity_image item: %+v", i, item)
		}
		if item.ID != want.id || item.EntityCaption != want.caption || item.StartMs != want.start || item.EndMs != want.end {
			t.Errorf("image %d lost its own identity/caption/timing: %+v", i, item)
		}
		if item.MotionID == "" || !containsString(CertifiedSingleImageMotions(), item.MotionID) || item.PresetID == "" {
			t.Errorf("image must have its own certified motion and preset: %+v", item)
		}
	}
	if items[0].MotionID == items[1].MotionID {
		t.Errorf("adjacent images should use independent motions: %q", items[0].MotionID)
	}
}

func TestWireMultiEntityAddsSequenceCameraForMultiImageGroups(t *testing.T) {
	group := MultiEntityGroup{
		GroupID: "camera-group", SceneID: "scene-camera", GroupType: MultiEntityKindImage,
		Count: 2, StartMS: 0, EndMS: 5000,
		Items: []MultiEntityCandidate{
			{ID: "a", SceneID: "scene-camera", Kind: MultiEntityKindImage, StartMS: 0, EndMS: 4000, AssetRef: &capmodel.OverlayAssetRef{AssetID: "a", SHA256: strings.Repeat("a", 64)}},
			{ID: "b", SceneID: "scene-camera", Kind: MultiEntityKindImage, StartMS: 0, EndMS: 4000, AssetRef: &capmodel.OverlayAssetRef{AssetID: "b", SHA256: strings.Repeat("b", 64)}},
		},
	}
	items, err := WireMultiEntityOverlays(group, 1280, 720)
	if err != nil {
		t.Fatal(err)
	}
	for i, item := range items {
		if item.CameraMotionID == "" {
			t.Fatalf("image %d lost the sequence camera", i)
		}
		if !AnimationSelectable(item.CameraMotionID, "entity.sequence_camera") {
			t.Fatalf("image %d camera %q is not certified", i, item.CameraMotionID)
		}
	}

	single := group
	single.Items = group.Items[:1]
	single.Count = 1
	single.GroupID = "single-camera"
	singleItems, err := WireMultiEntityOverlays(single, 1280, 720)
	if err != nil {
		t.Fatal(err)
	}
	if singleItems[0].CameraMotionID != "" {
		t.Fatalf("single image received a sequence camera: %q", singleItems[0].CameraMotionID)
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
	if err != nil || len(items) != 5 {
		t.Fatalf("WireMultiEntityOverlays() = %d items, err=%v; want five timed images", len(items), err)
	}
	for i, item := range items {
		if len(item.ImageLayers) != 0 || item.AssetRefs[0].AssetID != string(rune('A'+i)) {
			t.Errorf("image %d should be an independent item, got %+v", i, item)
		}
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
	if len(plan.Items) != 2 {
		t.Fatalf("group plan emitted %d items, want two independently timed images", len(plan.Items))
	}
	for _, item := range plan.Items {
		if item.Kind != string(capmodel.KindEntityImage) || len(item.ImageLayers) != 0 || item.MotionID == "" {
			t.Fatalf("group plan did not preserve independent animated images: %+v", item)
		}
	}
	if EditorialSectionForItem(plan.Items[0]) != EditorialSectionSingleImage {
		t.Fatalf("image editorial section=%q, want %q", EditorialSectionForItem(plan.Items[0]), EditorialSectionSingleImage)
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

// Group motion offsets must be seeded by group identity: rewiring the same
// group is bit-identical (retries), while distinct groups vary (previously a
// hardcoded offset 0 gave every group in every job the same entrances).
func TestWireMultiEntityMotionsSeededByGroup(t *testing.T) {
	mkGroup := func(groupID string) MultiEntityGroup {
		return MultiEntityGroup{
			GroupID: groupID, SceneID: "scene-1", StartMS: 0, EndMS: 5000,
			Items: []MultiEntityCandidate{
				{ID: "a", Kind: MultiEntityKindImage, StartMS: 0, EndMS: 4000, AssetRef: &capmodel.OverlayAssetRef{AssetID: "img-a", SHA256: strings.Repeat("a", 64)}, Name: "Ada"},
				{ID: "b", Kind: MultiEntityKindImage, StartMS: 0, EndMS: 4000, AssetRef: &capmodel.OverlayAssetRef{AssetID: "img-b", SHA256: strings.Repeat("b", 64)}, Name: "Grace"},
			},
		}
	}
	wire := func(groupID string) []capmodel.OverlayItem {
		t.Helper()
		items, err := WireMultiEntityOverlays(mkGroup(groupID), 1280, 720)
		if err != nil {
			t.Fatalf("wire %s: %v", groupID, err)
		}
		if len(items) != 2 || len(items[0].ImageLayers) != 0 || len(items[1].ImageLayers) != 0 {
			t.Fatalf("wire %s = %+v, want two independent image items", groupID, items)
		}
		return items
	}
	first, again := wire("group-1"), wire("group-1")
	for i := range first {
		if first[i].MotionID != again[i].MotionID || first[i].CaptionMotionID != again[i].CaptionMotionID {
			t.Fatalf("rewire not deterministic: %+v vs %+v", first, again)
		}
		if first[i].MotionID == "" || first[i].CaptionMotionID == "" {
			t.Fatalf("empty motion on layer %d: %+v", i, first[i])
		}
	}
	seen := map[string]bool{}
	for _, groupID := range []string{"group-1", "group-2", "group-3", "group-4", "group-5", "group-6", "group-7", "group-8"} {
		for _, layer := range wire(groupID) {
			seen[layer.MotionID] = true
		}
	}
	if len(seen) < 2 {
		t.Errorf("eight distinct groups share one image motion %v: offset seed has no effect", seen)
	}
}
