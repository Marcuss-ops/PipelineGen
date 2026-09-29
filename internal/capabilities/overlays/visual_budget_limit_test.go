package overlays

import (
	"fmt"
	"strings"
	"testing"
)

// TestEffectivePhraseOverlayLimitFallsBackToCertifiedDefault certifies that
// an absent (0) or nonsensical (negative) caller value keeps the certified
// ceiling, while a positive value is honoured verbatim — it may raise or
// lower it.
func TestEffectivePhraseOverlayLimitFallsBackToCertifiedDefault(t *testing.T) {
	for _, requested := range []int{0, -1, -100} {
		if got := EffectivePhraseOverlayLimit(requested); got != MaxPhraseOverlaysPerRun {
			t.Fatalf("EffectivePhraseOverlayLimit(%d) = %d, want the certified default %d", requested, got, MaxPhraseOverlaysPerRun)
		}
	}
	for _, requested := range []int{1, 9, 40} {
		if got := EffectivePhraseOverlayLimit(requested); got != requested {
			t.Fatalf("EffectivePhraseOverlayLimit(%d) = %d, want the caller value verbatim", requested, got)
		}
	}
}

func limitTestItems(phrases int) []OverlayItem {
	items := make([]OverlayItem, 0, 20+phrases)
	for i := 0; i < 20; i++ {
		items = append(items, OverlayItem{
			ID:        fmt.Sprintf("image-%d", i),
			Kind:      "entity_image",
			AssetRefs: []OverlayAssetRef{{SHA256: fmt.Sprintf("hash-%d", i)}},
			Params:    map[string]any{"priority": float64(i)},
		})
	}
	for i := 0; i < phrases; i++ {
		items = append(items, OverlayItem{
			ID:     fmt.Sprintf("phrase-%d", i),
			Kind:   "text_phrase",
			Text:   fmt.Sprintf("Grounded phrase %d", i),
			Params: map[string]any{"priority": float64(i)},
		})
	}
	return items
}

func countPhraseItems(items []OverlayItem) int {
	count := 0
	for _, item := range items {
		if item.Kind == "text_phrase" {
			count++
		}
	}
	return count
}

func phraseItemIDs(items []OverlayItem) map[string]bool {
	ids := map[string]bool{}
	for _, item := range items {
		if item.Kind == "text_phrase" {
			ids[item.ID] = true
		}
	}
	return ids
}

// TestApplyEditorialOverlayBudgetHonoursCallerPhraseLimit certifies the
// payload-driven ceiling: the image ceiling stays fixed, the phrase ceiling
// follows the caller, and the reported budget echoes the caller's request.
func TestApplyEditorialOverlayBudgetHonoursCallerPhraseLimit(t *testing.T) {
	items := limitTestItems(17)

	// A ceiling BELOW the certified default: only the top-N unique grounded
	// phrases survive, ranked by their certified score (descending).
	got, budget := ApplyEditorialOverlayBudgetWithLimit(items, 3)
	if phrases := countPhraseItems(got); phrases != 3 {
		t.Fatalf("phrase overlays = %d, want the caller ceiling 3", phrases)
	}
	if budget != (PhraseOverlayBudget{Requested: 3, Materialized: 3, Shortfall: 0}) {
		t.Fatalf("phrase budget = %+v, want requested/materialized 3", budget)
	}
	// The three highest-scored phrases must win, and nothing else may leak in.
	if ids := phraseItemIDs(got); !ids["phrase-14"] || !ids["phrase-15"] || !ids["phrase-16"] || len(ids) != 3 {
		t.Fatalf("lower ceiling admitted the wrong phrases: %+v", ids)
	}

	// A ceiling ABOVE the certified default: every grounded candidate is
	// admitted, and the image ceiling is untouched.
	got, budget = ApplyEditorialOverlayBudgetWithLimit(items, 40)
	if phrases := countPhraseItems(got); phrases != 17 {
		t.Fatalf("phrase overlays = %d, want all 17 candidates admitted", phrases)
	}
	if images := len(got) - 17; images != MaxImageOverlaysPerRun {
		t.Fatalf("image overlays = %d, want the unchanged ceiling %d", images, MaxImageOverlaysPerRun)
	}
	if budget != (PhraseOverlayBudget{Requested: 40, Materialized: 17, Shortfall: 23}) {
		t.Fatalf("phrase budget = %+v, want requested 40 and 17 materialized", budget)
	}

	// Absent (zero) keeps the certified default.
	got, budget = ApplyEditorialOverlayBudgetWithLimit(items, 0)
	if phrases := countPhraseItems(got); phrases != MaxPhraseOverlaysPerRun {
		t.Fatalf("phrase overlays = %d, want the certified default %d", phrases, MaxPhraseOverlaysPerRun)
	}
	if budget.Requested != MaxPhraseOverlaysPerRun {
		t.Fatalf("phrase budget requested = %d, want the certified default %d", budget.Requested, MaxPhraseOverlaysPerRun)
	}
}

