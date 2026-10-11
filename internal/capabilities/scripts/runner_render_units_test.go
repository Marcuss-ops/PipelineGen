package scriptgeneration

import "testing"

// runner_render_units_test.go pins the used-as-stock render contract
// (payload script_params.segments[].stock_clip_ids): a scene whose clips are
// ALL stock-marked is audio-only — no localized clip render is ever produced
// for it, because its video comes from its stock binding. A scene that still
// owns a visual clip keeps the historical single-unit fan-out on that clip.

func stockClip(id string) *ClipReference {
	return &ClipReference{ID: id, AsStock: true, DurationUS: 5_000_000}
}

func visualClip(id string) *ClipReference {
	return &ClipReference{ID: id, DurationUS: 5_000_000}
}

func TestSceneVisualClipsAreStockOnly_RequiresAllClipsStockMarked(t *testing.T) {
	if sceneVisualClipsAreStockOnly(Scene{ID: "s-empty"}) {
		t.Fatal("a scene with no clips is the subtitle-only lane, not stock-only")
	}
	if !sceneVisualClipsAreStockOnly(Scene{ID: "s-all", Clips: []*ClipReference{stockClip("a"), stockClip("b")}}) {
		t.Fatal("a scene with every clip stock-marked must be stock-only")
	}
	if sceneVisualClipsAreStockOnly(Scene{ID: "s-mixed", Clips: []*ClipReference{stockClip("a"), visualClip("b")}}) {
		t.Fatal("a scene keeping one visual clip must not be stock-only")
	}
	if sceneVisualClipsAreStockOnly(Scene{ID: "s-visual", Clip: visualClip("a")}) {
		t.Fatal("a plain visual scene must not be stock-only")
	}
}

func TestPrimaryVisualClip_SkipsStockMarkedClips(t *testing.T) {
	got := primaryVisualClip(Scene{ID: "s", Clips: []*ClipReference{stockClip("stock-a"), visualClip("visual-b")}})
	if got == nil || got.ID != "visual-b" {
		t.Fatalf("primary visual clip = %+v, want visual-b", got)
	}
	if got := primaryVisualClip(Scene{ID: "s", Clips: []*ClipReference{stockClip("a")}}); got != nil {
		t.Fatalf("all-stock scene has no visual clip, got %+v", got)
	}
}

func TestRenderUnitsForScene_AllStockClipsProduceNoUnits(t *testing.T) {
	scene := Scene{ID: "scene-stock", Clips: []*ClipReference{stockClip("a"), stockClip("b")}}
	if units := RenderUnitsForScene(scene); len(units) != 0 {
		t.Fatalf("all-stock scene produced %d render units, want none", len(units))
	}
	legacy := Scene{ID: "scene-stock-legacy", Clip: stockClip("a")}
	if units := RenderUnitsForScene(legacy); len(units) != 0 {
		t.Fatalf("all-stock legacy-alias scene produced %d render units, want none", len(units))
	}
}

func TestRenderUnitsForScene_MixedSceneRendersOnlyVisualClip(t *testing.T) {
	scene := Scene{ID: "scene-mixed", Clips: []*ClipReference{stockClip("stock-a"), visualClip("visual-b")}}
	units := RenderUnitsForScene(scene)
	if len(units) != 1 {
		t.Fatalf("mixed scene produced %d render units, want exactly one", len(units))
	}
	if units[0].Clip == nil || units[0].Clip.ID != "visual-b" {
		t.Fatalf("mixed scene unit must ride the visual clip, got %+v", units[0].Clip)
	}
}

func TestRenderUnitsForScene_VisualSceneKeepsHistoricalFanout(t *testing.T) {
	scene := Scene{ID: "scene-visual", Clip: visualClip("a")}
	units := RenderUnitsForScene(scene)
	if len(units) != 1 || units[0].Clip == nil || units[0].Clip.ID != "a" {
		t.Fatalf("visual scene must keep the single-unit fan-out, got %+v", units)
	}
	if units := RenderUnitsForScene(Scene{ID: "scene-bare"}); len(units) != 0 {
		t.Fatalf("clip-less scene must produce no units, got %+v", units)
	}
}

func TestRenderUnitCount_ExcludesAllStockScenes(t *testing.T) {
	scenes := []Scene{
		{ID: "s-visual", Clip: visualClip("a")},
		{ID: "s-stock", Clips: []*ClipReference{stockClip("b")}},
		{ID: "s-mixed", Clips: []*ClipReference{stockClip("c"), visualClip("d")}},
	}
	if got := RenderUnitCount(scenes); got != 2 {
		t.Fatalf("RenderUnitCount = %d, want 2 (visual + mixed-visual)", got)
	}
}

func TestSceneStockClipIDs_CollectsOnlyMarkedClips(t *testing.T) {
	scene := Scene{ID: "s", Clips: []*ClipReference{stockClip("a"), visualClip("b"), nil}}
	ids := sceneStockClipIDs(scene)
	if len(ids) != 1 {
		t.Fatalf("stock clip ids = %v, want only {a}", ids)
	}
	if _, ok := ids["a"]; !ok {
		t.Fatalf("stock clip ids = %v, want only {a}", ids)
	}
	if ids := sceneStockClipIDs(Scene{ID: "s"}); len(ids) != 0 {
		t.Fatalf("clip-less scene stock ids = %v, want empty", ids)
	}
}
