package overlays

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strings"
	"testing"
)

func dateMotionProbeInput(startsMS []int64) PlanInput {
	numbers := make([]TimedAnnotation, 0, len(startsMS))
	for index, start := range startsMS {
		numbers = append(numbers, TimedAnnotation{
			Text:       fmt.Sprintf("%d giugno 200%d", index+1, index),
			Type:       "DATE",
			StartMs:    start,
			EndMs:      start + 1_500,
			StartUS:    start * 1_000,
			DurationUS: 1_500_000,
			Score:      1 - float64(index)*0.01,
		})
	}
	return PlanInput{
		PlanID: "date-motion-probe", VideoID: "date-motion-probe",
		Width: 1920, Height: 1080, FPSNum: 24, FPSDen: 1,
		AnimationCounts: map[string]int{"dates": len(numbers)},
		Scenes: []SceneInput{{
			ID: "dates", EndUS: 30_000_000, Numbers: numbers,
		}},
	}
}

func TestDateMotionFamilyTypewriterUsesTenCertifiedPresets(t *testing.T) {
	candidates := DateMotionFamilyCandidates("typewriter")
	if len(candidates) != 10 {
		t.Fatalf("typewriter date family = %d motions, want 10: %v", len(candidates), candidates)
	}
	for _, id := range candidates {
		if !strings.HasPrefix(id, "date_typewriter_") {
			t.Errorf("typewriter date family contains %q", id)
		}
	}
	if got := DateMotionFamilyCandidates("auto"); len(got) != len(DatePresentationMotionCandidates()) {
		t.Fatalf("auto date family = %d motions, want the full %d", len(got), len(DatePresentationMotionCandidates()))
	}
}

func TestDateMotionFamilyTypewriterRotatesAllTen(t *testing.T) {
	input := dateMotionProbeInput([]int64{1_607, 7_607, 13_607, 19_607, 25_607, 31_607, 37_607, 43_607, 49_607, 55_607})
	input.DateMotionFamily = "typewriter"
	input.AnimationCounts = map[string]int{"dates": 10}
	input.Scenes[0].EndUS = 90_000_000
	plan, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes))
	if err != nil {
		t.Fatal(err)
	}
	plan.Items = PrepareCompiledAnimationLayout(plan.Items, input, 24, 1)
	seen := map[string]bool{}
	cards := 0
	for _, item := range plan.Items {
		if item.TemplateID != "TIMELINE_DATE_CARD" {
			continue
		}
		cards++
		seen[item.MotionID] = true
		if !strings.HasPrefix(item.MotionID, "date_typewriter_") {
			t.Errorf("date card motion %q is outside the typewriter family", item.MotionID)
		}
	}
	if cards != 10 {
		t.Fatalf("date cards = %d, want 10", cards)
	}
	if len(seen) != 10 {
		t.Fatalf("typewriter date motions = %d, want 10: %v", len(seen), seen)
	}
}

func TestDateMotionFamilyUnknownFailsClosed(t *testing.T) {
	input := dateMotionProbeInput([]int64{1_607})
	input.DateMotionFamily = "definitely_not_a_date_family"
	if _, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes)); err == nil {
		t.Fatal("BuildPlan accepted an unknown date motion family")
	}
}

func TestDateMotionProbeSpacedDates(t *testing.T) {
	input := dateMotionProbeInput([]int64{1_607, 4_897, 8_626, 12_591})
	plan, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes))
	if err != nil {
		t.Fatal(err)
	}
	plan.Items = PrepareCompiledAnimationLayout(plan.Items, input, 24, 1)
	seen := map[string]bool{}
	for _, item := range plan.Items {
		if item.TemplateID != "TIMELINE_DATE_CARD" {
			continue
		}
		t.Logf("spaced date %q motion=%q start=%d end=%d", item.Text, item.MotionID, item.StartMs, item.EndMs)
		seen[item.MotionID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("spaced dates use %d distinct motions, want 4: %v", len(seen), seen)
	}
}

