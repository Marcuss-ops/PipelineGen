package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// ── Temporal facts wiring (Fase 2) ──

func TestSemanticCandidatesMapsSingleCallBundle(t *testing.T) {
	source := "Greek salad opened on 15 marzo 2008."
	const payload = `{
	 "entities":[{"text":"salad","start":6,"end":11,"score":0.9,"type":"CONCEPT"}],
	 "relations":[],"lower_thirds":[],
	 "bullets":[{"sentence_index":0,"text":"Greek salad opened on 15 marzo 2008.","start":0,"end":36,"entities":[0]}],
	 "chart_points":[],
	 "temporal_facts":[{"surface":"15 marzo 2008","type":"DATE","normalized_date":"2008-03-15","precision":"day","start_byte":22,"end_byte":35,"evidence":"15 marzo 2008"}]
	}`
	// Guard the fixture offsets against drift.
	if source[22:35] != "15 marzo 2008" {
		t.Fatalf("fixture offset drift: %q", source[22:35])
	}
	r := &semanticRunner{output: []byte(payload)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	bundle, err := a.SemanticCandidates(context.Background(), "it", source, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Bullets) != 1 || bundle.Bullets[0].Text != source {
		t.Fatalf("bullets: %+v", bundle.Bullets)
	}
	if len(bundle.Charts) != 0 {
		t.Fatalf("charts: %+v", bundle.Charts)
	}
	if len(bundle.Temporal) != 1 {
		t.Fatalf("temporal facts: %+v", bundle.Temporal)
	}
	fact := bundle.Temporal[0]
	if fact.Text != "15 marzo 2008" || fact.NormalizedDate != "2008-03-15" || fact.Precision != "day" || fact.Start != 22 || fact.End != 35 || fact.Ambiguous {
		t.Fatalf("temporal fact: %+v", fact)
	}
	var req visualNERRequest
	if err := json.Unmarshal(r.input, &req); err != nil {
		t.Fatal(err)
	}
	if req.Operation != "analyze" || req.EntityCount != 12 {
		t.Fatalf("request: %+v", req)
	}
}

func TestSemanticCandidatesMapsNumericFacts(t *testing.T) {
	source := "Revenue hit $1,200 with 25% growth."
	const payload = `{"entities":[],"relations":[],"lower_thirds":[],"bullets":[],"chart_points":[],
	 "numeric_facts":[{"surface":"$1,200","type":"MONEY","value":1200,"start_byte":12,"end_byte":18,"evidence":"$1,200"},{"surface":"25%","type":"PERCENT","value":25,"start_byte":24,"end_byte":27,"evidence":"25%"}]}`
	a, _ := NewVisualNERAdapter(executorForSemantic(&semanticRunner{output: []byte(payload)}))
	bundle, err := a.SemanticCandidates(context.Background(), "en", source, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Numeric) != 2 {
		t.Fatalf("numeric facts: %+v", bundle.Numeric)
	}
	if bundle.Numeric[0].Text != "$1,200" || !bundle.Numeric[0].HasValue || bundle.Numeric[0].Value != 1200 || bundle.Numeric[0].Kind != "MONEY" {
		t.Fatalf("money fact: %+v", bundle.Numeric[0])
	}
	if bundle.Numeric[1].Value != 25 || !bundle.Numeric[1].HasValue {
		t.Fatalf("percent fact: %+v", bundle.Numeric[1])
	}
}

func TestAnalyzeRejectsUngroundedNumericFact(t *testing.T) {
	source := "Revenue hit $1,200."
	r := &semanticRunner{output: []byte(`{"entities":[],"relations":[],"lower_thirds":[],"bullets":[],"chart_points":[],"numeric_facts":[{"surface":"$1,200","type":"MONEY","start_byte":0,"end_byte":6,"evidence":"$1,200"}]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.Analyze(context.Background(), "en", source, 3); err == nil {
		t.Fatal("expected grounding error for numeric fact")
	}
}

func TestAnalyzeAcceptsBundleWithoutTemporalFactsBackwardsCompatible(t *testing.T) {
	source := "Greek salad"
	r := &semanticRunner{output: []byte(`{"entities":[],"relations":[],"lower_thirds":[],"bullets":[],"chart_points":[]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.Analyze(context.Background(), "en", source, 3); err != nil {
		t.Fatalf("older binaries without temporal_facts must decode: %v", err)
	}
}

func TestAnalyzeRejectsUngroundedTemporalFact(t *testing.T) {
	source := "Opened 15 marzo 2008."
	// Offsets point at a different span than the surface.
	r := &semanticRunner{output: []byte(`{"entities":[],"relations":[],"lower_thirds":[],"bullets":[],"chart_points":[],"temporal_facts":[{"surface":"15 marzo 2008","type":"DATE","normalized_date":"2008-03-15","precision":"day","start_byte":0,"end_byte":14,"evidence":"15 marzo 2008"}]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.Analyze(context.Background(), "it", source, 3); err == nil {
		t.Fatal("expected grounding error for temporal fact")
	}
}

func TestAnalyzeRejectsInventedNormalization(t *testing.T) {
	source := "La consegna fu il 12 marzo."
	r := &semanticRunner{output: []byte(`{"entities":[],"relations":[],"lower_thirds":[],"bullets":[],"chart_points":[],"temporal_facts":[{"surface":"12 marzo","type":"DATE","normalized_date":"2020-03-12","precision":"day","start_byte":17,"end_byte":25,"evidence":"12 marzo"}]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.Analyze(context.Background(), "it", source, 3); err == nil {
		t.Fatal("expected rejection: unresolved claim must not carry a normalized date")
	}
}

func TestAnalyzeRejectsInvalidIsoNormalization(t *testing.T) {
	source := "Data: 12 marzo 2008."
	r := &semanticRunner{output: []byte(`{"entities":[],"relations":[],"lower_thirds":[],"bullets":[],"chart_points":[],"temporal_facts":[{"surface":"12 marzo 2008","type":"DATE","normalized_date":"2008-02-31","precision":"day","start_byte":6,"end_byte":19,"evidence":"12 marzo 2008"}]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.Analyze(context.Background(), "it", source, 3); err == nil {
		t.Fatal("expected rejection: impossible calendar date 2008-02-31")
	}
}

func TestAnalyzeRejectsUnknownPrecision(t *testing.T) {
	source := "2001"
	r := &semanticRunner{output: []byte(`{"entities":[],"relations":[],"lower_thirds":[],"bullets":[],"chart_points":[],"temporal_facts":[{"surface":"2001","type":"DATE","normalized_date":"2001","precision":"decade","start_byte":0,"end_byte":4,"evidence":"2001"}]}`)}
	a, _ := NewVisualNERAdapter(executorForSemantic(r))
	if _, err := a.Analyze(context.Background(), "it", source, 3); err == nil {
		t.Fatal("expected rejection: unknown precision")
	}
}

// TestSemanticCandidatesKeepsNearbyDatesFromTwoToSeven walks the Fase 2
// acceptance ladder: 2..=7 date facts in one scene, all mapped to the
// temporal bundle regardless of the visual top-N budget.
func TestSemanticCandidatesKeepsNearbyDatesFromTwoToSeven(t *testing.T) {
	years := []string{"1998", "2001", "2005", "2010", "2019", "2050", "2088"}
	for count := 2; count <= 7; count++ {
		source := "Cronologia: "
		facts := make([]string, 0, count)
		for i, year := range years[:count] {
			fact := year + " marzo"
			if i > 0 {
				source += ", poi "
			}
			source += fact
			facts = append(facts, fact)
		}
		source += "."
		// Build the worker payload with offsets verified directly against
		// the source, so instrumenting the test cannot drift the fixture.
		entries := make([]string, 0, count)
		for _, fact := range facts {
			start := strings.Index(source, fact)
			if start < 0 {
				t.Fatalf("fixture drift: %q not in %q", fact, source)
			}
			normalized := fact[:4] + "-03"
			entries = append(entries, fmt.Sprintf(
				"{\"surface\":%q,\"type\":\"DATE\",\"normalized_date\":%q,\"precision\":\"month\",\"start_byte\":%d,\"end_byte\":%d,\"evidence\":%q}",
				fact, normalized, start, start+len(fact), fact))
		}
		payload := fmt.Sprintf(`{"entities":[],"relations":[],"lower_thirds":[],"bullets":[],"chart_points":[],"numeric_facts":[],"temporal_facts":[%s]}`,
			strings.Join(entries, ","))
		a, _ := NewVisualNERAdapter(executorForSemantic(&semanticRunner{output: []byte(payload)}))
		// entity_count is an intentionally small top-N budget: the temporal
		// facts must survive it untouched.
		bundle, err := a.SemanticCandidates(context.Background(), "it", source, 3)
		if err != nil {
			t.Fatalf("count=%d: %v", count, err)
		}
		if len(bundle.Temporal) != count {
			t.Fatalf("count=%d: temporal facts %+v", count, bundle.Temporal)
		}
		for i, fact := range bundle.Temporal {
			if fact.Text != facts[i] || fact.Precision != "month" || fact.NormalizedDate != facts[i][:4]+"-03" {
				t.Fatalf("count=%d fact %d: %+v", count, i, fact)
			}
		}
	}
}

// TestSemanticCandidatesKeepsNumericFactsBeyondTopN covers the numeric half
// of Fase 2: more NUMBER/PERCENT/MONEY occurrences than the visual top-N
// budget must all survive into the bundle.
func TestSemanticCandidatesKeepsNumericFactsBeyondTopN(t *testing.T) {
	source := "Vendite: $1,200 e 25%. Poi euro 1.200 euro, e un 12,5% finale."
	facts := []struct {
		surface string
		kind    string
		value   float64
	}{
		{"$1,200", "MONEY", 1200}, {"25%", "PERCENT", 25},
		{"1.200 euro", "MONEY", 1200}, {"12,5%", "PERCENT", 12.5},
	}
	// Build the worker payload with offsets verified directly against the
	// source so the fixture cannot drift.
	entries := make([]string, 0, len(facts))
	for _, fact := range facts {
		start := strings.Index(source, fact.surface)
		if start < 0 {
			t.Fatalf("fixture drift: %q not in %q", fact.surface, source)
		}
		value := fmt.Sprintf("%v", fact.value)
		entries = append(entries, fmt.Sprintf(
			"{\"surface\":%q,\"type\":%q,\"value\":%s,\"start_byte\":%d,\"end_byte\":%d,\"evidence\":%q}",
			fact.surface, fact.kind, value, start, start+len(fact.surface), fact.surface))
	}
	payload := fmt.Sprintf(`{"entities":[],"relations":[],"lower_thirds":[],"bullets":[],"chart_points":[],"numeric_facts":[%s]}`,
		strings.Join(entries, ","))
	// top-N=2 is the deliberately small visual budget: all four numeric
	// facts must survive it.
	a, _ := NewVisualNERAdapter(executorForSemantic(&semanticRunner{output: []byte(payload)}))
	bundle, err := a.SemanticCandidates(context.Background(), "it", source, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Numeric) != len(facts) {
		t.Fatalf("numeric facts beyond top-N=2: %+v", bundle.Numeric)
	}
	for i, w := range facts {
		fact := bundle.Numeric[i]
		if fact.Text != w.surface || fact.Kind != w.kind || !fact.HasValue || fact.Value != w.value {
			t.Fatalf("fact %d: %+v, want %+v", i, fact, w)
		}
		if source[fact.Start:fact.End] != fact.Text {
			t.Fatalf("fact %d not grounded: %v", i, fact)
		}
	}
}
