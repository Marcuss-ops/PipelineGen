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

type translatedNLPResult struct {
	sceneIndex  int
	lang        Language
	annotations *scriptpkg.SceneAnnotations
}

// runTranslatedNLP extracts grounded names/entities and important phrases from
// every translated scene text. The source-language Annotations surface remains
// untouched because it is the overlay/media identity surface; translated
// annotations are stored separately and selected by the document language.
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

	workers := DefaultNLPConcurrency
	if limit := r.vidRushPipeline.Backpressure.ExtractionLimit; limit > 0 && limit < workers {
		workers = limit
	}
	outcomes, err := concurrent.Map(ctx, work, workers, func(opCtx context.Context, _ int, item translatedNLPWork) (translatedNLPResult, error) {
		var entities []VisualEntity
		var phrases []string
		err := kernobs.MeasureOperation(opCtx, kernobs.OperationInfo{
			Stage: kernobs.StageName("scene_analysis"), Component: kernobs.ComponentNLP, Operation: kernobs.OperationExtract,
			Provider: string(item.lang), MetadataJSON: fmt.Sprintf("{\"scene_id\":%q,\"language\":%q,\"surface\":\"translation\"}", result.Scenes[item.sceneIndex].ID, item.lang),
		}, func(measureCtx context.Context) error {
			var extractErr error
			if includeEntities {
				entityLimit := extraction.MaxEntitiesPerSegment
				if entityLimit <= 0 {
					entityLimit = 3
				}
				entities, extractErr = r.vidRushPipeline.NERPort.Extract(measureCtx, item.text, entityLimit)
				if extractErr != nil {
					return extractErr
				}
			}
			if includePhrases && r.vidRushPipeline.PhraseExtractor != nil {
				phraseLimit := extraction.MaxImportantPhrasesPerSegment
				if phraseLimit <= 0 {
					phraseLimit = 3
				}
				phrases, extractErr = r.vidRushPipeline.PhraseExtractor.ExtractImportantPhrases(measureCtx, item.text, phraseLimit, string(item.lang), req.Model)
			}
			return extractErr
		})
		if err != nil {
			return translatedNLPResult{}, fmt.Errorf("translated NLP for scene %s/%s failed: %w", result.Scenes[item.sceneIndex].ID, item.lang, err)
		}

		entityLimit := extraction.MaxEntitiesPerSegment
		if entityLimit <= 0 {
			entityLimit = 3
		}
		if len(entities) > entityLimit {
			entities = entities[:entityLimit]
		}
		phraseLimit := extraction.MaxImportantPhrasesPerSegment
		if phraseLimit <= 0 {
			phraseLimit = 3
		}
		groundedPhrases := groundImportantPhrases(item.text, entities, phrases, phraseLimit)
		insights := scriptpkg.SegmentInsights{
			SegmentID:        result.Scenes[item.sceneIndex].ID,
			TextHash:         SceneTextHash(item.text),
			ImportantPhrases: groundedPhrases,
		}
		for _, entity := range entities {
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
		return translatedNLPResult{sceneIndex: item.sceneIndex, lang: item.lang, annotations: annotation}, nil
	})
	if err != nil {
		return err
	}

	for _, outcome := range outcomes {
		if outcome.annotations == nil {
			continue
		}
		if result.Scenes[outcome.sceneIndex].LocalizedAnnotations == nil {
			result.Scenes[outcome.sceneIndex].LocalizedAnnotations = make(map[Language]*scriptpkg.SceneAnnotations)
		}
		outcome.annotations.Language = string(outcome.lang)
		result.Scenes[outcome.sceneIndex].LocalizedAnnotations[outcome.lang] = outcome.annotations
	}
	return nil
}
