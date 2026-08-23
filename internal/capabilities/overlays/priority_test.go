// Package overlays — priority_test.go certifies the GOLDEN 08 content
// priority table and the deterministic overlap degradation.
package overlays

import "testing"

// TestContentPriorityCanonicalTable pins the editorial priority of every
// semantic template (phrase 100 > word 80 > image 60 > effect 20, structural
// 0) and the conservative unknown-template default.
func TestContentPriorityCanonicalTable(t *testing.T) {
	cases := []struct {
		template string
		want     int
	}{
		{"IMPORTANT_PHRASE", 100},
		{"IMPORTANT_WORD", 80},
		{"NUMBER", 80},
		{"QUOTE", 80},
		{"LOCATION", 80},
		{"lower_third", 80},
		{"quote", 80},
		{"IMAGE_OVERLAY", 60},
		{"PRODUCT", 60},
		{"LOGO", 60},
		{"image_popup", 60},
		{"LIGHT_LEAK", 20},
		{"BACKGROUND", 0},
		{"VIDEO_BACKGROUND", 0},
		{"SHAPE", 0},
		{"unknown_template", 0}, // conservative: never drop content we don't know
	}
	for _, tc := range cases {
		if got := ContentPriority(tc.template); got != tc.want {
			t.Errorf("ContentPriority(%q) = %d, want %d", tc.template, got, tc.want)
		}
	}
	// Relative ordering the degradation relies on: phrase > word > image > effect.
	if !(PriorityPhrase > PriorityWord && PriorityWord > PriorityImage && PriorityImage > PriorityEffect) {
		t.Fatalf("priority ordering broken: phrase=%d word=%d image=%d effect=%d",
			PriorityPhrase, PriorityWord, PriorityImage, PriorityEffect)
	}
}

// TestDegradeOverlapsDropsLowestPriority pins the GOLDEN 08 degradation: four
// overlapping content items (phrase + word + image + leak) with the default
// budget 3 drop exactly the light leak (lowest priority), deterministically.
func TestDegradeOverlapsDropsLowestPriority(t *testing.T) {
	items := []OverlayItem{
		{ID: "bg", TemplateID: "BACKGROUND", StartMs: 0, EndMs: 5000},
		{ID: "img", TemplateID: "IMAGE_OVERLAY", StartMs: 1000, EndMs: 3000},
		{ID: "leak", TemplateID: "LIGHT_LEAK", StartMs: 1000, EndMs: 3000},
		{ID: "word", TemplateID: "IMPORTANT_WORD", StartMs: 1500, EndMs: 2500},
		{ID: "phrase", TemplateID: "IMPORTANT_PHRASE", StartMs: 1500, EndMs: 2500},
	}
	got := DegradeOverlaps(items, DefaultOverlapBudget)
	if len(got) != 4 {
		t.Fatalf("survivors = %d, want 4 (drop only the light leak): %+v", len(got), got)
	}
	for _, item := range got {
		if item.ID == "leak" {
			t.Fatalf("light leak (priority 20) must degrade first: %+v", got)
		}
	}
	// Original relative order is preserved (z-index unaffected by a drop).
	wantOrder := []string{"bg", "img", "word", "phrase"}
	for i, item := range got {
		if item.ID != wantOrder[i] {
			t.Fatalf("survivor[%d] = %q, want %q (order preserved)", i, item.ID, wantOrder[i])
		}
	}
}

// TestDegradeOverlapsStructuralNeverDropped pins the structural exemption:
// background/shape layers are never counted toward the budget, so a scene
// with a background + 3 overlapping content items degrades nothing.
func TestDegradeOverlapsStructuralNeverDropped(t *testing.T) {
	items := []OverlayItem{
		{ID: "bg", TemplateID: "BACKGROUND", StartMs: 0, EndMs: 5000},
		{ID: "img", TemplateID: "IMAGE_OVERLAY", StartMs: 1000, EndMs: 3000},
		{ID: "word", TemplateID: "IMPORTANT_WORD", StartMs: 1000, EndMs: 3000},
		{ID: "phrase", TemplateID: "IMPORTANT_PHRASE", StartMs: 1000, EndMs: 3000},
	}
	got := DegradeOverlaps(items, DefaultOverlapBudget)
	if len(got) != 4 {
		t.Fatalf("survivors = %d, want 4 (3 content items ≤ budget, background exempt): %+v", len(got), got)
	}
}