// TestPhraseReservationNeverClobbersTheLongCandidatesItDidNotReserve pins the
// long/short reservation against slice-aliasing: admission must follow from the
// ranking alone, never from whether the reservation happened to reuse the
// backing array of the long list.
//
// The fixture is the shape the defect needs: MORE 8+ word candidates than the
// reservation (3 of 5), plus a short candidate, so the surplus long candidates
// are re-appended after the short ones were written into long's spare capacity.
// Writing them in place destroys the surplus entries before they are read back,
// which both loses a long candidate that fits the budget and admits the short
// one twice.
func TestPhraseReservationNeverClobbersTheLongCandidatesItDidNotReserve(t *testing.T) {
	longText := func(i int) string {
		return fmt.Sprintf("long candidate %d carries eight words right here", i)
	}
	items := []OverlayItem{
		{ID: "long-1", Kind: "text_phrase", Text: longText(1), Params: map[string]any{"priority": 0.9}},
		{ID: "long-2", Kind: "text_phrase", Text: longText(2), Params: map[string]any{"priority": 0.8}},
		{ID: "long-3", Kind: "text_phrase", Text: longText(3), Params: map[string]any{"priority": 0.7}},
		{ID: "long-4", Kind: "text_phrase", Text: longText(4), Params: map[string]any{"priority": 0.6}},
		{ID: "long-5", Kind: "text_phrase", Text: longText(5), Params: map[string]any{"priority": 0.5}},
		{ID: "short-1", Kind: "text_phrase", Text: "short fragment", Params: map[string]any{"priority": 0.4}},
	}
	// The reservation keys on the 8+ word class, so a fixture edit that made a
	// "long" candidate short would silently stop exercising the branch.
	for _, item := range items {
		words := len(strings.Fields(item.Text))
		if item.ID == "short-1" {
			if words >= 8 {
				t.Fatalf("fixture %q must stay a short candidate, got %d words", item.ID, words)
			}
			continue
		}
		if words < 8 {
			t.Fatalf("fixture %q must stay a long candidate, got %d words", item.ID, words)
		}
	}

	got, budget := ApplyEditorialOverlayBudgetWithLimit(items, 5)
	if budget.Requested != 5 || budget.Materialized != 5 {
		t.Fatalf("phrase budget = %+v, want 5 requested and 5 materialized", budget)
	}

	admitted := make([]string, 0, len(got))
	seen := map[string]int{}
	for _, item := range got {
		admitted = append(admitted, item.ID)
		seen[item.ID]++
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("phrase %q admitted %d times: %v", id, count, admitted)
		}
	}
	// ADMITTED SET: the three reserved long candidates, the short one (remaining
	// slots follow the same deterministic ranking), then the top surplus long
	// candidate — the ceiling cuts long-5. The budget returns the admitted items
	// in PLAN order, so the set is what this assertion is about.
	want := []string{"long-1", "long-2", "long-3", "long-4", "short-1"}
	if len(admitted) != len(want) {
		t.Fatalf("admitted %v, want %v", admitted, want)
	}
	for i := range want {
		if admitted[i] != want[i] {
			t.Fatalf("admitted %v, want %v", admitted, want)
		}
	}
}

// TestRunLevelBudgetDoesNotLetAnotherSceneEvictAHigherRankedPhrase pins the
// single-owner rule for overlap: how many items may share a moment belongs to
// the planner's overlap budget, and comparing windows ACROSS scenes is never
// valid — scene 2's 100-300ms phrase must not evict scene 1's.
func TestRunLevelBudgetDoesNotLetAnotherSceneEvictAHigherRankedPhrase(t *testing.T) {
	items := []OverlayItem{
		{ID: "scene-1-higher", SceneID: "scene-1", Kind: "text_phrase", Text: "higher ranked phrase", StartMs: 100, EndMs: 300, Params: map[string]any{"priority": 0.9}},
		{ID: "scene-2-same-window", SceneID: "scene-2", Kind: "text_phrase", Text: "other scene phrase", StartMs: 100, EndMs: 300, Params: map[string]any{"priority": 0.8}},
	}
	got, _ := ApplyEditorialOverlayBudgetWithLimit(items, 5)
	ids := phraseItemIDs(got)
	if !ids["scene-1-higher"] || !ids["scene-2-same-window"] {
		t.Fatalf("a same-millisecond phrase in another scene evicted a candidate: %v", ids)
	}
	if len(got) != 2 {
		t.Fatalf("admitted %d items, want both grounded phrases", len(got))
	}
}

