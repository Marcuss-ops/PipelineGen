// Package imagesearch — golden_battery_test.go is the PERMANENT golden
// regression battery for Image Search. It certifies the chain
//
//	FRASE → Semantic/Entity Extraction → Canonicalizzazione
//	      → Image Search Query → (Relevance validation) → Immagine scelta
//
// with 28 sentences (T01–T28) designed to break extractor and resolver:
// persons, places, companies, products, multiple entities in one sentence,
// ambiguous names, negation, abstract sentences that must NOT trigger an
// image search, pronoun coreference, and the full multi-sentence paragraph
// PipelineGen will actually process.
//
// The battery runs the REAL deterministic CPU extractor (the production
// fallback) through the Resolver, and certifies, per sentence:
//
//	expected entities found (typed)      → entity recall / precision
//	canonical ids derived correctly      → canonicalization accuracy
//	primary + ordered queries            → query relevance
//	no-image decision on abstract text   → no-image decision accuracy
//	wrong identity / negated selection   → must be ZERO
//
// The golden regression pairs are T18/T19 (Michael Jordan vs Michael B.
// Jordan), T22/T23 (jaguar animal vs Jaguar car) and T27 (negation) — the
// tests that prove the resolver understands WHICH entity to represent
// visually, not just which words to extract.
//
// NOTE on canonical ids: the repo's canonical format (entities.CanonicalEntityID)
// is "{type}:{slug}" — e.g. "person:floyd-mayweather". The battery spec's
// shorthand "floyd-mayweather" is the slug part; the type prefix is the
// repo's SSOT and is asserted here.
package imagesearch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	localnlp "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/nlp/local"
)

// TestMain installs the same repository lexicon the composition root loads,
// because the local NLP extractor resolves stop/function words through
// linguistics.DefaultLexicon(). No test-only word lists are allowed.
func TestMain(m *testing.M) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../../config/lexicons"))
	registry, err := linguistics.NewLexiconRegistry(root)
	if err != nil {
		panic(err)
	}
	if err := linguistics.SetDefaultLexicon(registry); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// wantEntity is one expected typed entity. Canonical is asserted only when
// non-empty (value entities like MONEY carry no canonical expectation).
type wantEntity struct {
	typ       string
	text      string
	canonical string
}

// goldenCase is one battery sentence with its full expected surface.
type goldenCase struct {
	id             string
	text           string
	prior          []string
	wantRequired   bool
	wantQueries    []string // exact ordered query list (nil when no image search)
	wantEntities   []wantEntity
	wantNegated    []wantEntity
	wantVisual     []wantEntity
	wantContexts   []wantEntity
	forbidQueries  []string // must never appear in any query (case-insensitive)
	forbidEntities []wantEntity
	wantPhrases    []string // must be present in ImportantPhrases
}
func TestGoldenBattery_ImageSearch(t *testing.T) {
	runBattery(t, "en", goldenCases())
}

// TestGoldenBattery_ImageSearch_IT runs the Italian battery (same canonical
// queries/entities/ids, Italian input).
func TestGoldenBattery_ImageSearch_IT(t *testing.T) {
	runBattery(t, "it", goldenCasesIT())
}

// runBattery runs the whole battery for one language, asserts the
// certification floor, and prints the per-sentence metric table.
func runBattery(t *testing.T, lang string, cases []goldenCase) {
	resolver := NewResolver(localnlp.NewExtractor())

	var rows []caseMetrics
	var noImageRows []caseMetrics
	for _, gc := range cases {
		t.Run(lang+":"+gc.id, func(t *testing.T) {
			row := runGoldenCase(t, resolver, gc, lang)
			rows = append(rows, row)
			if !gc.wantRequired {
				noImageRows = append(noImageRows, row)
			}
		})
	}

	// ── Aggregate metrics ────────────────────────────────────────────
	var totalExpected, totalFound, totalFP, canonicalChecked, canonicalOK int
	for _, row := range rows {
		totalExpected += row.expected
		totalFound += row.found
		totalFP += len(row.falsePositives)
		canonicalChecked += row.canonicalChecked
		canonicalOK += row.canonicalOK
	}

	noImageTotal := len(noImageRows)
	noImageOK := 0
	for _, row := range noImageRows {
		if row.noImageOK {
			noImageOK++
		}
	}

	recall := float64(totalFound) / float64(totalExpected)
	precision := float64(totalFound) / float64(totalFound+totalFP)
	canonicalAccuracy := float64(canonicalOK) / float64(canonicalChecked)

	var wrongIdentity, negatedSeen int
	for _, row := range rows {
		wrongIdentity += row.wrongIdentity
		negatedSeen += row.negatedSeen
	}

	// ── Report ───────────────────────────────────────────────────────
	var report strings.Builder
	report.WriteString("\n===== GOLDEN BATTERY: IMAGE SEARCH [" + lang + "] =====\n")
	report.WriteString(fmt.Sprintf("%-10s %-10s %-8s %-8s %-8s\n", "ID", "required", "entities", "queries", "verdict"))
	for _, row := range rows {
		verdict := "PASS"
		if !row.pass() {
			verdict = "FAIL"
		}
		report.WriteString(fmt.Sprintf("%-10s %-10v %-8d %-8d %s\n", row.id, row.requiredOK, row.found, row.queries, verdict))
	}
	report.WriteString("\n--- metrics ---\n")
	report.WriteString(fmt.Sprintf("entity recall                 = %.4f (%d/%d)\n", recall, totalFound, totalExpected))
	report.WriteString(fmt.Sprintf("entity precision              = %.4f (%d/%d)\n", precision, totalFound, totalFound+totalFP))
	report.WriteString(fmt.Sprintf("canonicalization accuracy     = %.4f (%d/%d)\n", canonicalAccuracy, canonicalOK, canonicalChecked))
	report.WriteString(fmt.Sprintf("no-image decision accuracy    = %.4f (%d/%d)\n", float64(noImageOK)/float64(noImageTotal), noImageOK, noImageTotal))
	report.WriteString(fmt.Sprintf("wrong-identity selections     = %d\n", wrongIdentity))
	report.WriteString(fmt.Sprintf("negated-person selections     = %d\n", negatedSeen))
	report.WriteString("CERTIFICATION THRESHOLDS: recall>=0.95 precision>=0.95 canonical>=0.98 no-image=1.0 wrong=0 negated=0\n")
	t.Log(report.String())

	// ── Certification floor (the spec's initial targets) ─────────────
	require.GreaterOrEqual(t, recall, 0.95, "entity recall must meet the certification floor")
	require.GreaterOrEqual(t, precision, 0.95, "entity precision must meet the certification floor")
	require.GreaterOrEqual(t, canonicalAccuracy, 0.98, "canonicalization accuracy must meet the certification floor")
	require.Equal(t, noImageTotal, noImageOK, "every abstract sentence must decide no-image correctly (T24/T25/T26)")
	require.Zero(t, wrongIdentity, "wrong-identity selections must be zero (T06/T18/T19/T20/T21/T22/T23)")
	require.Zero(t, negatedSeen, "negated-person selections must be zero (T27)")
	for _, row := range rows {
		require.True(t, row.pass(), "battery case %s must pass: %+v", row.id, row)
	}
}

