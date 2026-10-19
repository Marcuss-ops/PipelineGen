package scriptgeneration

import (
	"testing"

	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func prefilterSceneInput() *capabilityoverlay.SceneInput {
	mapCandidate, ok := capabilityoverlay.NewMapCandidate("gpe:garlasco", "Garlasco", 45.0, 9.0, 1_000_000, 2_000_000, 0.9, "")
	if !ok {
		panic("map candidate fixture invalid")
	}
	return &capabilityoverlay.SceneInput{ID: "scene-0",
		Phrases:    []capabilityoverlay.TimedAnnotation{{Text: "some grounded phrase"}},
		Numbers:    []capabilityoverlay.TimedAnnotation{{Text: "13 agosto 2007", Type: "DATE"}},
		Images:     []capabilityoverlay.ImageCandidate{{AssetID: "image-a"}},
		Products:   []capabilityoverlay.ImageCandidate{{AssetID: "product-a"}},
		Logos:      []capabilityoverlay.ImageCandidate{{AssetID: "logo-a"}},
		Maps:       []capabilityoverlay.MapCandidate{mapCandidate},
		BrandTexts: []capabilityoverlay.TimedAnnotation{{Text: "brand"}},
		Quotes:     []capabilityoverlay.TimedAnnotation{{Text: "quote"}},
	}
}

// Disabled lanes must not reach the planner: the run-level budgets inside
// BuildPlan dedupe globally, so a disabled scene's candidates would evict an
// enabled scene's identical or overlapping surfaces before the end category
// filter can remove them. Quotes/keywords intentionally pass through — the
// internal editorial budget never retains them.
func TestDropCategoryDisabledCandidatesMatchesEndFilter(t *testing.T) {
	canvas := OverlayCanvasSpec{OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
		scriptpkg.OverlayCategoryPhrases:   scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryNumbers:   scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryCharts:    scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryImages:    scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryMaps:      scriptpkg.ToggleDisabled,
		scriptpkg.OverlayCategoryBrandText: scriptpkg.ToggleDisabled,
	}}
	got := dropCategoryDisabledCandidates(prefilterSceneInput(), "scene-0", 0, canvas)
	if got == nil {
		t.Fatal("quotes-only scene input must survive the pre-filter")
	}
	if len(got.Phrases) != 0 || len(got.Numbers) != 0 || len(got.Images) != 0 ||
		len(got.Products) != 0 || len(got.Logos) != 0 || len(got.Maps) != 0 || len(got.BrandTexts) != 0 {
		t.Fatalf("disabled lanes survived: %+v", got)
	}
	if len(got.Quotes) != 1 {
		t.Fatalf("quotes must pass the pre-filter untouched, got %+v", got.Quotes)
	}
}

// Numbers survive the pre-filter when charts are enabled even if the numbers
// lane is disabled: chart placements derive from grounded numbers, while the
// number cards themselves still die at the end category filter.
func TestDropCategoryDisabledCandidatesKeepsNumbersForCharts(t *testing.T) {
	canvas := OverlayCanvasSpec{
		EnableCharts: true,
		OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryNumbers: scriptpkg.ToggleDisabled,
			scriptpkg.OverlayCategoryCharts:  scriptpkg.ToggleEnabled,
		},
	}
	got := dropCategoryDisabledCandidates(prefilterSceneInput(), "scene-0", 0, canvas)
	if got == nil || len(got.Numbers) != 1 {
		t.Fatalf("numbers must survive for chart derivation, got %+v", got)
	}
}

