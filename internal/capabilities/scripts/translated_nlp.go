package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	phrasepkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases"
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
//
// It reads the SOURCE annotations from the durable result. Callers that run it
// concurrently with a TTS writer — which mutates the scenes' voiceover fields —
// must instead pass the annotations they computed, via
// runTranslatedNLPWithSourceAnnotations.
func (r *Runner) runTranslatedNLP(ctx context.Context, req GenerateRequest, result *GenerateResult) error {
	return r.runTranslatedNLPWithSourceAnnotations(ctx, req, result, nil)
}

// runTranslatedNLPWithSourceAnnotations is runTranslatedNLP with the source
// annotations supplied explicitly, keyed by scene index.
//
// Why the seam exists: translated NLP depends on the translations (final after
// the translate phase) and on the SOURCE entity annotations only — never on TTS.
// Passing them in lets the SceneTextReady fan-out run this work on the semantic
// branch, concurrently with the still-running TTS branch, instead of after the
// global join. The annotations are the prepare branch's own output, so the
// function never has to read (or race with) the mutable Scene while TTS writes
// its voiceover fields.
//
// A nil map keeps the historical behaviour: read result.Scenes[i].Annotations.
func (r *Runner) runTranslatedNLPWithSourceAnnotations(ctx context.Context, req GenerateRequest, result *GenerateResult, sourceAnnotations map[int]*scriptpkg.SceneAnnotations) error {
	localized, err := r.computeLocalizedAnnotations(ctx, req, result, sourceAnnotations)
	if err != nil {
		return err
	}
	applyLocalizedAnnotations(result, localized)
	return nil
}

// computeLocalizedAnnotations is the PURE half of the translated-NLP pass: it
// returns sceneIndex → language → localized annotations as VALUES and never
// mutates result.
//
// The split is what makes the concurrent call safe. The TTS writer snapshots a
// scene with `*item.scene` (localizedRenderClipFields copies the whole struct)
// and json-marshals the result for its per-unit checkpoint, so ANY write onto
// result.Scenes while TTS runs is a data race — not only a write to the field
// being written. Computing into a detached value and applying it on the owning
// goroutine keeps the overlap without a shared write.
func (r *Runner) computeLocalizedAnnotations(ctx context.Context, req GenerateRequest, result *GenerateResult, sourceAnnotations map[int]*scriptpkg.SceneAnnotations) (map[int]map[Language]*scriptpkg.SceneAnnotations, error) {
	if r == nil || result == nil || r.vidRushPipeline == nil {
		return nil, nil
	}
	sourceAnnotationsFor := func(sceneIndex int) *scriptpkg.SceneAnnotations {
		if sourceAnnotations == nil {
			return result.Scenes[sceneIndex].Annotations
		}
		return sourceAnnotations[sceneIndex]
	}
	extraction := req.MediaPlan.Extraction
	includeEntities := extraction.Includes(mediadomain.ExtractionIncludeEntities) || extraction.Includes(mediadomain.ExtractionIncludeSpecialNames)
	includePhrases := extraction.Includes(mediadomain.ExtractionIncludeImportantPhrases)
	includeWords := extraction.Includes(mediadomain.ExtractionIncludeImportantWords)
	if !includeEntities && !includePhrases && !includeWords {
		return nil, nil
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
		return nil, nil
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

	// Ground every SOURCE annotation entity in its translation BEFORE the NER
	// fan-out. Matching is pure CPU over already-final text, and its result is
	// both (a) the identity hint the projection consumes below and (b) the
	// input of the redundant-call gate: when the source annotations already
	// ground `entityLimit` entities here, a translated NER call can only
	// produce candidates the limit window discards.
	sourceMatches := make([][]localizedSourceMatch, len(work))
	for index, item := range work {
		sourceMatches[index] = matchLocalizedSourceEntities(item.text, string(item.lang), sourceAnnotationsFor(item.sceneIndex))
	}

	outcomes, err := concurrent.Map(ctx, work, workers, func(opCtx context.Context, idx int, item translatedNLPWork) (translatedNLPOutcome, error) {
		var outcome translatedNLPOutcome
		// The translated NER call is SKIPPED when the source annotations
		// already cover the entity limit for this language. Skipping is
		// output-equivalent, not an approximation: mergeTranslatedNamedEntities
		// drops VisualNER's typed named entities as soon as source matches
		// exist, and limitTranslatedVisualEntities orders PERSON first, so with
		// `entityLimit` grounded PERSON matches every model entity falls outside
		// the window anyway. ``sourceMatchesCoverEntityLimit`` encodes exactly
		// that precondition (and refuses to skip for any other entity kind).
		if includeEntities && r.vidRushPipeline.NERPort != nil && !sourceMatchesCoverEntityLimit(sourceMatches[idx], entityLimit) {
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
		return nil, err
	}

	localized := make(map[int]map[Language]*scriptpkg.SceneAnnotations)
	// Grounding and annotation projection are pure CPU work over each
	// translation's own text. Source entity matches carry stable identity only;
	// phrase surfaces are always selected from the localized narration.
	for index, item := range work {
		// Already-extracted source names act as identity hints only. A hint is
		// copied into the localized annotations only when its name can be found
		// in this translated scene, so it cannot invent a mention or a phrase.
		// The SAME matches also carry the source identity, which the localized
		// annotation inherits below instead of re-minting one from the
		// translated surface.
		outcomes[index].entities = mergeTranslatedNamedEntities(outcomes[index].entities, localizedSourceVisualEntities(sourceMatches[index]))
		outcomes[index].entities = limitTranslatedVisualEntities(outcomes[index].entities, entityLimit)
		phraseCandidates := phrasepkg.ImportantPhrases(item.text, entityRuneSpans(item.text, outcomes[index].entities), phraseLimit, string(item.lang))
		var groundedPhrases []string
		if includePhrases {
			groundedPhrases = groundImportantPhrases(item.text, outcomes[index].entities, phraseCandidates, phraseLimit)
		}
		var importantWords []string
		if includeWords {
			importantWords = phrasepkg.ImportantWords(phraseCandidates, wordLimit, string(item.lang))
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
		stampLocalizedSourceIdentity(annotation, sourceMatches[index])
		annotation.Language = string(item.lang)
		if localized[item.sceneIndex] == nil {
			localized[item.sceneIndex] = make(map[Language]*scriptpkg.SceneAnnotations)
		}
		localized[item.sceneIndex][item.lang] = annotation
	}
	return localized, nil
}

// applyLocalizedAnnotations projects the computed per-(scene, language)
// annotations onto the durable result. It is the ONLY writer of
// Scene.LocalizedAnnotations, so it always runs on the goroutine that owns the
// result (the phase goroutine, after the fan-out join).
func applyLocalizedAnnotations(result *GenerateResult, localized map[int]map[Language]*scriptpkg.SceneAnnotations) {
	if result == nil || len(localized) == 0 {
		return
	}
	for sceneIndex, byLanguage := range localized {
		if sceneIndex < 0 || sceneIndex >= len(result.Scenes) {
			continue
		}
		if result.Scenes[sceneIndex].LocalizedAnnotations == nil {
			result.Scenes[sceneIndex].LocalizedAnnotations = make(map[Language]*scriptpkg.SceneAnnotations, len(byLanguage))
		}
		for language, annotation := range byLanguage {
			result.Scenes[sceneIndex].LocalizedAnnotations[language] = annotation
		}
	}
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
