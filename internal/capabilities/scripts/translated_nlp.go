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
	entities []VisualEntity
	phrases  []string
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
	if !includeEntities && !includePhrases {
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

	// ── Phase 1: entities (always per scene) + phrases only when the
	// configured extractor cannot batch. ─────────────────────────────────
	var batcher BatchImportantPhraseExtractor
	canBatch := false
	phraseExtractor := r.vidRushPipeline.PhraseExtractor
	if includePhrases && phraseExtractor != nil {
		if candidate, ok := phraseExtractor.(BatchImportantPhraseExtractor); ok {
			batcher, canBatch = candidate, true
		}
	}

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
			if includePhrases && !canBatch && phraseExtractor != nil {
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
	if includePhrases && canBatch && batcher != nil {
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
				phrases, batchErr = batcher.ExtractImportantPhrasesBatch(measureCtx, texts, phraseLimit, string(lang), req.Model)
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
		groundedPhrases := groundImportantPhrases(item.text, outcomes[index].entities, outcomes[index].phrases, phraseLimit)
		insights := scriptpkg.SegmentInsights{
			SegmentID:        result.Scenes[item.sceneIndex].ID,
			TextHash:         SceneTextHash(item.text),
			ImportantPhrases: groundedPhrases,
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
