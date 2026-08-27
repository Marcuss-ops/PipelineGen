// Package adapters bridges script entity extraction to the Ollama backend.
package adapters

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"

	scriptadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	localnlp "github.com/Marcuss-ops/PipelineGen/internal/platform/nlp"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// OllamaEntityExtractorAdapter is the sole bridge between the script port and
// the real Ollama entity-extraction backend.
type OllamaEntityExtractorAdapter struct {
	client *client.Client
}

const segmentUnderstandingRole = models.RoleSegmentUnderstanding

func NewOllamaEntityExtractorAdapter(c *client.Client) scriptadapters.EntityExtractor {
	return &OllamaEntityExtractorAdapter{client: c}
}

func (a *OllamaEntityExtractorAdapter) ExtractEntities(ctx context.Context, req script.EntityExtractionRequest) (*script.EntityResult, error) {
	if a.client == nil {
		return nil, scriptadapters.ErrEntityExtractorUnavailable
	}

	segments := splitIntoSegments(req.Text)
	if len(segments) == 0 {
		return &script.EntityResult{}, nil
	}

	entityCount := req.EntityCount
	if entityCount <= 0 {
		entityCount = 5
	}
	analysis, err := a.client.ExtractEntitiesFromScriptWithModelAndLanguage(ctx, segments, entityCount, selectedEntityModel(), req.Language)
	if err != nil {
		return nil, err
	}
	if analysis == nil {
		return &script.EntityResult{}, nil
	}

	result := &script.EntityResult{
		Persons:          []script.Entity{},
		Places:           []script.Entity{},
		Concepts:         []script.Entity{},
		ArtlistPhrases:   []string{},
		ImportantPhrases: []string{},
		ImportantWords:   []string{},
	}
	seenEntities := make(map[string]struct{})

	for _, seg := range analysis.SegmentEntities {
		// The model is useful for concepts and editorial insights, but its
		// named-entity output is not authoritative: small models may split a
		// multi-word name into token-sized fragments. Resolve spans from the
		// source text first, then use those canonical candidates to suppress
		// contained model fragments.
		deterministic, _ := localnlp.NewExtractor().ExtractEntities(ctx, script.EntityExtractionRequest{
			Text: seg.SegmentText, Language: req.Language, EntityCount: req.EntityCount,
		})
		canonicalValues := deterministicEntityValues(deterministic)
		if deterministic != nil {
			appendDeterministicEntities(result, seenEntities, deterministic)
		}
		for _, rawName := range seg.NomiSpeciali {
			kind, value := parseTypedEntity(rawName)
			if value == "" {
				continue
			}
			if isContainedEntityFragment(value, canonicalValues) {
				continue
			}
			switch kind {
			case "PERSON":
				appendUniqueEntity(&result.Persons, seenEntities, kind, value, 0.98)
			case "PLACE", "LOCATION", "CITY", "COUNTRY":
				appendUniqueEntity(&result.Places, seenEntities, "PLACE", value, 0.98)
			default:
				appendUniqueEntity(&result.Concepts, seenEntities, kind, value, 0.95)
			}
		}

		// entity_senza_testo contains identifiable visual subjects mapped to
		// precise visual descriptions. Preserve the subjects as concepts and
		// promote the descriptions to first-class Artlist/image search phrases.
		for subject, description := range seg.EntitaSenzaTesto {
			subject = strings.TrimSpace(subject)
			description = strings.TrimSpace(description)
			if subject != "" {
				appendUniqueEntity(&result.Concepts, seenEntities, "VISUAL_SUBJECT", subject, 0.92)
			}
			if description != "" {
				result.ArtlistPhrases = append(result.ArtlistPhrases, description)
			}
		}

		for _, concept := range seg.ParoleImportanti {
			concept = strings.TrimSpace(concept)
			if concept == "" {
				continue
			}
			appendUniqueEntity(&result.Concepts, seenEntities, "KEYWORD", concept, 0.90)
			result.ImportantWords = append(result.ImportantWords, concept)
		}
		for _, phrase := range seg.FrasiImportanti {
			if phrase = strings.TrimSpace(phrase); phrase != "" {
				result.ImportantPhrases = append(result.ImportantPhrases, phrase)
			}
		}
		for _, phrase := range seg.ArtlistPhrases {
			if phrase = strings.TrimSpace(phrase); phrase != "" {
				result.ArtlistPhrases = append(result.ArtlistPhrases, phrase)
			}
		}
	}

	result.ArtlistPhrases = sliceutil.UniqueStrings(result.ArtlistPhrases)
	result.ImportantPhrases = sliceutil.UniqueStrings(result.ImportantPhrases)
	result.ImportantWords = sliceutil.UniqueStrings(result.ImportantWords)

	if rawBytes, marshalErr := json.Marshal(analysis); marshalErr == nil {
		result.Raw = string(rawBytes)
	}
	return result, nil
}

