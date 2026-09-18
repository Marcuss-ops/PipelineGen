package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

type translatedNLPWork struct {
	sceneIndex int
	lang       Language
	text       string
}

// translatedNLPOutcome contains named-entity evidence returned by VisualNER.
// Editorial phrases and words are selected later by deterministic local rules
// over the exact translated text.
type translatedNLPOutcome struct {
	entities []VisualEntity
}

// runTranslatedNLP extracts translated names/entities with VisualNER and
// selects important phrases from translated text using deterministic local
// rules. Source annotations remain untouched; every translated surface gets
// its own grounded spans so overlay timing uses that language's TTS artifact.
// No phrase-extraction model request is made.
func (r *Runner) runTranslatedNLP(ctx context.Context, req GenerateRequest, result *GenerateResult) error {
	if r == nil || result == nil || r.vidRushPipeline == nil {
		return nil
	}
	extraction := req.MediaPlan.Extraction
	includeEntities := extraction.Includes(mediadomain.ExtractionIncludeEntities) || extraction.Includes(mediadomain.ExtractionIncludeSpecialNames)
	includePhrases := extraction.Includes(mediadomain.ExtractionIncludeImportantPhrases)
	includeWords := extraction.Includes(mediadomain.ExtractionIncludeImportantWords)
	if !includeEntities && !includePhrases && !includeWords {
		return nil
	}

	langs := make([]Language, 0, len(req.Languages)+1)
	seenLang := make(map[Language]struct{}, len(req.Languages)+1)
	for _, lang := range append([]Language{req.SourceLanguage}, req.Languages...) {
		if lang != "" {
			if _, exists := seenLang[lang]; !exists {
				seenLang[lang] = struct{}{}
				langs = append(langs, lang)
			}
		}
	}

	work := make([]translatedNLPWork, 0)
	for sceneIndex := range result.Scenes {
		for _, lang := range langs {
			if lang == req.SourceLanguage {
				continue
			}
			text := strings.TrimSpace(result.Scenes[sceneIndex].Text[lang])
			if text != "" {
				work = append(work, translatedNLPWork{sceneIndex: sceneIndex, lang: lang, text: text})
			}
		}
	}
	if len(work) == 0 {
		return nil
	}

	entityLimit := extraction.MaxEntitiesPerSegment
	if entityLimit <= 0 {
		entityLimit = 3
	}
	phraseLimit := extraction.MaxImportantPhrasesPerSegment
	if phraseLimit <= 0 {
		phraseLimit = 3
	}
	wordLimit := extraction.MaxImportantWordsPerSegment
	if wordLimit <= 0 {
		wordLimit = 3
	}
	workers := DefaultNLPConcurrency
	if limit := r.vidRushPipeline.Backpressure.ExtractionLimit; limit > 0 && limit < workers {
		workers = limit
	}
	outcomes, err := concurrent.Map(ctx, work, workers, func(opCtx context.Context, _ int, item translatedNLPWork) (translatedNLPOutcome, error) {
		var outcome translatedNLPOutcome
		if includeEntities && r.vidRushPipeline.NERPort != nil {
			err := kernobs.MeasureOperation(opCtx, kernobs.OperationInfo{
				Stage: kernobs.StageName("scene_analysis"), Component: kernobs.ComponentNLP, Operation: kernobs.OperationExtract,
				Provider: string(item.lang), MetadataJSON: fmt.Sprintf("{\"scene_id\":%q,\"language\":%q,\"surface\":\"translation\"}", result.Scenes[item.sceneIndex].ID, item.lang),
			}, func(measureCtx context.Context) error {
				var extractErr error
				outcome.entities, extractErr = r.vidRushPipeline.NERPort.Extract(measureCtx, item.text, entityLimit)
				return extractErr
			})
			if err != nil {
				return translatedNLPOutcome{}, fmt.Errorf("translated NER for scene %s/%s failed: %w", result.Scenes[item.sceneIndex].ID, item.lang, err)
			}
		}
		return outcome, nil
	})
	if err != nil {
		return err
	}

	// Grounding and annotation projection are pure CPU work over each
	// translation's own text. Source entity matches carry stable identity only;
	// phrase surfaces are always selected from the localized narration.
	for index, item := range work {
		// Treat already-extracted source names as identity hints only. A hint is
		// copied into the localized annotations only when its name can be found
		// in this translated scene, so it cannot invent a mention or a phrase.
		// The SAME matches also carry the source identity, which the localized
		// annotation inherits below instead of re-minting one from the
		// translated surface.
		sourceMatches := matchLocalizedSourceEntities(item.text, string(item.lang), result.Scenes[item.sceneIndex].Annotations)
		outcomes[index].entities = mergeTranslatedNamedEntities(outcomes[index].entities, localizedSourceVisualEntities(sourceMatches))
		outcomes[index].entities = limitTranslatedVisualEntities(outcomes[index].entities, entityLimit)
		phraseCandidates := deterministicImportantPhrases(item.text, outcomes[index].entities, phraseLimit, string(item.lang))
		var groundedPhrases []string
		if includePhrases {
			groundedPhrases = groundImportantPhrases(item.text, outcomes[index].entities, phraseCandidates, phraseLimit)
		}
		var importantWords []string
		if includeWords {
			importantWords = deterministicImportantWords(phraseCandidates, wordLimit, string(item.lang))
		}
		insights := scriptpkg.SegmentInsights{
			SegmentID:        result.Scenes[item.sceneIndex].ID,
			TextHash:         SceneTextHash(item.text),
			ImportantPhrases: groundedPhrases,
			ImportantWords:   importantWords,
			SpecialNames:     translatedSpecialNames(item.text, nil, outcomes[index].entities, entityLimit),
		}
		for _, entity := range outcomes[index].entities {
			entityType := entity.Type
			if strings.TrimSpace(string(entityType)) == "" {
				entityType = scriptpkg.EntityTypeVisualConcept
			}
			insights.Entities = append(insights.Entities, scriptpkg.ExtractedEntity{
				Value: entity.Text, Type: string(entityType), Confidence: float64(entity.Score),
			})
		}
		annotation := projectEntityAnnotations(item.text, string(item.lang), scriptpkg.VidRushSegmentResult{
			SegmentID: result.Scenes[item.sceneIndex].ID,
			SceneID:   result.Scenes[item.sceneIndex].ID,
			Position:  result.Scenes[item.sceneIndex].Index,
			Text:      item.text,
			TextHash:  SceneTextHash(item.text),
			Insights:  insights,
		})
		if annotation == nil {
			continue
		}
		stampLocalizedSourceIdentity(annotation, sourceMatches)
		if result.Scenes[item.sceneIndex].LocalizedAnnotations == nil {
			result.Scenes[item.sceneIndex].LocalizedAnnotations = make(map[Language]*scriptpkg.SceneAnnotations)
		}
		annotation.Language = string(item.lang)
		result.Scenes[item.sceneIndex].LocalizedAnnotations[item.lang] = annotation
	}
	return nil
}

