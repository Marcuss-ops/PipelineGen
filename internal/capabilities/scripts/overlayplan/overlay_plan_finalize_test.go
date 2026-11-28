package overlayplan

import (
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestNormalizeLegacyChainSegmentTemplates(t *testing.T) {
	brand := capmodel.OverlayItem{
		ID: "brand", Kind: string(capmodel.KindBrandText), TemplateID: "logo_default",
	}
	normalizeLegacyChainSegmentTemplates(&brand)
	if brand.TemplateID != "IMPORTANT_PHRASE" {
		t.Fatalf("legacy standalone brand template = %q, want IMPORTANT_PHRASE", brand.TemplateID)
	}

	chain := capmodel.OverlayItem{
		ID: "mixed", Kind: capmodel.KindMixedChain, TemplateID: capmodel.TemplateMixedChain,
	}
	chain.SetChainSegments([]capmodel.ChainSegment{
		{Kind: "brand_text", TemplateID: "logo_default", Text: "Tyson", StartUS: 0, DurationUS: 1_000_000},
		{Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Phrase", StartUS: 1_000_000, DurationUS: 1_000_000},
	})

	normalizeLegacyChainSegmentTemplates(&chain)
	segments, ok := chain.Segments()
	if !ok || segments[0].TemplateID != "IMPORTANT_PHRASE" {
		t.Fatalf("legacy brand template was not normalized: ok=%v segments=%+v", ok, segments)
	}
	if segments[1].TemplateID != "IMPORTANT_PHRASE" {
		t.Fatalf("canonical phrase template changed unexpectedly: %+v", segments[1])
	}
}

// ITCLOSE-06: the planner chooses a visible family for plain phrase cards
// (short = Oswald, long = Playfair) and marks the item with phrase_family.
// The run-level overlay_style projection runs after the seal and must leave
// those two families distinct instead of flattening both to the run font.
func TestApplyPayloadOverlayStyleKeepsPlannerPhraseFamily(t *testing.T) {
	fontSize := 72.0
	canvas := OverlayCanvasSpec{Style: &scriptpkg.OverlayStyleSpec{
		FontFamily: "bricolage_grotesque",
		Size:       &scriptpkg.OverlaySizeSpec{FontSize: &fontSize},
	}}
	items := []capmodel.OverlayItem{
		{
			ID:         "short",
			Kind:       "text_phrase",
			TemplateID: "IMPORTANT_PHRASE",
			Params: map[string]any{
				"phrase_family": "short_phrase",
				"font_family":   "oswald",
				"font_size_px":  96.0,
			},
		},
		{
			ID:         "long",
			Kind:       "text_phrase",
			TemplateID: "IMPORTANT_PHRASE",
			Params:     map[string]any{},
		},
	}

	ApplyPayloadOverlayStyleToItems(items, canvas)

	if got := items[0].Params["font_family"]; got != "oswald" {
		t.Fatalf("planner short-phrase family was flattened: font_family=%v", got)
	}
	if got := items[0].Params["font_size_px"]; got != 96.0 {
		t.Fatalf("planner short-phrase size was flattened: font_size_px=%v", got)
	}
	if got := items[1].Params["font_family"]; got != "bricolage_grotesque" {
		t.Fatalf("unlocked phrase must receive the run-level font: font_family=%v", got)
	}
}

func TestDedupeReanchoredPhrasePlanItemsKeepsChapterBullet(t *testing.T) {
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{
		{
			ID: "extracted-phrase", SceneID: "scene-02", Kind: "text_phrase",
			TemplateID: "IMPORTANT_PHRASE", StartUS: 125_058_000, DurationUS: 4_781_000,
			Text:   "Una delle clip porta la conversazione fuori dal terreno delle accuse e dentro quello del calcio.",
			Params: map[string]any{"priority": 0.95},
		},
		{
			ID: "chapter-bullet", SceneID: "scene-02", Kind: "text_phrase",
			TemplateID: "IMPORTANT_PHRASE", StartUS: 125_058_000, DurationUS: 4_781_000,
			Text:   "Una delle clip porta la conversazione fuori dal terreno delle accuse e dentro quello del calcio.",
			Params: map[string]any{"editorial_role": "chapter_bullet", "priority": 0.98},
		},
		{
			ID: "later-repeat", SceneID: "scene-02", Kind: "text_phrase",
			TemplateID: "IMPORTANT_PHRASE", StartUS: 140_000_000, DurationUS: 2_000_000,
			Text:   "Una delle clip porta la conversazione fuori dal terreno delle accuse e dentro quello del calcio.",
			Params: map[string]any{},
		},
	}}

	dedupeReanchoredPhrasePlanItems(plan)
	if len(plan.Items) != 2 {
		t.Fatalf("expected overlapping duplicate removed while later repeat survives, got %d items", len(plan.Items))
	}
	if plan.Items[0].ID != "chapter-bullet" || plan.Items[1].ID != "later-repeat" {
		t.Fatalf("unexpected phrase survivors: %#v", []string{plan.Items[0].ID, plan.Items[1].ID})
	}
}

func TestDedupeReanchoredPhrasePlanItemsDropsPhraseDuplicatedInsideChain(t *testing.T) {
	chain := capmodel.OverlayItem{
		ID: "phrase-chain", SceneID: "scene-02", Kind: capmodel.KindMixedChain,
		TemplateID: capmodel.TemplateMixedChain, StartUS: 10_000_000, DurationUS: 5_000_000,
	}
	chain.SetChainSegments([]capmodel.ChainSegment{{
		Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", Text: "Prime Video Sport",
		StartUS: 10_000_000, DurationUS: 2_500_000,
	}})
	plan := &capmodel.OverlayPlan{Items: []capmodel.OverlayItem{
		chain,
		{
			ID: "standalone-duplicate", SceneID: "scene-02", Kind: "text_phrase",
			TemplateID: "IMPORTANT_PHRASE", StartUS: 10_000_000, DurationUS: 2_500_000,
			Text: "Prime Video Sport", Params: map[string]any{},
		},
	}}

	dedupeReanchoredPhrasePlanItems(plan)
	if len(plan.Items) != 1 || plan.Items[0].ID != "phrase-chain" {
		t.Fatalf("expected only the chain to survive, got %#v", plan.Items)
	}
}

func TestResolveReanchoredPhraseOverlapsKeepsEditorialWinnersAndFitsDateTail(t *testing.T) {
	plan := &capmodel.OverlayPlan{Intake: &capmodel.OverlayPlanIntake{}, Items: []capmodel.OverlayItem{
		{ID: "title", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 0, DurationUS: 1_300_000, Text: "Una distinzione", Params: map[string]any{"editorial_role": "scene_title", "priority": 1.0}},
		{ID: "phrase", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 0, DurationUS: 2_300_000, Text: "La risposta comincia da una distinzione semplice", Params: map[string]any{"priority": 0.87}},
		{ID: "date", SceneID: "scene-04", Kind: "number", TemplateID: "TIMELINE_DATE_CARD", StartUS: 5_000_000, DurationUS: 5_000_000, Text: "13 settembre"},
		{ID: "bullet", SceneID: "scene-04", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 9_600_000, DurationUS: 5_800_000, Text: "Sono informazioni attribuite ai servizi RaiPlay", Params: map[string]any{"editorial_role": "chapter_bullet", "priority": 0.98}},
		{ID: "nested-phrase", SceneID: "scene-04", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 11_000_000, DurationUS: 2_800_000, Text: "attribuite ai servizi RaiPlay", Params: map[string]any{"priority": 0.91}},
	}}

	resolveReanchoredPhraseOverlaps(plan)

	if len(plan.Items) != 3 {
		t.Fatalf("items=%d, want title, trimmed date and chapter bullet", len(plan.Items))
	}
	if plan.Items[0].ID != "title" || plan.Items[1].ID != "date" || plan.Items[2].ID != "bullet" {
		t.Fatalf("unexpected survivors: %#v", []string{plan.Items[0].ID, plan.Items[1].ID, plan.Items[2].ID})
	}
	if got := plan.Items[1].DurationUS; got != 4_600_000 {
		t.Fatalf("trimmed date duration=%d, want 4600000", got)
	}
	for i, item := range plan.Items {
		for j := i + 1; j < len(plan.Items); j++ {
			other := plan.Items[j]
			if item.StartUSValue() < other.EndUSValue() && other.StartUSValue() < item.EndUSValue() {
				t.Fatalf("survivors overlap: %s and %s", item.ID, other.ID)
			}
		}
	}
	if got := plan.Intake.DroppedByReason["reanchored_overlap"]; got != 2 {
		t.Fatalf("reanchored_overlap drops=%d, want 2", got)
	}
}

func TestAppendFixedIntroHookUsesOpeningDisplayTextAndThreeSecondWindow(t *testing.T) {
	plan := &capmodel.OverlayPlan{}
	result := &contract.GenerateResult{Scenes: []contract.Scene{
		{ID: "scene-intro", Role: scriptpkg.SceneRoleOpening, ExecutionMode: scriptpkg.SceneExecutionFixedMedia, DurationUS: 5_000_000, Text: map[contract.Language]string{"it": "Accuse, repliche, fonti"}},
		{ID: "scene-outro", Role: scriptpkg.SceneRoleClosing, ExecutionMode: scriptpkg.SceneExecutionFixedMedia, DurationUS: 5_000_000, Text: map[contract.Language]string{"it": "Guarda i prossimi aggiornamenti"}},
	}}

	appendFixedIntroHook(plan, result, "it")

	if len(plan.Items) != 1 {
		t.Fatalf("hook count=%d, want 1", len(plan.Items))
	}
	hook := plan.Items[0]
	if hook.SceneID != "scene-intro" || hook.Text != "Accuse, repliche, fonti" {
		t.Fatalf("hook=%+v, want intro display text", hook)
	}
	if hook.StartUS != 0 || hook.DurationUS != 3_000_000 || hook.EndMs != 3000 {
		t.Fatalf("hook window start=%d duration=%d end_ms=%d, want [0, 3s]", hook.StartUS, hook.DurationUS, hook.EndMs)
	}
}