func deterministicEntityValues(result *script.EntityResult) []string {
	if result == nil {
		return nil
	}
	values := make([]string, 0, len(result.Persons)+len(result.Places)+len(result.Concepts))
	for _, group := range [][]script.Entity{result.Persons, result.Places, result.Concepts} {
		for _, entity := range group {
			if entity.Type != "KEYWORD" && strings.TrimSpace(entity.Value) != "" {
				values = append(values, entity.Value)
			}
		}
	}
	return values
}

func appendDeterministicEntities(dst *script.EntityResult, seen map[string]struct{}, source *script.EntityResult) {
	for _, entity := range source.Persons {
		appendUniqueEntity(&dst.Persons, seen, "PERSON", entity.Value, entity.Score)
	}
	for _, entity := range source.Places {
		kind := entity.Type
		if kind == "GPE" {
			kind = "PLACE"
		}
		appendUniqueEntity(&dst.Places, seen, kind, entity.Value, entity.Score)
	}
	for _, entity := range source.Concepts {
		if entity.Type == "KEYWORD" {
			continue
		}
		appendUniqueEntity(&dst.Concepts, seen, entity.Type, entity.Value, entity.Score)
	}
}

func isContainedEntityFragment(value string, canonical []string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, full := range canonical {
		full = strings.TrimSpace(full)
		if strings.EqualFold(value, full) {
			return true
		}
		if len([]rune(full)) <= len([]rune(value)) {
			continue
		}
		lowerFull, lowerValue := strings.ToLower(full), strings.ToLower(value)
		for _, token := range strings.Fields(lowerFull) {
			if token == lowerValue {
				return true
			}
		}
	}
	return false
}

// ExtractEntitiesBatch is the bounded implementation of the canonical entity
// extraction operation. The processor discovers it without requiring a second
// public port, so legacy test and fallback adapters remain valid.
func (a *OllamaEntityExtractorAdapter) ExtractEntitiesBatch(ctx context.Context, reqs []script.EntityExtractionRequest) ([]script.EntityExtractionBatchResult, error) {
	if a.client == nil {
		return nil, scriptadapters.ErrEntityExtractorUnavailable
	}
	if len(reqs) == 0 {
		return nil, nil
	}
	texts := make([]string, len(reqs))
	entityCount := 5
	language := ""
	for i, req := range reqs {
		texts[i] = req.Text
		if req.EntityCount > 0 {
			entityCount = req.EntityCount
		}
		if language == "" {
			language = req.Language
		}
	}
	results, err := a.client.ExtractEntitiesFromBatchWithModel(ctx, texts, entityCount, selectedEntityModel(), language)
	if err != nil {
		return nil, err
	}
	batch := make([]script.EntityExtractionBatchResult, 0, len(reqs))
	for i, result := range results {
		batch = append(batch, script.EntityExtractionBatchResult{
			SegmentID: reqs[i].SegmentID, SegmentIndex: i,
			Result: entityResultFromAnalysis(result),
		})
	}
	return batch, nil
}

func selectedEntityModel() string {
	// OLLAMA_ENTITY_MODEL remains the highest-precedence compatibility
	// override for existing deployments and benchmarks. Without it, the
	// model is resolved by responsibility from the canonical registry.
	if model := strings.TrimSpace(os.Getenv("OLLAMA_ENTITY_MODEL")); model != "" {
		return model
	}
	return modelForRole(segmentUnderstandingRole).ID
}

