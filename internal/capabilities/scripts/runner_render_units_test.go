package scriptgeneration

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// runner_render_units_test.go pins the stock and clip visual contracts: stock
// media remains source footage, while selected clips use the local render lane.

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

// TestSceneRenderSpec_BurnsCaptionsOnClipOnlyMainClips pins the per-clip
// subtitle contract: in a clip_only run the visual is the clip itself (a
// rendered card such as cliprender_*), not a yt_ library clip, and it must
// still inherit the requested caption burn. Treating only yt_ clips as source
// footage silently delivered un-subtitled main clips.
func TestSceneRenderSpec_BurnsCaptionsOnClipOnlyMainClips(t *testing.T) {
	req := GenerateRequest{
		MediaMode: scriptpkg.MediaMode("clip_only"),
		Render: scriptpkg.VideoRenderSpec{
			Subtitles: &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"},
		},
	}
	scene := Scene{ID: "scene-main", Clip: visualClip("cliprender_abc")}
	spec := sceneRenderSpec(req, scene)
	if spec.Subtitles == nil || !spec.Subtitles.Enabled {
		t.Fatal("a clip_only main clip must keep the requested caption burn")
	}
}

// TestSceneRenderSpec_StockVisualNeverInheritsCaptions keeps the other half:
// a stock-owned visual (or a stock_only body scene) never burns narration
// captions onto the stock plate, because those captions belong to the clips.
func TestSceneRenderSpec_StockVisualIsUntouched(t *testing.T) {
	req := GenerateRequest{
		MediaMode: scriptpkg.MediaMode("stock_only"),
		Render: scriptpkg.VideoRenderSpec{
			Subtitles: &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"},
		},
	}
	for _, scene := range []Scene{
		{ID: "scene-stock-folder", Clip: visualClip("cliprender_abc"), Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}},
		{ID: "scene-body", Clip: visualClip("cliprender_abc")},
	} {
		spec := sceneRenderSpec(req, scene)
		if spec.Subtitles != nil && spec.Subtitles.Enabled {
			t.Fatalf("scene %q must not burn captions onto a stock visual", scene.ID)
		}
	}
	narrated := Scene{ID: "scene-stock-vo", Stock: &scriptpkg.StockBinding{AssetID: "stock-asset"}, Voiceover: map[Language]AudioReference{"en": {ID: "vo-1"}}}
	if spec := sceneRenderSpec(req, narrated); spec.Subtitles != nil && spec.Subtitles.Enabled {
		t.Fatal("narrated stock visual must bypass subtitle rendering")
	}
}

// TestFixedMediaKeepsRequestedVisualTreatment pins the r15c regression: an
// intro/outro clip without narration still has to be rendered when the payload
// asks for a card treatment (frame, grid, watermark). Skipping it shipped the
// raw source to the final assembly, so the foreground grid never reached the
// delivered video even though the clip contract carried it.
func TestFixedMediaKeepsRequestedVisualTreatment(t *testing.T) {
	treatment := scriptpkg.VideoRenderSpec{
		Enabled: true,
		Subtitles: &scriptpkg.VideoSubtitlesSpec{
			Enabled: true, Mode: "burn",
		},
		Grid: &scriptpkg.VideoClipGridSpec{
			Scope: "foreground", Style: "lines", SpacingPX: 96, LineWidthPX: 1, Color: "#7FA8C8", Opacity: 0.08,
		},
		Watermark:              &scriptpkg.VideoWatermarkSpec{Enabled: true, Text: "OBAMA • ARCHIVIO"},
		ForegroundScalePercent: 75,
	}
	req := GenerateRequest{SourceLanguage: "it", Render: treatment}
	intro := Scene{
		ID: "intro-01", ExecutionMode: scriptpkg.SceneExecutionFixedMedia,
		Role: scriptpkg.SceneRoleOpening, Text: map[Language]string{"it": ""},
	}
	if !fixedMediaNeedsLocalizedRender(req, intro) {
		t.Fatal("a captionless intro with a requested card treatment must use the local render lane")
	}
	spec := sceneRenderSpec(req, intro)
	if spec.Grid == nil || spec.Grid.Scope != "foreground" {
		t.Fatalf("fixed-media render spec lost the foreground grid: %+v", spec.Grid)
	}
	if spec.Watermark == nil || !spec.Watermark.Enabled {
		t.Fatalf("fixed-media render spec lost the watermark: %+v", spec.Watermark)
	}
	if spec.ForegroundScalePercent != 75 {
		t.Fatalf("foreground scale = %d, want 75", spec.ForegroundScalePercent)
	}
	if spec.Subtitles != nil && spec.Subtitles.Enabled {
		t.Fatal("an empty fixed-media caption must not enable subtitle burn")
	}

	// The caption-only default is unchanged: no caption and no visual treatment
	// stays on the native clip, never on the localized render lane.
	plain := GenerateRequest{SourceLanguage: "it", Render: scriptpkg.VideoRenderSpec{
		Enabled: true, Subtitles: &scriptpkg.VideoSubtitlesSpec{Enabled: true, Mode: "burn"},
	}}
	if fixedMediaNeedsLocalizedRender(plain, intro) {
		t.Fatal("a captionless intro without treatment must stay native")
	}

	// A captioned intro keeps its subtitle variant and the treatment.
	captioned := intro
	captioned.Text = map[Language]string{"it": "Barack Obama, 1961"}
	if !fixedMediaNeedsLocalizedRender(req, captioned) {
		t.Fatal("a captioned intro must use the localized subtitle render lane")
	}
	if spec := sceneRenderSpec(req, captioned); spec.Subtitles == nil || !spec.Subtitles.Enabled {
		t.Fatal("a captioned intro must keep the requested subtitle burn")
	}
}

func TestRenderUnitCount_ExcludesAllStockScenes(t *testing.T) {
	scenes := []Scene{
		{ID: "s-visual", Clip: visualClip("a")},
		{ID: "s-stock", Clips: []*ClipReference{stockClip("b")}},
		{ID: "s-mixed", Clips: []*ClipReference{stockClip("c"), visualClip("d")}},
		{ID: "s-bound-stock", Stock: &scriptpkg.StockBinding{AssetID: "stock-asset", DriveLink: "https://drive.test/stock"}},
	}
	if got := RenderUnitCount(scenes); got != 2 {
		t.Fatalf("RenderUnitCount = %d, want 2 (visual + mixed-visual; stock binding excluded)", got)
	}
}

func TestRenderUnitsForScene_ConcreteStockBindingBypassesChronon(t *testing.T) {
	scene := Scene{ID: "stock-vo", Stock: &scriptpkg.StockBinding{
		AssetID: "yt_stock_asset", Name: "shared stock", DriveLink: "https://drive.test/stock", DurationMs: 60_000,
	}}
	units := RenderUnitsForScene(scene)
	if len(units) != 0 {
		t.Fatalf("stock visual units = %+v, want none; stock must bypass Chronon", units)
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