// TestGoldenBattery_FullParagraph certifies the complete English paragraph
// the spec highlights as the closest to real PipelineGen input, threading
// pronoun coreference across sentences.
func TestGoldenBattery_FullParagraph(t *testing.T) {
	assertParagraph(t, "en", []string{
		"Floyd Mayweather built one of boxing's most recognizable brands.",
		"After earning enormous purses from fights against Manny Pacquiao and other opponents, he expanded Mayweather Promotions and invested in real estate.",
		"His financial success turned him from a championship boxer into a global businessman.",
	})
}

// TestGoldenBattery_FullParagraph_IT certifies the same paragraph in
// Italian, where sentence 2 exercises the pro-drop subject ("ha ampliato …"
// = "he expanded …") and sentence 3 the article+possessive opener ("Il suo
// successo …" = "His success …").
func TestGoldenBattery_FullParagraph_IT(t *testing.T) {
	assertParagraph(t, "it", []string{
		"Floyd Mayweather ha costruito uno dei marchi più riconoscibili della boxe.",
		"Dopo aver guadagnato enormi borse da combattimento contro Manny Pacquiao e altri avversari, ha ampliato Mayweather Promotions e ha investito nel settore immobiliare.",
		"Il suo successo finanziario lo ha trasformato da pugile campione in imprenditore globale.",
	})
}

// assertParagraph runs a three-sentence paragraph through the resolver with
// coreference threading and asserts the per-sentence surfaces plus the
// aggregate semantic extraction. The expected queries and canonical ids are
// language-invariant.
func assertParagraph(t *testing.T, lang string, paragraph []string) {
	resolver := NewResolver(localnlp.NewExtractor())

	var prior []string
	aggregate := map[string]bool{}
	for i, sentence := range paragraph {
		dec := resolver.Resolve(context.Background(), Request{Text: sentence, Language: lang, PriorPersons: prior})
		require.True(t, dec.Required, "sentence %d must require an image search", i+1)
		require.NotNil(t, dec.Primary, "sentence %d must have a primary entity", i+1)

		for _, e := range dec.Entities {
			aggregate[entityKey(e.Type, e.Text)] = true
		}

		switch i {
		case 0:
			require.Equal(t, "person:floyd-mayweather", dec.Primary.CanonicalID, "sentence 1 primary")
			require.Equal(t, []string{"Floyd Mayweather"}, dec.Queries, "sentence 1 queries")
		case 1:
			// "he" / the dropped subject must resolve to Floyd Mayweather
			// even though the name is not in the sentence.
			require.Equal(t, "person:floyd-mayweather", dec.Primary.CanonicalID, "sentence 2 primary must be the coreference-resolved Floyd Mayweather")
			require.Equal(t, []string{"Floyd Mayweather", "Manny Pacquiao", "Mayweather Promotions", "real estate"}, dec.Queries, "sentence 2 queries")
			require.Equal(t, "MONEY", dec.Visual[0].Type, "sentence 2 must carry the money visual (enormous purses / enormi borse)")
		case 2:
			require.Equal(t, "person:floyd-mayweather", dec.Primary.CanonicalID, "sentence 3 primary")
			require.Equal(t, []string{"Floyd Mayweather"}, dec.Queries, "sentence 3 queries")
		}

		// Thread the resolved primary forward as the coreference antecedent.
		if dec.Primary != nil {
			prior = append([]string{dec.Primary.Text}, prior...)
		}
	}

	// The paragraph's expected semantic extraction (per the spec):
	// Floyd Mayweather (PERSON), Manny Pacquiao (PERSON),
	// Mayweather Promotions (ORG), real estate (CATEGORY).
	for _, want := range []wantEntity{
		{"PERSON", "Floyd Mayweather", "person:floyd-mayweather"},
		{"PERSON", "Manny Pacquiao", "person:manny-pacquiao"},
		{"ORG", "Mayweather Promotions", "org:mayweather-promotions"},
		{"CATEGORY", "real estate", "category:real-estate"},
	} {
		require.True(t, aggregate[entityKey(want.typ, want.text)], "paragraph must extract %s %q", want.typ, want.text)
	}
}