func translatedSpecialNames(text string, candidates []string, entities []VisualEntity, limit int) []string {
	var out []string
	seen := make(map[string]struct{})
	appendName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	// A special name must correspond to a typed, source-grounded entity.
	// This also replaces partial model spans ("Las", "Vegas") with the
	// complete localized entity and rejects German common nouns that were
	// surfaced by capitalization-only fallback.
	for _, entity := range entities {
		if !isNamedVisualEntity(entity.Type) {
			continue
		}
		if span, ok := findEntitySpan(text, entity.Text); ok {
			appendName(span.Text)
		}
	}
	// Keep model candidates only when they overlap a typed entity. The entity
	// surface is emitted above, so candidates never introduce partial aliases.
	for _, candidate := range candidates {
		span, ok := findEntitySpan(text, candidate)
		if !ok {
			continue
		}
		for _, entity := range entities {
			if !isNamedVisualEntity(entity.Type) {
				continue
			}
			entitySpan, entityOK := findEntitySpan(text, entity.Text)
			if entityOK && span.StartRune < entitySpan.EndRune && entitySpan.StartRune < span.EndRune {
				appendName(entitySpan.Text)
				break
			}
		}
	}
	return limitTranslatedNLPStrings(out, limit)
}

func isNamedVisualEntity(kind scriptpkg.EntityType) bool {
	switch kind {
	case scriptpkg.EntityTypePerson, scriptpkg.EntityTypeLocation, scriptpkg.EntityTypeOrganization,
		scriptpkg.EntityTypeEvent, scriptpkg.EntityTypeWork, scriptpkg.EntityTypeProduct:
		return true
	default:
		return false
	}
}

func localizedSourceEntityType(raw string) scriptpkg.EntityType {
	switch scriptpkg.NormalizeAnnotationType(raw) {
	case "PERSON":
		return scriptpkg.EntityTypePerson
	case "GPE", "LOCATION":
		return scriptpkg.EntityTypeLocation
	case "ORG", "ORGANIZATION":
		return scriptpkg.EntityTypeOrganization
	case "EVENT":
		return scriptpkg.EntityTypeEvent
	case "WORK":
		return scriptpkg.EntityTypeWork
	case "PRODUCT":
		return scriptpkg.EntityTypeProduct
	default:
		return ""
	}
}

func limitTranslatedVisualEntities(entities []VisualEntity, limit int) []VisualEntity {
	if limit <= 0 || len(entities) <= limit {
		return entities
	}
	priority := []scriptpkg.EntityType{
		scriptpkg.EntityTypePerson, scriptpkg.EntityTypeLocation, scriptpkg.EntityTypeOrganization,
		scriptpkg.EntityTypeEvent, scriptpkg.EntityTypeWork, scriptpkg.EntityTypeProduct,
	}
	out := make([]VisualEntity, 0, limit)
	used := make([]bool, len(entities))
	for _, kind := range priority {
		for i, entity := range entities {
			if used[i] || entity.Type != kind {
				continue
			}
			out = append(out, entity)
			used[i] = true
			if len(out) == limit {
				return out
			}
		}
	}
	for i, entity := range entities {
		if used[i] {
			continue
		}
		out = append(out, entity)
		if len(out) == limit {
			break
		}
	}
	return out
}

func mergeTranslatedNamedEntities(visual, named []VisualEntity) []VisualEntity {
	if len(named) == 0 {
		return visual
	}
	out := make([]VisualEntity, 0, len(visual)+len(named))
	for _, entity := range visual {
		switch entity.Type {
		case scriptpkg.EntityTypePerson, scriptpkg.EntityTypeOrganization,
			scriptpkg.EntityTypeEvent, scriptpkg.EntityTypeWork, scriptpkg.EntityTypeProduct:
			// Typed model names are the language-aware identity source. Drop
			// heuristic title-case labels when a typed extraction is available.
			// Keep deterministic location hits from VisualNER: structured model
			// name extraction can omit places even when they occur verbatim.
			continue
		}
		out = append(out, entity)
	}
	for _, candidate := range named {
		if strings.TrimSpace(candidate.Text) == "" {
			continue
		}
		duplicate := false
		for _, existing := range out {
			if existing.Type == candidate.Type && strings.EqualFold(existing.Text, candidate.Text) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, candidate)
		}
	}
	return out
}

func limitTranslatedNLPStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return append([]string(nil), values...)
}