func TestRunLevelBudgetReplacesOverlappingPhraseFragmentsWithLongPhrase(t *testing.T) {
	items := []OverlayItem{
		{ID: "fragment", SceneID: "scene-1", Kind: "text_phrase", Text: "mandados de prisão temporária", StartMs: 200, EndMs: 300, Params: map[string]any{"priority": 0.99}},
		{ID: "long", SceneID: "scene-1", Kind: "text_phrase", Text: "emitidos dezesseis mandados de prisão temporária e mais dezoito mandados de busca", StartMs: 100, EndMs: 500, Params: map[string]any{"priority": 0.5}},
		{ID: "backup", SceneID: "scene-1", Kind: "text_phrase", Text: "a defesa contesta a necessidade e a proporcionalidade da prisão", StartMs: 600, EndMs: 800, Params: map[string]any{"priority": 0.4}},
	}
	got, budget := ApplyEditorialOverlayBudgetWithLimit(items, 2)
	ids := phraseItemIDs(got)
	if !ids["long"] || !ids["backup"] || ids["fragment"] {
		t.Fatalf("overlap-aware phrase set = %v, want long and backup only", ids)
	}
	if budget.Materialized != 2 {
		t.Fatalf("phrase budget = %+v, want two non-overlapping phrases", budget)
	}
}

func TestRunLevelImageBudgetKeepsPerSceneOccurrences(t *testing.T) {
	shared := strings.Repeat("a", 64)
	items := []OverlayItem{
		{ID: "scene-1-image", SceneID: "scene-1", Kind: "image", AssetRefs: []OverlayAssetRef{{SHA256: shared}}},
		{ID: "scene-2-image", SceneID: "scene-2", Kind: "image", AssetRefs: []OverlayAssetRef{{SHA256: shared}}},
		{ID: "scene-2-duplicate", SceneID: "scene-2", Kind: "image", AssetRefs: []OverlayAssetRef{{SHA256: shared}}},
	}
	got, _ := ApplyEditorialOverlayBudgetWithLimit(items, 5)
	var ids []string
	for _, item := range got {
		if item.Kind == "image" {
			ids = append(ids, item.ID)
		}
	}
	if len(ids) != 2 || ids[0] != "scene-1-image" || ids[1] != "scene-2-image" {
		t.Fatalf("per-scene image selection = %v, want one occurrence in each scene", ids)
	}
}

// TestApplyPhraseOverlayBudgetHonoursCallerPhraseLimit covers the non-run-level
// entry point (RunLevelEditorialBudget=false) with the same payload ceiling.
func TestApplyPhraseOverlayBudgetHonoursCallerPhraseLimit(t *testing.T) {
	items := append(limitTestItems(9), OverlayItem{ID: "keyword-1", Kind: "keyword", Text: "keyword"})

	got, budget := ApplyPhraseOverlayBudgetWithLimit(items, 2)
	if phrases := countPhraseItems(got); phrases != 2 {
		t.Fatalf("phrase overlays = %d, want the caller ceiling 2", phrases)
	}
	// Non-phrase items are copied through untouched.
	foundKeyword := false
	for _, item := range got {
		if item.ID == "keyword-1" {
			foundKeyword = true
		}
	}
	if !foundKeyword {
		t.Fatalf("non-phrase item was dropped: %+v", got)
	}
	if budget != (PhraseOverlayBudget{Requested: 2, Materialized: 2, Shortfall: 0}) {
		t.Fatalf("phrase budget = %+v, want requested/materialized 2", budget)
	}
}

// TestMeasurePhraseOverlayBudgetHonoursCallerPhraseLimit certifies the
// reported budget against an already-compiled plan reflects the caller
// request, not the compile-time default.
func TestMeasurePhraseOverlayBudgetHonoursCallerPhraseLimit(t *testing.T) {
	items := limitTestItems(2)
	if budget := MeasurePhraseOverlayBudgetWithLimit(items, 7); budget != (PhraseOverlayBudget{Requested: 7, Materialized: 2, Shortfall: 5}) {
		t.Fatalf("phrase budget = %+v, want requested 7 / materialized 2 / shortfall 5", budget)
	}
	if budget := MeasurePhraseOverlayBudgetWithLimit(items, 0); budget != (PhraseOverlayBudget{Requested: MaxPhraseOverlaysPerRun, Materialized: 2, Shortfall: MaxPhraseOverlaysPerRun - 2}) {
		t.Fatalf("phrase budget = %+v, want the certified default request", budget)
	}
}
