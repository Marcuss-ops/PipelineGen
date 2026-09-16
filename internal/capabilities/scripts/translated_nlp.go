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

// translatedNLPOutcome is the per-(scene, language) evidence produced by the
// extraction phases. Phrases are resolved in a SECOND phase so a batched
// phrase request can serve many scenes at once; entities stay per scene because
// VisualNER is a deterministic per-text extractor with a source-span contract.
type translatedNLPOutcome struct {
	entities     []VisualEntity
	phrases      []string
	words        []string
	specialNames []string
}

// runTranslatedNLP extracts grounded names/entities and important phrases from
// every translated scene text. The source-language Annotations surface remains
// untouched because it is the overlay/media identity surface; translated
// annotations are stored separately and selected by the document language.
//
// Cost shape: entities are per (scene, language) because the visual NER is
// deterministic and local, while phrases are requested ONE batch per language
// when the configured extractor implements BatchImportantPhraseExtractor (the
// Ollama adapter does). Before batching, a 10-scene three-language run issued 30
// model calls for phrase hints; it now issues one call per chunk of scenes per
// language. When the batched interface is absent or fails, the phase falls back
// to the per-scene call so the result surface is identical either way.
func (r *Runner) runTranslatedNLP(ctx context.Context, req GenerateRequest, result *GenerateResult) error {
	if r == nil || result == nil || r.vidRushPipeline == nil || r.vidRushPipeline.NERPort == nil {
		return nil
	}
	extraction := req.MediaPlan.Extraction
	includeEntities := extraction.Includes(mediadomain.ExtractionIncludeEntities) || extraction.Includes(mediadomain.ExtractionIncludeSpecialNames)
	includePhrases := extraction.Includes(mediadomain.ExtractionIncludeImportantPhrases)
	includeWords := extraction.Includes(mediadomain.ExtractionIncludeImportantWords)
	includeSpecialNames := extraction.Includes(mediadomain.ExtractionIncludeSpecialNames)
	includeModelNLP := includePhrases || includeWords || includeSpecialNames
	if !includeEntities && !includeModelNLP {
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
	nlpLimit := max(entityLimit, phraseLimit, wordLimit)

	// ── Phase 1: entities (always per scene) + phrases only when the
	// configured extractor cannot batch. ─────────────────────────────────
	var phraseBatcher BatchImportantPhraseExtractor
	canBatchPhrases := false
	phraseExtractor := r.vidRushPipeline.PhraseExtractor
	if includePhrases && phraseExtractor != nil {
		if candidate, ok := phraseExtractor.(BatchImportantPhraseExtractor); ok {
			phraseBatcher, canBatchPhrases = candidate, true
		}
	}
	detailedExtractor, hasDetailedExtractor := phraseExtractor.(SceneNLPExtractor)
	detailedBatcher, canBatchDetailed := phraseExtractor.(BatchSceneNLPExtractor)
	useDetailedBatch := includeModelNLP && hasDetailedExtractor && canBatchDetailed

	workers := DefaultNLPConcurrency
	if limit := r.vidRushPipeline.Backpressure.ExtractionLimit; limit > 0 && limit < workers {
		workers = limit
	}
	outcomes, err := concurrent.Map(ctx, work, workers, func(opCtx context.Context, _ int, item translatedNLPWork) (translatedNLPOutcome, error) {
		var outcome translatedNLPOutcome
		err := kernobs.MeasureOperation(opCtx, kernobs.OperationInfo{
			Stage: kernobs.StageName("scene_analysis"), Component: kernobs.ComponentNLP, Operation: kernobs.OperationExtract,
			Provider: string(item.lang), MetadataJSON: fmt.Sprintf("{\"scene_id\":%q,\"language\":%q,\"surface\":\"translation\"}", result.Scenes[item.sceneIndex].ID, item.lang),
		}, func(measureCtx context.Context) error {
			var extractErr error
			if includeEntities {
				outcome.entities, extractErr = r.vidRushPipeline.NERPort.Extract(measureCtx, item.text, entityLimit)
				if extractErr != nil {
					return extractErr
				}
			}
			if includeModelNLP && hasDetailedExtractor && !useDetailedBatch {
				var extraction SceneNLPExtraction
				extraction, extractErr = detailedExtractor.ExtractSceneNLP(measureCtx, item.text, nlpLimit, string(item.lang), req.Model)
				if extractErr != nil {
					return extractErr
				}
				applyTranslatedNLPExtraction(&outcome, extraction, includePhrases, includeWords, includeSpecialNames, includeEntities)
			} else if includePhrases && !canBatchPhrases && phraseExtractor != nil {
				outcome.phrases, extractErr = phraseExtractor.ExtractImportantPhrases(measureCtx, item.text, phraseLimit, string(item.lang), req.Model)
			}
			return extractErr
		})
		if err != nil {
			return translatedNLPOutcome{}, fmt.Errorf("translated NLP for scene %s/%s failed: %w", result.Scenes[item.sceneIndex].ID, item.lang, err)
		}
		return outcome, nil
	})
	if err != nil {
		return err
	}

	// ── Phase 2: batched phrases, one request per chunk of scenes per
	// language, in the canonical (scene, language) order. ────────────────
	if useDetailedBatch {
		byLang := make(map[Language][]int, len(langs))
		for index, item := range work {
			byLang[item.lang] = append(byLang[item.lang], index)
		}
		for _, lang := range langs {
			indexes := byLang[lang]
			if len(indexes) == 0 {
				continue
			}
			texts := make([]string, len(indexes))
			for i, index := range indexes {
				texts[i] = work[index].text
			}
			var extractions []SceneNLPExtraction
			err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
				Stage: kernobs.StageName("scene_analysis"), Component: kernobs.ComponentNLP, Operation: kernobs.OperationExtract,
				Provider: string(lang), Items: int64(len(texts)),
				MetadataJSON: fmt.Sprintf("{\"language\":%q,\"surface\":\"translation_nlp_batch\",\"scenes\":%d}", lang, len(texts)),
			}, func(measureCtx context.Context) error {
				var batchErr error
				extractions, batchErr = detailedBatcher.ExtractSceneNLPBatch(measureCtx, texts, nlpLimit, string(lang), req.Model)
				return batchErr
			})
			if err != nil || len(extractions) != len(indexes) {
				// The batch path is a cost optimization. A failed or malformed
				// result falls back to the detailed per-scene contract.
				for i, index := range indexes {
					value, singleErr := detailedExtractor.ExtractSceneNLP(ctx, texts[i], nlpLimit, string(lang), req.Model)
					if singleErr != nil {
						return fmt.Errorf("translated NLP for scene %s/%s failed after batch fallback: %w", result.Scenes[work[index].sceneIndex].ID, lang, singleErr)
					}
					applyTranslatedNLPExtraction(&outcomes[index], value, includePhrases, includeWords, includeSpecialNames, includeEntities)
				}
				continue
			}
			for i, index := range indexes {
				applyTranslatedNLPExtraction(&outcomes[index], extractions[i], includePhrases, includeWords, includeSpecialNames, includeEntities)
			}
		}
	} else if !hasDetailedExtractor && includePhrases && canBatchPhrases && phraseBatcher != nil {
		byLang := make(map[Language][]int, len(langs))
		for index, item := range work {
			byLang[item.lang] = append(byLang[item.lang], index)
		}
		for _, lang := range langs {
			indexes := byLang[lang]
			if len(indexes) == 0 {
				continue
			}
			texts := make([]string, len(indexes))
			for i, index := range indexes {
				texts[i] = work[index].text
			}
			var phrases [][]string
			err := kernobs.MeasureOperation(ctx, kernobs.OperationInfo{
				Stage: kernobs.StageName("scene_analysis"), Component: kernobs.ComponentNLP, Operation: kernobs.OperationExtract,
				Provider: string(lang), Items: int64(len(texts)),
				MetadataJSON: fmt.Sprintf("{\"language\":%q,\"surface\":\"translation_batch\",\"scenes\":%d}", lang, len(texts)),
			}, func(measureCtx context.Context) error {
				var batchErr error
				phrases, batchErr = phraseBatcher.ExtractImportantPhrasesBatch(measureCtx, texts, phraseLimit, string(lang), req.Model)
				return batchErr
			})
			if err != nil {
				// The batched shape is an optimization, never a new failure
				// mode: fall back to the per-scene call for this language.
				for i, index := range indexes {
					value, singleErr := phraseExtractor.ExtractImportantPhrases(ctx, texts[i], phraseLimit, string(lang), req.Model)
					if singleErr != nil {
						return fmt.Errorf("translated NLP phrases for scene %s/%s failed: %w", result.Scenes[work[index].sceneIndex].ID, lang, singleErr)
					}
					outcomes[index].phrases = value
				}
				continue
			}
			if len(phrases) != len(indexes) {
				return fmt.Errorf("translated NLP batch phrases for %s returned %d results for %d scenes", lang, len(phrases), len(indexes))
			}
			for i, index := range indexes {
				outcomes[index].phrases = phrases[i]
			}
		}
	}

	// ── Phase 3: grounding + annotation projection (pure CPU). ───────────
	for index, item := range work {
		// Treat already-extracted source names as identity hints only. A hint is
		// copied into the localized annotations only when its name can be found
		// in this translated scene, so it cannot invent a mention or a phrase.
		sourceHints := groundLocalizedSourceEntities(item.text, result.Scenes[item.sceneIndex].Annotations)
		outcomes[index].entities = mergeTranslatedNamedEntities(outcomes[index].entities, sourceHints)
		outcomes[index].entities = limitTranslatedVisualEntities(outcomes[index].entities, entityLimit)
		groundedPhrases := groundImportantPhrases(item.text, outcomes[index].entities, outcomes[index].phrases, phraseLimit)
		insights := scriptpkg.SegmentInsights{
			SegmentID:        result.Scenes[item.sceneIndex].ID,
			TextHash:         SceneTextHash(item.text),
			ImportantPhrases: groundedPhrases,
			ImportantWords:   limitTranslatedNLPStrings(outcomes[index].words, wordLimit),
			SpecialNames:     translatedSpecialNames(item.text, outcomes[index].specialNames, outcomes[index].entities, entityLimit),
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

func groundLocalizedSourceEntities(text string, source *scriptpkg.SceneAnnotations) []VisualEntity {
	if source == nil {
		return nil
	}
	all := append(append([]scriptpkg.AnnotatedEntity(nil), source.PrimaryEntities...), source.SecondaryEntities...)
	var out []VisualEntity
	for _, entity := range all {
		kind := localizedSourceEntityType(entity.Type)
		if kind == "" {
			continue
		}
		identity := firstNonEmpty(entity.CanonicalName, entity.Text)
		aliases := []string{identity}
		if kind == scriptpkg.EntityTypePerson {
			identity = normalizeVisualPersonName(identity)
			aliases = []string{identity}
			// Source mention surfaces can include a role or an editorial lead-in
			// ("Trainer Cus D’Amato", "Like Muhammad Ali"). Try complete
			// proper-name suffixes, longest first, against the translated text.
			for _, run := range personNameRuns(identity) {
				for start := 1; start < len(run); start++ {
					aliases = append(aliases, strings.Join(run[start:], " "))
				}
			}
		}
		for _, alias := range aliases {
			span, ok := findEntitySpan(text, alias)
			if !ok || strings.TrimSpace(span.Text) == "" {
				continue
			}
			out = append(out, VisualEntity{Text: span.Text, Type: kind, Score: float32(entity.Confidence)})
			break
		}
	}
	return out
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

func applyTranslatedNLPExtraction(outcome *translatedNLPOutcome, extraction SceneNLPExtraction, includePhrases, includeWords, includeSpecialNames, includeEntities bool) {
	if outcome == nil {
		return
	}
	if includePhrases {
		outcome.phrases = append([]string(nil), extraction.ImportantPhrases...)
	}
	if includeWords {
		outcome.words = append([]string(nil), extraction.ImportantWords...)
	}
	if includeSpecialNames {
		outcome.specialNames = append([]string(nil), extraction.SpecialNames...)
	}
	if includeEntities {
		outcome.entities = mergeTranslatedNamedEntities(outcome.entities, extraction.Entities)
	}
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
