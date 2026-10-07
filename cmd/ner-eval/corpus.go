package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

type corpus struct {
	Version              string       `json:"version"`
	AnnotationMethod     string       `json:"annotation_method"`
	AnnotationGuidelines string       `json:"annotation_guidelines"`
	Annotators           []string     `json:"annotators"`
	Cases                []corpusCase `json:"cases"`
}
type corpusCase struct {
	ID       string       `json:"id"`
	Language string       `json:"language"`
	Text     string       `json:"text"`
	Entities []goldEntity `json:"entities"`
}
type goldEntity struct {
	Text  string `json:"text"`
	Label string `json:"label"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

var supportedNERLanguages = map[string]struct{}{"en": {}, "it": {}, "es": {}, "pt": {}, "fr": {}, "de": {}}

// loadCorpus reads, decodes and validates the externally annotated gold corpus
// and then enforces the backend input limit plus the pass-count overflow guard.
// The raw bytes are returned so the caller can fingerprint the exact source
// document that produced the report.
func loadCorpus(path string, warmup, iterations int) (corpus, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return corpus{}, nil, fmt.Errorf("read corpus: %w", err)
	}
	var gold corpus
	if err := json.Unmarshal(data, &gold); err != nil {
		return corpus{}, nil, fmt.Errorf("decode corpus: %w", err)
	}
	if err := validateCorpus(gold); err != nil {
		return corpus{}, nil, fmt.Errorf("invalid corpus: %w", err)
	}
	if (warmup > 0 && len(gold.Cases) > math.MaxInt/warmup) || len(gold.Cases) > math.MaxInt/iterations {
		return corpus{}, nil, errors.New("corpus size multiplied by pass count overflows evaluation call count")
	}
	for _, item := range gold.Cases {
		if utf8.RuneCountInString(item.Text) > 100_000 {
			return corpus{}, nil, fmt.Errorf("invalid corpus: case %q text exceeds backend limit of 100000 Unicode codepoints", item.ID)
		}
	}
	return gold, data, nil
}

func validateCorpus(data corpus) error {
	if data.Version != corpusVersion {
		return fmt.Errorf("version must be %q", corpusVersion)
	}
	if data.AnnotationMethod != "human_double_annotation_adjudicated" {
		return errors.New("annotation_method must be human_double_annotation_adjudicated")
	}
	if strings.TrimSpace(data.AnnotationGuidelines) == "" {
		return errors.New("annotation_guidelines version is required")
	}
	annotators := make(map[string]struct{}, len(data.Annotators))
	for _, annotator := range data.Annotators {
		if id := strings.TrimSpace(annotator); id != "" {
			annotators[id] = struct{}{}
		}
	}
	if len(annotators) < 2 {
		return errors.New("at least two distinct human annotators are required")
	}
	if data.Cases == nil {
		return errors.New("cases must be provided as an array")
	}
	if len(data.Cases) < 600 {
		return fmt.Errorf("corpus must contain at least 600 human-adjudicated scenes; got %d", len(data.Cases))
	}
	if len(data.Cases) == 0 {
		return errors.New("cases must not be empty")
	}
	seen := make(map[string]struct{}, len(data.Cases))
	languageCounts := make(map[string]int, len(supportedNERLanguages))
	for index, item := range data.Cases {
		id := strings.TrimSpace(item.ID)
		if id == "" || strings.TrimSpace(item.Language) == "" || item.Text == "" || !utf8.ValidString(item.Text) {
			return fmt.Errorf("case[%d] requires id, language, and valid UTF-8 text", index)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate case id %q", id)
		}
		seen[id] = struct{}{}
		language := languageKey(item.Language)
		if _, ok := supportedNERLanguages[language]; !ok {
			return fmt.Errorf("case %q has unsupported language %q", id, item.Language)
		}
		languageCounts[language]++
		for entityIndex, entity := range item.Entities {
			label := normalizeLabel(entity.Label)
			if !validEvaluatorLabel(entity.Label) || label == "" || entity.Start < 0 || entity.End <= entity.Start || entity.End > len(item.Text) || !utf8.RuneStart(item.Text[entity.Start]) || entity.End < len(item.Text) && !utf8.RuneStart(item.Text[entity.End]) {
				return fmt.Errorf("case %q entity[%d] has invalid label or byte offsets", id, entityIndex)
			}
			if item.Text[entity.Start:entity.End] != entity.Text {
				return fmt.Errorf("case %q entity[%d] text does not match its gold span", id, entityIndex)
			}
		}
	}
	for language := range supportedNERLanguages {
		if languageCounts[language] < 100 {
			return fmt.Errorf("corpus requires at least 100 annotated scenes per supported language; %s has %d", language, languageCounts[language])
		}
	}
	return nil
}

func validEvaluatorLabel(value string) bool { return normalizeLabel(value) != "" }

func normalizeLabel(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "PER", "PERSON":
		return "PERSON"
	case "ORG", "ORGANIZATION", "COMPANY", "CORP", "CORPORATION", "BUSINESS":
		return "ORG"
	case "GPE", "LOC", "LOCATION", "PLACE", "CITY", "COUNTRY":
		return "GPE"
	case "LOGO":
		return "LOGO"
	case "BRAND":
		return "ORG"
	case "DATE", "DATETIME", "CALENDAR_DATE":
		return "DATE"
	case "TIME", "CARDINAL", "NUMBER", "NUM", "METRIC", "METRICS", "STATISTIC", "STATISTICS", "QUANTITY", "ORDINAL", "MONEY", "PERCENT", "PERCENTAGE":
		return script.NormalizeAnnotationType(value)
	case "QUOTE":
		return "QUOTE"
	case "EVENT":
		return "EVENT"
	case "PRODUCT":
		return "PRODUCT"
	case "WORK", "WORK_OF_ART":
		return "WORK_OF_ART"
	case "CONCEPT", "VISUAL_CONCEPT", "MISC", "NORP", "LAW", "LANGUAGE":
		return "CONCEPT"
	default:
		return ""
	}
}
