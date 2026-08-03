package script

import (
	"strings"
	"testing"
)

func introHookSegments() []ScriptSegment {
	return []ScriptSegment{
		{ID: IntroHookSegmentID, Topic: "Introduzione", SourceText: "Cinque campioni hanno trasformato il pugilato in modi completamente diversi."},
		{ID: "boxer-mike-tyson", Topic: "Mike Tyson", SourceText: "Mike Tyson e la potenza."},
	}
}

func introHookBinding() StockBindingInput {
	return StockBindingInput{
		Index: 0, SceneID: "scene-0", SegmentID: IntroHookSegmentID,
		AssetID: "INTRO_STOCK_ASSET_ID", StartMs: 0, EndMs: 7000,
	}
}

func TestValidateIntroHookStockAcceptsCanonicalBinding(t *testing.T) {
	if d := validateIntroHookStock(introHookSegments(), []StockBindingInput{introHookBinding()}, "item"); len(d) > 0 {
		t.Fatalf("canonical intro-hook binding rejected: %v", d)
	}
}

func TestValidateIntroHookStockIgnoresNonIntroBindings(t *testing.T) {
	bindings := []StockBindingInput{
		{Index: 1, SegmentID: "boxer-mike-tyson", AssetID: "tyson", StartMs: 0, EndMs: 5000},
		{Index: 2, SegmentID: "boxer-muhammad-ali", AssetID: "ali", StartMs: 0, EndMs: 5000},
	}
	if d := validateIntroHookStock(introHookSegments(), bindings, "item"); len(d) > 0 {
		t.Fatalf("boxer bindings must not trigger intro-hook rules: %v", d)
	}
}

func TestValidateIntroHookStockRejectsIndexMoved(t *testing.T) {
	b := introHookBinding()
	b.Index = 1
	d := validateIntroHookStock(introHookSegments(), []StockBindingInput{b}, "item")
	if len(d) != 1 || !strings.Contains(d[0], "must target index 0") {
		t.Fatalf("details = %v, want single index-0 violation", d)
	}
}

func TestValidateIntroHookStockRejectsWrongSceneID(t *testing.T) {
	b := introHookBinding()
	b.SceneID = "scene-1"
	d := validateIntroHookStock(introHookSegments(), []StockBindingInput{b}, "item")
	if len(d) != 1 || !strings.Contains(d[0], "must target scene-0") {
		t.Fatalf("details = %v, want scene-0 violation", d)
	}
}

func TestValidateIntroHookStockRejectsMissingSegmentSlot(t *testing.T) {
	// intro-hook is not the first explicit segment.
	segments := []ScriptSegment{
		{ID: "boxer-mike-tyson", Topic: "Mike Tyson", SourceText: "Tyson."},
	}
	d := validateIntroHookStock(segments, []StockBindingInput{introHookBinding()}, "item")
	if len(d) != 1 || !strings.Contains(d[0], "segments[0].id=intro-hook") {
		t.Fatalf("details = %v, want segments[0] violation", d)
	}
	if d := validateIntroHookStock(nil, []StockBindingInput{introHookBinding()}, "item"); len(d) != 1 {
		t.Fatalf("empty segments must be rejected, got %v", d)
	}
}

func TestValidateIntroHookStockRejectsEmptySourceText(t *testing.T) {
	segments := []ScriptSegment{
		{ID: IntroHookSegmentID, Topic: "Introduzione", SourceText: "   "},
	}
	d := validateIntroHookStock(segments, []StockBindingInput{introHookBinding()}, "item")
	if len(d) != 1 || !strings.Contains(d[0], "requires non-empty source_text") {
		t.Fatalf("details = %v, want non-empty source_text violation", d)
	}
}

func TestValidateIntroHookStockRejectsInvalidInterval(t *testing.T) {
	b := introHookBinding()
	b.StartMs = -1
	d := validateIntroHookStock(introHookSegments(), []StockBindingInput{b}, "item")
	if len(d) != 1 || !strings.Contains(d[0], "start_ms >= 0") {
		t.Fatalf("details = %v, want start_ms violation", d)
	}
	b = introHookBinding()
	b.EndMs = b.StartMs
	d = validateIntroHookStock(introHookSegments(), []StockBindingInput{b}, "item")
	if len(d) != 1 || !strings.Contains(d[0], "end_ms must be greater than start_ms") {
		t.Fatalf("details = %v, want end_ms violation", d)
	}
}

func TestGenerationEnvelopeRejectsInvalidIntroHookBinding(t *testing.T) {
	item := GenerationItemV2{
		ID: "item", Source: SourceSpec{Type: SourceText, Topic: "topic"},
		Output: OutputSpec{
			StockEnabled: ToggleEnabled,
			StockBindings: []StockBindingInput{
				{Index: 1, SceneID: "scene-0", SegmentID: IntroHookSegmentID, AssetID: "intro", StartMs: 0, EndMs: 7000},
			},
		},
		ScriptParams: ScriptSpec{Segments: introHookSegments()},
	}
	err := (&GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{item}}).Validate()
	if err == nil {
		t.Fatal("expected intro-hook index violation to fail validation")
	}
	if pe, ok := err.(*PlanInvalidError); !ok {
		t.Fatalf("error type = %T, want PlanInvalidError", err)
	} else if len(pe.Details) != 1 || !strings.Contains(pe.Details[0], "must target index 0") {
		t.Fatalf("details = %v", pe.Details)
	}
}

func TestGenerationEnvelopeAcceptsCanonicalIntroHookBinding(t *testing.T) {
	item := GenerationItemV2{
		ID: "item", Source: SourceSpec{Type: SourceText, Topic: "topic"},
		Output: OutputSpec{
			StockEnabled: ToggleEnabled,
			StockBindings: []StockBindingInput{
				{Index: 0, SceneID: "scene-0", SegmentID: IntroHookSegmentID, AssetID: "intro", StartMs: 0, EndMs: 7000},
			},
		},
		ScriptParams: ScriptSpec{Segments: introHookSegments()},
	}
	if err := (&GenerationEnvelopeV2{Version: 2, Items: []GenerationItemV2{item}}).Validate(); err != nil {
		t.Fatalf("canonical intro-hook envelope rejected: %v", err)
	}
}