func TestDropCategoryDisabledCandidatesKeepsEnabledScenes(t *testing.T) {
	got := dropCategoryDisabledCandidates(prefilterSceneInput(), "scene-0", 0, OverlayCanvasSpec{})
	if got == nil {
		t.Fatal("enabled scene input must survive untouched")
	}
	if len(got.Phrases) != 1 || len(got.Numbers) != 1 || len(got.Images) != 1 ||
		len(got.Products) != 1 || len(got.Logos) != 1 || len(got.Maps) != 1 ||
		len(got.BrandTexts) != 1 || len(got.Quotes) != 1 {
		t.Fatalf("enabled lanes dropped: %+v", got)
	}
	if dropCategoryDisabledCandidates(nil, "scene-0", 0, OverlayCanvasSpec{}) != nil {
		t.Fatal("nil scene input must stay nil")
	}
	quotesOnly := &capabilityoverlay.SceneInput{ID: "scene-0",
		Quotes: []capabilityoverlay.TimedAnnotation{{Text: "quote"}}}
	// Quotes/keywords are intentionally not pre-filtered even when their
	// lane is disabled: the internal editorial budget never retains them,
	// so they cannot evict anything downstream.
	if got := dropCategoryDisabledCandidates(quotesOnly, "scene-0", 0, OverlayCanvasSpec{
		OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryQuotes: scriptpkg.ToggleDisabled,
		},
	}); got == nil || len(got.Quotes) != 1 {
		t.Fatalf("quotes must pass the pre-filter untouched, got %+v", got)
	}
}

// Regression for the R11 date shortfall (2/4): every scene except
// date-e-reperti disables numbers, so their identical date surfaces must not
// enter the planner where the run-wide number text-dedupe would evict the
// enabled scene's card before the end filter removes the disabled copies.
func TestDisabledSceneNumbersDoNotEvictEnabledDates(t *testing.T) {
	date := func(text string, startMS int64) capabilityoverlay.TimedAnnotation {
		return capabilityoverlay.TimedAnnotation{
			Text: text, Type: "DATE",
			StartMs: startMS, EndMs: startMS + 400,
			StartUS: startMS * 1000, DurationUS: 400_000, Score: 0.8,
		}
	}
	delitto := &capabilityoverlay.SceneInput{ID: "delitto-e-luogo", Numbers: []capabilityoverlay.TimedAnnotation{
		date("13 agosto 2007", 4400),
	}}
	reperti := &capabilityoverlay.SceneInput{ID: "date-e-reperti", Numbers: []capabilityoverlay.TimedAnnotation{
		date("13 agosto 2007", 4400),
		date("2015", 6000),
		date("28 settembre 2026", 9500),
		date("8 ottobre 2026", 13500),
	}}
	canvas := OverlayCanvasSpec{
		OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
			scriptpkg.OverlayCategoryNumbers: scriptpkg.ToggleEnabled,
		},
		OverlayCategorySegments: []scriptpkg.ScriptSegment{
			{ID: "delitto-e-luogo", OverlayCategories: map[scriptpkg.OverlayCategory]scriptpkg.Toggle{
				scriptpkg.OverlayCategoryNumbers: scriptpkg.ToggleDisabled,
			}},
		},
	}
	scenes := make([]capabilityoverlay.SceneInput, 0, 2)
	for _, in := range []*capabilityoverlay.SceneInput{delitto, reperti} {
		if filtered := dropCategoryDisabledCandidates(in, in.ID, 0, canvas); filtered != nil {
			scenes = append(scenes, *filtered)
		}
	}
	if len(scenes) != 1 || scenes[0].ID != "date-e-reperti" {
		t.Fatalf("scenes reaching the planner = %v, want only date-e-reperti", scenes)
	}
	config := capabilityoverlay.AllCandidatesPlannerConfig(scenes)
	plan, err := capabilityoverlay.BuildPlan(capabilityoverlay.PlanInput{
		PlanID: "date-regression", VideoID: "video-date-regression",
		Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, Scenes: scenes,
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	dates := map[string]bool{}
	for _, item := range plan.Items {
		if item.Kind == "number" && item.TemplateID == "TIMELINE_DATE_CARD" {
			dates[item.Text] = true
		}
	}
	for _, want := range []string{"13 agosto 2007", "2015", "28 settembre 2026", "8 ottobre 2026"} {
		if !dates[want] {
			t.Fatalf("planned dates = %v, want %q to survive", dates, want)
		}
	}
}