func modelForRole(role models.Role) models.Model {
	for _, model := range models.Canonical() {
		if model.Role == role {
			return model
		}
	}
	// The canonical registry always contains segment_understanding. Keep
	// this fail-safe source-bound and visible if registry wiring drifts.
	return models.SegmentUnderstanding
}

func entityResultFromAnalysis(result *detail.EntityExtractionResult) *script.EntityResult {
	if result == nil {
		return &script.EntityResult{}
	}
	out := &script.EntityResult{
		ArtlistPhrases:   append([]string(nil), result.ArtlistPhrases...),
		ImportantPhrases: append([]string(nil), result.FrasiImportanti...),
		// ImportantWords is populated only by the trimming loop below so each
		// word is emitted exactly once (never duplicated by a second copy).
		ImportantWords: make([]string, 0, len(result.ParoleImportanti)),
	}
	for _, raw := range result.NomiSpeciali {
		kind, value := parseTypedEntity(raw)
		if value == "" {
			continue
		}
		entity := script.Entity{Value: value, Type: kind, Score: 0.98}
		switch kind {
		case "PERSON":
			out.Persons = append(out.Persons, entity)
		case "PLACE", "LOCATION", "CITY", "COUNTRY":
			entity.Type = "PLACE"
			out.Places = append(out.Places, entity)
		default:
			out.Concepts = append(out.Concepts, entity)
		}
	}
	for subject, description := range result.EntitaSenzaTesto {
		if strings.TrimSpace(subject) != "" {
			out.Concepts = append(out.Concepts, script.Entity{Value: strings.TrimSpace(subject), Type: "VISUAL_SUBJECT", Score: 0.92})
		}
		if strings.TrimSpace(description) != "" {
			out.ArtlistPhrases = append(out.ArtlistPhrases, strings.TrimSpace(description))
		}
	}
	for _, word := range result.ParoleImportanti {
		out.ImportantWords = append(out.ImportantWords, strings.TrimSpace(word))
	}
	return out
}

// parseTypedEntity accepts the new "TYPE: value" contract while remaining
// backward compatible with older untyped cache/model responses.
func parseTypedEntity(raw string) (kind, value string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "[") {
		if end := strings.Index(raw, "]"); end > 1 {
			kind = normalizeEntityKind(raw[1:end])
			value = normalizeExtractedEntityValue(raw[end+1:])
			if kind != "" && value != "" {
				return kind, value
			}
		}
	}
	for _, separator := range []string{":", "|"} {
		if before, after, ok := strings.Cut(raw, separator); ok {
			candidateKind := normalizeEntityKind(before)
			candidateValue := normalizeExtractedEntityValue(after)
			if candidateKind != "" && candidateValue != "" {
				return candidateKind, candidateValue
			}
		}
	}
	// Untyped legacy names stay searchable but are not falsely labelled as
	// people. The new prompt emits explicit types for high-confidence routing.
	return "CONCEPT", normalizeExtractedEntityValue(raw)
}

func normalizeExtractedEntityValue(value string) string {
	return strings.Trim(strings.TrimSpace(value), ".,;:!?\"'’”)]}>")
}

func normalizeEntityKind(raw string) string {
	kind := strings.ToUpper(strings.TrimSpace(raw))
	switch kind {
	case "PERSON", "PLACE", "LOCATION", "CITY", "COUNTRY", "ORGANIZATION", "ORG", "EVENT", "WORK", "PRODUCT", "OTHER", "CONCEPT":
		if kind == "ORG" {
			return "ORGANIZATION"
		}
		return kind
	default:
		return ""
	}
}

func appendUniqueEntity(dst *[]script.Entity, seen map[string]struct{}, kind, value string, score float32) {
	kind = strings.ToUpper(strings.TrimSpace(kind))
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	key := kind + "\x00" + strings.ToLower(value)
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*dst = append(*dst, script.Entity{Value: value, Type: kind, Score: score})
}

func splitIntoSegments(text string) []string {
	raw := strings.Split(strings.TrimSpace(text), "\n\n")
	segments := make([]string, 0, len(raw))
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			segments = append(segments, s)
		}
	}
	return segments
}

var _ scriptadapters.EntityExtractor = (*OllamaEntityExtractorAdapter)(nil)
