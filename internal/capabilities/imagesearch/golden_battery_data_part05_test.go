package imagesearch

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// runGoldenCase runs one sentence through the real resolver and asserts every
// expected surface, returning the metrics row. The language selects the
// battery variant ("en" or "it"); the expected queries/entities/canonical
// ids are identical across languages (canonicalization is language-
// invariant).
func runGoldenCase(t *testing.T, resolver *Resolver, gc goldenCase, lang string) caseMetrics {
	t.Helper()
	metrics := caseMetrics{id: lang + ":" + gc.id, expected: len(gc.wantEntities) + len(gc.wantVisual) + len(gc.wantContexts) + len(gc.wantNegated)}

	dec := resolver.Resolve(context.Background(), Request{Text: gc.text, Language: lang, PriorPersons: gc.prior})

	// 1. image_search_required decision.
	metrics.requiredOK = dec.Required == gc.wantRequired
	if !metrics.requiredOK {
		t.Errorf("[%s] image_search_required = %v, want %v (reason %q)", gc.id, dec.Required, gc.wantRequired, dec.NoImageReason)
	}

	// 2. Query list (exact, ordered).
	metrics.queries = len(dec.Queries)
	metrics.queriesOK = strings.Join(dec.Queries, "|") == strings.Join(gc.wantQueries, "|")
	if !metrics.queriesOK {
		t.Errorf("[%s] queries = %v, want %v", gc.id, dec.Queries, gc.wantQueries)
	}
	if !gc.wantRequired && len(dec.Queries) > 0 {
		t.Errorf("[%s] abstract sentence must not produce queries: %v", gc.id, dec.Queries)
	}

	// 3. Forbidden queries (wrong identity / negated person leaking).
	for _, forbidden := range gc.forbidQueries {
		for _, q := range dec.Queries {
			if strings.Contains(strings.ToLower(q), strings.ToLower(forbidden)) {
				metrics.wrongIdentity++
				t.Errorf("[%s] forbidden query surfaced: %q (contains %q)", gc.id, q, forbidden)
			}
		}
	}

	// 4. Forbidden entities.
	for _, forbidden := range gc.forbidEntities {
		for _, e := range detectedEntities(dec) {
			if e.Type == forbidden.typ && strings.EqualFold(e.Text, forbidden.text) {
				metrics.wrongIdentity++
				t.Errorf("[%s] forbidden entity surfaced: %s %q", gc.id, e.Type, e.Text)
			}
		}
	}

	// 5. Expected entities (typed + canonical).
	expected := append([]wantEntity(nil), gc.wantEntities...)
	expected = append(expected, gc.wantVisual...)
	expected = append(expected, gc.wantContexts...)
	expected = append(expected, gc.wantNegated...)
	detected := detectedEntities(dec)
	expectedSet := make(map[string]wantEntity, len(expected))
	for _, want := range expected {
		expectedSet[entityKey(want.typ, want.text)] = want
	}
	detectedSet := make(map[string]bool, len(detected))
	for _, e := range detected {
		detectedSet[entityKey(e.Type, e.Text)] = true
	}
	for _, want := range expected {
		if !detectedSet[entityKey(want.typ, want.text)] {
			metrics.missing = append(metrics.missing, fmt.Sprintf("%s %q", want.typ, want.text))
			t.Errorf("[%s] missing entity: %s %q", gc.id, want.typ, want.text)
			continue
		}
		metrics.found++
		if want.canonical == "" {
			continue
		}
		metrics.canonicalChecked++
		got := findDetected(detected, want.typ, want.text)
		if got.CanonicalID == want.canonical {
			metrics.canonicalOK++
		} else {
			metrics.canonicalMiss = append(metrics.canonicalMiss, fmt.Sprintf("%s %q → %q want %q", want.typ, want.text, got.CanonicalID, want.canonical))
			t.Errorf("[%s] canonical id: %s %q = %q, want %q", gc.id, want.typ, want.text, got.CanonicalID, want.canonical)
		}
	}
	for _, e := range detected {
		if _, ok := expectedSet[entityKey(e.Type, e.Text)]; !ok {
			metrics.falsePositives = append(metrics.falsePositives, fmt.Sprintf("%s %q", e.Type, e.Text))
			t.Errorf("[%s] false positive entity: %s %q", gc.id, e.Type, e.Text)
		}
	}

	// 6. Negated list.
	for _, want := range gc.wantNegated {
		found := false
		for _, e := range dec.Negated {
			if e.Type == want.typ && strings.EqualFold(e.Text, want.text) {
				found = true
				if want.canonical != "" && e.CanonicalID != want.canonical {
					metrics.canonicalMiss = append(metrics.canonicalMiss, fmt.Sprintf("negated %s %q", want.typ, want.text))
				}
			}
		}
		if !found {
			t.Errorf("[%s] negated entity missing: %s %q (negated=%+v)", gc.id, want.typ, want.text, dec.Negated)
		}
	}
	// A negated person must never be selected as primary or appear in queries.
	for _, e := range dec.Negated {
		for _, q := range dec.Queries {
			if strings.Contains(strings.ToLower(q), strings.ToLower(e.Text)) {
				metrics.negatedSeen++
				t.Errorf("[%s] negated person %q leaked into query %q", gc.id, e.Text, q)
			}
		}
		if dec.Primary != nil && dec.Primary.CanonicalID == e.CanonicalID && e.CanonicalID != "" {
			metrics.negatedSeen++
			t.Errorf("[%s] negated person %q selected as primary", gc.id, e.Text)
		}
	}

	// 7. Important phrases.
	for _, want := range gc.wantPhrases {
		found := false
		for _, phrase := range dec.ImportantPhrases {
			if strings.Contains(strings.ToLower(phrase), strings.ToLower(want)) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("[%s] important phrase %q missing (phrases=%v)", gc.id, want, dec.ImportantPhrases)
		}
	}

	// 8. No-image metric (abstract sentences must decide Required=false and
	// emit zero queries).
	if !gc.wantRequired {
		metrics.noImageOK = !dec.Required && len(dec.Queries) == 0
	}

	return metrics
}

// detectedEntities flattens every typed surface of the decision.
func detectedEntities(dec ImageSearchDecision) []ResolvedEntity {
	out := make([]ResolvedEntity, 0, len(dec.Entities)+len(dec.Contexts)+len(dec.Visual)+len(dec.Negated))
	out = append(out, dec.Entities...)
	out = append(out, dec.Contexts...)
	out = append(out, dec.Visual...)
	out = append(out, dec.Negated...)
	return out
}

func entityKey(typ, text string) string {
	return typ + "\x00" + strings.ToLower(strings.TrimSpace(text))
}

func findDetected(detected []ResolvedEntity, typ, text string) ResolvedEntity {
	for _, e := range detected {
		if e.Type == typ && strings.EqualFold(e.Text, text) {
			return e
		}
	}
	return ResolvedEntity{}
}

// TestGoldenBattery_ImageSearch runs the English battery.
