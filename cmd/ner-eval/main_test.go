package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

func validAnnotatedCorpus() corpus {
	languages := []string{"en", "it", "es", "pt", "fr", "de"}
	data := corpus{
		Version:              corpusVersion,
		AnnotationMethod:     "human_double_annotation_adjudicated",
		AnnotationGuidelines: "v1",
		Annotators:           []string{"annotator-a", "annotator-b"},
		Cases:                make([]corpusCase, 0, 600),
	}
	for _, language := range languages {
		for i := 0; i < 100; i++ {
			item := corpusCase{ID: fmt.Sprintf("%s-%d", language, i), Language: language, Text: "Tesla"}
			if i == 0 {
				item.Entities = []goldEntity{{Text: "Tesla", Label: "ORG", Start: 0, End: 5}}
			}
			data.Cases = append(data.Cases, item)
		}
	}
	return data
}

func TestValidateCorpusRejectsNonSourceGoldOffsets(t *testing.T) {
	valid := validAnnotatedCorpus()
	for i := range valid.Cases {
		valid.Cases[i].Text = "A company employs workers."
		valid.Cases[i].Entities = nil
	}
	valid.Cases[0].Text = "Tesla launched in 2020."
	valid.Cases[0].Entities = []goldEntity{{Text: "Tesla", Label: "ORG", Start: 0, End: 5}}
	if err := validateCorpus(valid); err != nil {
		t.Fatalf("valid 600-scene corpus rejected: %v", err)
	}
	tooSmall := valid
	tooSmall.Cases = append([]corpusCase(nil), valid.Cases[:599]...)
	if err := validateCorpus(tooSmall); err == nil || !strings.Contains(err.Error(), "at least 600") {
		t.Fatalf("small corpus error = %v", err)
	}
	valid.Cases[0].Entities[0].Text = "Elon Musk"
	if err := validateCorpus(valid); err == nil {
		t.Fatal("unanchored gold span must be rejected")
	}
}

func TestValidateCorpusRequiresDoubleAnnotationAndLanguageCoverage(t *testing.T) {
	data := validAnnotatedCorpus()
	data.Annotators = []string{"annotator-a", "annotator-a"}
	if err := validateCorpus(data); err == nil || !strings.Contains(err.Error(), "two distinct") {
		t.Fatalf("duplicate annotator error = %v", err)
	}
	data = validAnnotatedCorpus()
	for i := len(data.Cases) - 1; i >= 0; i-- {
		if data.Cases[i].Language == "de" {
			data.Cases[i].Language = "en"
			break
		}
	}
	if err := validateCorpus(data); err == nil || !strings.Contains(err.Error(), "at least 100 annotated scenes per supported language") {
		t.Fatalf("language coverage error = %v", err)
	}
}

func TestValidateCorpusRejectsNonUTF8ByteBoundary(t *testing.T) {
	data := validAnnotatedCorpus()
	data.Cases[0].Text = "😀 Tesla"
	data.Cases[0].Entities[0] = goldEntity{Text: "Tesla", Label: "ORG", Start: 1, End: 6}
	if err := validateCorpus(data); err == nil || !strings.Contains(err.Error(), "invalid label or byte offsets") {
		t.Fatalf("invalid UTF-8 boundary error = %v", err)
	}
}

func TestValidateCorpusRejectsUnsupportedAnnotationMethod(t *testing.T) {
	data := validAnnotatedCorpus()
	data.AnnotationMethod = "synthetic"
	if err := validateCorpus(data); err == nil || !strings.Contains(err.Error(), "human_double_annotation_adjudicated") {
		t.Fatalf("annotation method error = %v", err)
	}
}

func TestRunRejectsCasesAboveBackendInputLimit(t *testing.T) {
	data := validAnnotatedCorpus()
	data.Cases[0].Entities = nil
	data.Cases[0].Text = strings.Repeat("a", 100_001)
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "corpus.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	err = run([]string{"--corpus", path}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "exceeds backend limit") {
		t.Fatalf("oversize corpus error = %v", err)
	}
}

func TestNormalizeLabelUsesExplicitSharedNERVocabulary(t *testing.T) {
	for raw, want := range map[string]string{
		"PER": "PERSON", "ORG": "ORG", "LOC": "GPE", "GPE": "GPE",
		"MISC": "CONCEPT", "NORP": "CONCEPT", "LAW": "CONCEPT",
		"brand": "ORG", "logo": "LOGO", "work_of_art": "WORK_OF_ART", "PRODUCT": "PRODUCT",
		"ORGANIZATION": "ORG", "LOCATION": "GPE", "VISUAL_CONCEPT": "CONCEPT",
	} {
		if got := normalizeLabel(raw); got != want {
			t.Errorf("normalizeLabel(%q) = %q, want %q", raw, got, want)
		}
	}
	if normalizeLabel("invented_label") != "" {
		t.Fatal("unknown corpus/prediction labels must fail closed")
	}
}