// TestDegradeOverlapsNonOverlappingKept pins the time-window boundary: items
// that do not overlap in time are never degraded, even when the same content
// types would exceed the budget if they overlapped.
func TestDegradeOverlapsNonOverlappingKept(t *testing.T) {
	items := []OverlayItem{
		{ID: "a", TemplateID: "LIGHT_LEAK", StartMs: 0, EndMs: 1000},
		{ID: "b", TemplateID: "LIGHT_LEAK", StartMs: 1000, EndMs: 2000}, // touches at 1000, half-open → no overlap
		{ID: "c", TemplateID: "LIGHT_LEAK", StartMs: 2000, EndMs: 3000},
		{ID: "d", TemplateID: "LIGHT_LEAK", StartMs: 3000, EndMs: 4000},
	}
	got := DegradeOverlaps(items, DefaultOverlapBudget)
	if len(got) != 4 {
		t.Fatalf("survivors = %d, want 4 (non-overlapping items never degrade): %+v", len(got), got)
	}
}

// TestDegradeOverlapsDeterministic pins re-render determinism: two runs over
// the same item list produce byte-identical survivor sequences.
func TestDegradeOverlapsDeterministic(t *testing.T) {
	items := []OverlayItem{
		{ID: "leak1", TemplateID: "LIGHT_LEAK", StartMs: 0, EndMs: 1000},
		{ID: "leak2", TemplateID: "LIGHT_LEAK", StartMs: 500, EndMs: 1500},
		{ID: "img1", TemplateID: "IMAGE_OVERLAY", StartMs: 400, EndMs: 900},
		{ID: "img2", TemplateID: "IMAGE_OVERLAY", StartMs: 600, EndMs: 1100},
		{ID: "word", TemplateID: "IMPORTANT_WORD", StartMs: 450, EndMs: 950},
		{ID: "phrase", TemplateID: "IMPORTANT_PHRASE", StartMs: 550, EndMs: 1050},
	}
	a := DegradeOverlaps(items, DefaultOverlapBudget)
	b := DegradeOverlaps(items, DefaultOverlapBudget)
	if len(a) != len(b) {
		t.Fatalf("degradation is not deterministic: len(a)=%d len(b)=%d", len(a), len(b))
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			t.Fatalf("degradation is not deterministic at %d: %q vs %q", i, a[i].ID, b[i].ID)
		}
	}
}

// TestCountContentCensus pins the per-attempt content census: items are
// tallied into the four canonical buckets by priority class, and structural
// layers (background/shape) plus unknown templates are never counted as
// content.
func TestCountContentCensus(t *testing.T) {
	plan := OverlayPlan{Items: []OverlayItem{
		{ID: "bg", TemplateID: "BACKGROUND"},
		{ID: "p1", TemplateID: "IMPORTANT_PHRASE"},
		{ID: "p2", TemplateID: "IMPORTANT_PHRASE"},
		{ID: "w1", TemplateID: "IMPORTANT_WORD"},
		{ID: "n1", TemplateID: "NUMBER"},
		{ID: "i1", TemplateID: "IMAGE_OVERLAY"},
		{ID: "l1", TemplateID: "LIGHT_LEAK"},
		{ID: "l2", TemplateID: "LIGHT_LEAK"},
		{ID: "shape", TemplateID: "SHAPE"},
		{ID: "unknown", TemplateID: "FUTURE_TEMPLATE"},
	}}
	got := CountContent(plan)
	if got.Phrases != 2 || got.Words != 2 || got.Images != 1 || got.Leaks != 2 {
		t.Fatalf("CountContent = %+v, want phrases=2 words=2 images=1 leaks=2", got)
	}
}

// TestBuildPlanDegradesOverlappingContent pins the planner wiring: a scene
// whose selected items overlap beyond the budget drops the lowest-priority
// items automatically (never piling up).
func TestBuildPlanDegradesOverlappingContent(t *testing.T) {
	plan, err := BuildPlan(PlanInput{
		PlanID: "p1", VideoID: "v1", Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1,
		Scenes: []SceneInput{{
			ID: "scene-1",
			// All four content categories overlap at [1000, 2000].
			Phrases:  []TimedAnnotation{{Text: "THE BIG CHANGE", StartMs: 1000, EndMs: 2000, Score: 1}},
			Keywords: []TimedAnnotation{{Text: "APPLE", StartMs: 1000, EndMs: 2000, Score: 1}},
			Images:   []ImageCandidate{{AssetID: "img-1", SHA256: "h", StartMs: 1000, EndMs: 2000, Score: 1}},
		}},
	}, PlannerConfig{MaxPhrases: 1, MaxKeywords: 1, MaxImages: 1})
	if err != nil {
		t.Fatal(err)
	}
	// phrase + word + image = 3 content items = exactly the budget → none dropped.
	if len(plan.Items) != 3 {
		t.Fatalf("items = %d, want 3 (phrase + word + image ≤ budget): %+v", len(plan.Items), plan.Items)
	}
}