func TestDateMotionAliasLimitControlsDateLane(t *testing.T) {
	input := dateMotionProbeInput([]int64{1_607, 2_607, 3_607, 4_607})
	// The producer historically sent only "dates"; it must cap the date lane
	// exactly like the legacy "timeline_date" alias.
	input.AnimationCounts = map[string]int{"dates": 1}
	plan, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes))
	if err != nil {
		t.Fatal(err)
	}
	plan.Items = PrepareCompiledAnimationLayout(plan.Items, input, 24, 1)
	seen := map[string]bool{}
	cards := 0
	for _, item := range plan.Items {
		if item.TemplateID != "TIMELINE_DATE_CARD" {
			continue
		}
		cards++
		seen[item.MotionID] = true
	}
	if cards != 4 {
		t.Fatalf("date cards = %d, want 4", cards)
	}
	if len(seen) != 1 {
		t.Fatalf("dates:1 produced %d motions, want 1: %v", len(seen), seen)
	}
}

func TestDateDuplicateRotationUsesShuffledWindow(t *testing.T) {
	const limit = 3
	catalog := DatePresentationMotionCandidates()
	prefix := catalog[:limit]
	var input PlanInput
	var window []string
	for attempt := 0; attempt < 128; attempt++ {
		input = PlanInput{PlanID: fmt.Sprintf("date-rotation-probe-%d", attempt), AnimationCounts: map[string]int{"dates": limit}}
		window = limitedShuffledMotionPool(catalog, limit, input.PlanID+"\x00date_presentation")
		if len(window) == limit && window[0] != prefix[0] && window[0] != catalog[0] {
			break
		}
		window = nil
	}
	if len(window) != limit {
		t.Fatal("could not find a seeded date window distinct from the catalog prefix")
	}
	items := []capmodel.OverlayItem{
		{ID: "date-a", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", MotionID: catalog[0]},
		{ID: "date-b", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", MotionID: catalog[0]},
	}
	EnsureDistinctNumberMotionsPinned(items, input, map[int]bool{0: true})
	if items[1].MotionID != window[0] {
		t.Fatalf("duplicate rotated to %q, want first motion of seeded window %q (catalog prefix %q)", items[1].MotionID, window[0], prefix)
	}
}

func TestDateMotionProbeNearbyDates(t *testing.T) {
	input := dateMotionProbeInput([]int64{1_607, 2_607, 3_607, 4_607})
	plan, err := BuildPlan(input, AllCandidatesPlannerConfig(input.Scenes))
	if err != nil {
		t.Fatal(err)
	}
	plan.Items = PrepareCompiledAnimationLayout(plan.Items, input, 24, 1)
	seen := map[string]bool{}
	cards := make([]capmodel.OverlayItem, 0, 4)
	for _, item := range plan.Items {
		if item.TemplateID != "TIMELINE_DATE_CARD" {
			continue
		}
		cards = append(cards, item)
		t.Logf("nearby date %q motion=%q start=%d end=%d", item.Text, item.MotionID, item.StartMs, item.EndMs)
		seen[item.MotionID] = true
	}
	if len(cards) != 4 {
		t.Fatalf("nearby dates kept=%d, want 4", len(cards))
	}
	if len(seen) != 4 {
		t.Fatalf("nearby dates use %d distinct motions, want 4: %v", len(seen), seen)
	}
	for i := range cards {
		for j := i + 1; j < len(cards); j++ {
			if cards[i].StartMs < cards[j].EndMs && cards[j].StartMs < cards[i].EndMs {
				t.Fatalf("nearby date cards overlap: %q [%d,%d] and %q [%d,%d]", cards[i].Text, cards[i].StartMs, cards[i].EndMs, cards[j].Text, cards[j].StartMs, cards[j].EndMs)
			}
		}
	}
}