func TestMetricsDistinguishExactBoundaryAndType(t *testing.T) {
	gold := map[exactKey]int{{start: 0, end: 9, label: "PERSON"}: 1}
	predicted := map[exactKey]int{{start: 0, end: 9, label: "ORG"}: 1}
	exact, boundary := &counter{}, &counter{}
	compareMultisets(gold, predicted, exact)
	goldBoundary := map[spanKey]int{{start: 0, end: 9}: 1}
	predBoundary := map[spanKey]int{{start: 0, end: 9}: 1}
	compareMultisets(goldBoundary, predBoundary, boundary)
	if got := makeScore(exact); got.TruePositive != 0 || got.FalsePositive != 1 || got.FalseNegative != 1 {
		t.Fatalf("exact score = %+v", got)
	}
	if got := makeScore(boundary); got.TruePositive != 1 || got.F1 != 1 {
		t.Fatalf("boundary score = %+v", got)
	}
}

func TestBenchmarkPassDefaultsMatchProtocol(t *testing.T) {
	if defaultNERWarmupPasses != 20 || defaultNERMeasuredPasses != 200 {
		t.Fatalf("benchmark defaults = warmup %d / measured %d", defaultNERWarmupPasses, defaultNERMeasuredPasses)
	}
}

func TestRunRequiresAnnotatedCorpus(t *testing.T) {
	if err := run(nil, &strings.Builder{}, &strings.Builder{}); err == nil {
		t.Fatal("running without the real externally annotated corpus must not succeed")
	}
}

func TestBackendFailuresCountGoldAsFalseNegatives(t *testing.T) {
	labels := make(map[string]*counter)
	languages := make(map[string]*languageAccumulator)
	exact, boundary := &counter{}, &counter{}
	item := corpusCase{Language: "fr-FR", Text: "Ada Lovelace spoke.", Entities: []goldEntity{{
		Text: "Ada Lovelace", Label: "PERSON", Start: 0, End: len("Ada Lovelace"),
	}}}
	observeBackendFailure(item, exact, boundary, labels, languages)
	if got := makeScore(exact); got.FalseNegative != 1 || got.Recall != 0 {
		t.Fatalf("exact score on backend failure = %+v", got)
	}
	if got := languages["fr"].scenes; got != 1 {
		t.Fatalf("failed scenes = %d, want one", got)
	}
}

func TestObserveWrongTypeIsFalsePositiveButNotUngroundedHallucination(t *testing.T) {
	item := corpusCase{ID: "wrong-type", Language: "en", Text: "Tesla"}
	gold := &counter{}
	boundary := &counter{}
	result := report{}
	labels := make(map[string]*counter)
	languages := make(map[string]*languageAccumulator)
	observe(item, []scriptgen.VisualEntity{{Text: "Tesla", Type: "PERSON", Start: 0, End: 5, Evidence: "Tesla"}}, gold, boundary, labels, &result, languages)
	if gold.fp != 1 || boundary.fp != 1 || result.HallucinatedEntities != 0 {
		t.Fatalf("exact/boundary/ungrounded hallucinated = %d/%d/%d", gold.fp, boundary.fp, result.HallucinatedEntities)
	}
}

func TestObserveRejectsInvalidOffsetsAsFalsePositives(t *testing.T) {
	labels := make(map[string]*counter)
	languages := make(map[string]*languageAccumulator)
	result := report{}
	item := corpusCase{ID: "unicode", Language: "fr", Text: "😀 Ada", Entities: []goldEntity{{
		Text: "Ada", Label: "PERSON", Start: len("😀 "), End: len("😀 Ada"),
	}}}
	boundary := &counter{}
	observe(item, []scriptgen.VisualEntity{{Text: "Ada", Type: "PERSON", Start: 1, End: 4}}, boundary, &counter{}, labels, &result, languages)
	if result.InvalidOffsets != 1 || result.UngroundedOutputs != 1 || result.HallucinatedEntities != 1 {
		t.Fatalf("invalid/ungrounded/hallucinated = %d/%d/%d", result.InvalidOffsets, result.UngroundedOutputs, result.HallucinatedEntities)
	}
}

func TestObserveCountsEvidenceMismatchAsHallucinationNotNERFalsePositive(t *testing.T) {
	item := corpusCase{ID: "bad-evidence", Language: "en", Text: "Tesla"}
	labels := make(map[string]*counter)
	languages := make(map[string]*languageAccumulator)
	result := report{}
	observe(item, []scriptgen.VisualEntity{{Text: "Tesla", Type: "ORG", Start: 0, End: 5, Evidence: "SpaceX"}}, &counter{}, &counter{}, labels, &result, languages)
	if result.HallucinatedEntities != 1 || result.UngroundedOutputs != 1 {
		t.Fatalf("hallucinated/ungrounded = %d/%d", result.HallucinatedEntities, result.UngroundedOutputs)
	}
}

func TestPercentileIsDeterministic(t *testing.T) {
	if got := percentile([]float64{8, 1, 5, 2, 3}, .95); got != 8 {
		t.Fatalf("p95 = %v, want 8", got)
	}
}
