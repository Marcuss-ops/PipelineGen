package rustexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectSemanticQuantitiesChecksAndPreservesGroundedMeasures(t *testing.T) {
	source := "Il fatturato è 3,2 miliardi di euro nel 2019, contro 2 miliardi di euro nel 2019."
	firstStart := len("Il fatturato è ")
	firstEnd := firstStart + len("3,2 miliardi")
	secondStart := len("Il fatturato è 3,2 miliardi di euro nel 2019, contro ")
	secondEnd := secondStart + len("2 miliardi")
	valueA, rawA, scale := 3.2e9, 3.2, 1e9
	valueB, rawB := 2e9, 2.0
	periodA := &semanticEngineQuantityPeriod{Year: 2019, FactIndex: 0}
	periodB := &semanticEngineQuantityPeriod{Year: 2019, FactIndex: 1}
	cueStart := len("Il fatturato è 3,2 miliardi di euro nel 2019, ")
	cueEnd := cueStart + len("contro")
	comparisonDifference := 1.2e9
	quantities, comparisons, err := projectSemanticQuantities(source, []semanticEngineQuantity{
		{ID: "q_01", Value: &valueA, RawValue: &rawA, Scale: &scale, Dimension: "currency", Unit: "EUR", UnitSurface: "euro", Qualifier: "exact", Period: periodA, StartByte: firstStart, EndByte: firstEnd, Evidence: source[firstStart:firstEnd], Status: "asserted"},
		{ID: "q_02", Value: &valueB, RawValue: &rawB, Scale: &scale, Dimension: "currency", Unit: "EUR", UnitSurface: "euro", Qualifier: "exact", Period: periodB, StartByte: secondStart, EndByte: secondEnd, Evidence: source[secondStart:secondEnd], Status: "asserted"},
	}, []semanticEngineQuantityComparison{
		{Kind: "difference", Left: "q_01", Right: "q_02", Dimension: "currency", Difference: &comparisonDifference, EvidenceStartByte: cueStart, EvidenceEndByte: cueEnd},
	})
	if err != nil {
		t.Fatalf("projectSemanticQuantities: %v", err)
	}
	if len(quantities) != 2 || quantities[0].Value == nil || *quantities[0].Value != valueA {
		t.Fatalf("quantities = %+v, want both exact source measures", quantities)
	}
	if len(comparisons) != 1 || comparisons[0].Evidence != "contro" || *comparisons[0].Difference != comparisonDifference {
		t.Fatalf("comparisons = %+v, want grounded difference", comparisons)
	}
}

func TestProjectSemanticQuantitiesRejectsCrossPeriodCurrencyComparisons(t *testing.T) {
	source := "3 euro nel 2019 contro 2 euro nel 2020"
	valueA, valueB := 3.0, 2.0
	periodA := &semanticEngineQuantityPeriod{Year: 2019, FactIndex: 0}
	periodB := &semanticEngineQuantityPeriod{Year: 2020, FactIndex: 1}
	firstStart := 0
	firstEnd := len("3")
	secondStart := len("3 euro nel 2019 contro ")
	secondEnd := secondStart + 1
	zero := 0.0
	_, _, err := projectSemanticQuantities(source, []semanticEngineQuantity{
		{ID: "q1", Value: &valueA, Dimension: "currency", Unit: "EUR", UnitSurface: "euro", Period: periodA, StartByte: firstStart, EndByte: firstEnd, Evidence: "3"},
		{ID: "q2", Value: &valueB, Dimension: "currency", Unit: "EUR", UnitSurface: "euro", Period: periodB, StartByte: secondStart, EndByte: secondEnd, Evidence: "2"},
	}, []semanticEngineQuantityComparison{{Kind: "difference", Left: "q1", Right: "q2", Dimension: "currency", Difference: &zero, EvidenceStartByte: len("3 euro nel 2019 "), EvidenceEndByte: len("3 euro nel 2019 contro")}})
	if err == nil {
		t.Fatal("currency comparison across different years passed")
	}
}

func TestProjectSemanticQuantitiesRejectsUngroundedEvidence(t *testing.T) {
	value := 42.0
	_, _, err := projectSemanticQuantities("The rate is 42%.", []semanticEngineQuantity{{
		ID: "q1", Value: &value, Dimension: "percent", Unit: "PCT", UnitSurface: "%", StartByte: 13, EndByte: 15, Evidence: "42",
	}}, nil)
	if err == nil {
		t.Fatal("ungrounded quantity passed")
	}
}

func TestSemanticEngineAdapterCarriesTypedQuantitiesIntoTheGoBundle(t *testing.T) {
	var response map[string]any
	if err := json.Unmarshal([]byte(semanticFixtureWithScenes(t, 2)), &response); err != nil {
		t.Fatal(err)
	}
	manifest := response["manifest"].(map[string]any)
	analyzers := manifest["analyzers"].(map[string]any)
	analyzers["quantities"] = "typed_quantities.v1"
	scenes := manifest["scenes"].([]any)
	scene := scenes[0].(map[string]any)
	firstStart := strings.Index(semanticSceneOne, "25%")
	secondStart := strings.Index(semanticSceneOne, "18%")
	cueStart := strings.Index(semanticSceneOne, "against")
	scene["quantities"] = []any{
		map[string]any{"id": "q_01", "value": 25.0, "raw_value": 25.0, "dimension": "percent", "unit": "PCT", "unit_surface": "25%", "qualifier": "exact", "start_byte": firstStart, "end_byte": firstStart + 3, "evidence": "25%", "sentence_index": 1, "status": "asserted"},
		map[string]any{"id": "q_02", "value": 18.0, "raw_value": 18.0, "dimension": "percent", "unit": "PCT", "unit_surface": "18%", "qualifier": "exact", "start_byte": secondStart, "end_byte": secondStart + 3, "evidence": "18%", "sentence_index": 1, "status": "asserted"},
	}
	scene["quantity_comparisons"] = []any{map[string]any{
		"kind": "difference", "left": "q_01", "right": "q_02", "dimension": "percent", "difference": 7.0,
		"evidence_start_byte": cueStart, "evidence_end_byte": cueStart + len("against"),
	}}
	fixture, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	adapter, _ := NewSemanticEngineAdapter(executorForSemantic(&semanticRunner{output: fixture}))
	bundles, err := adapter.SemanticBundles(context.Background(), "en", semanticScenes())
	if err != nil {
		t.Fatal(err)
	}
	bundle := bundles["scene_01"]
	if len(bundle.Quantities) != 2 || bundle.Quantities[0].Unit != "PCT" || *bundle.Quantities[0].Value != 25 {
		t.Fatalf("typed quantities were not carried through: %+v", bundle.Quantities)
	}
	if len(bundle.QuantityComparisons) != 1 || bundle.QuantityComparisons[0].Evidence != "against" {
		t.Fatalf("quantity comparison was not carried through: %+v", bundle.QuantityComparisons)
	}
}
