package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/semantics"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

type translatedNLPWork struct {
	sceneIndex int
	lang       contract.Language
	text       string
}

// translatedNLPOutcome contains named-entity evidence returned by VisualNER.
// Editorial phrases and words are selected later by deterministic local rules
// over the exact translated text.
type translatedNLPOutcome struct {
	entities []VisualEntity
	cached   *scriptpkg.SceneAnnotations
}

// localizedNLPSnapshot freezes only the scene identity, text, and source
// annotations consumed by computeLocalizedAnnotations. It is created before
// the parallel TTS branch begins writing voiceover/timing fields.
func localizedNLPSnapshot(result *contract.GenerateResult) *contract.GenerateResult {
	if result == nil {
		return nil
	}
	snapshot := &contract.GenerateResult{Scenes: make([]contract.Scene, len(result.Scenes))}
	for index := range result.Scenes {
		scene := result.Scenes[index]
		snapshot.Scenes[index] = contract.Scene{
			ID: scene.ID, Index: scene.Index, Text: make(map[contract.Language]string, len(scene.Text)),
			Annotations: scene.Annotations, Entities: scene.Entities,
			LocalizedAnnotations: scene.LocalizedAnnotations,
		}
		for language, text := range scene.Text {
			snapshot.Scenes[index].Text[language] = text
		}
	}
	return snapshot
}

// ── Coordinator-side translated-NLP dispatch ──────────────────────────
// Split out of scene_ready_coordinator.go to keep that file under the
// godlike/08 max_lines_per_file_strict cap (600). Same package, same behaviour:
// these are the sceneReadyCoordinator methods that schedule one translated-NLP
// computation per (scene, language) the moment that language's translation is
// final, so the source-NER wait overlaps the remaining TTS instead of forming a
// post-join barrier (see localizedNLPForScene).

func (c *sceneReadyCoordinator) translatedNLPRequested() bool {
	if c.runner == nil || c.runner.vidRushPipeline == nil {
		return false
	}
	extraction := c.req.MediaPlan.Extraction
	return extraction.EntityExtractionRequested() ||
		extraction.Includes(mediadomain.ExtractionIncludeImportantPhrases) ||
		extraction.Includes(mediadomain.ExtractionIncludeImportantWords)
}

// localizedNLPForScene waits only for this scene's source enrichment and then
// computes one translated annotation value. It deliberately runs outside the
// translation/TTS worker: the scene's voiceover can continue while source NER
// finishes, and the final pass reuses this value instead of calling NER again.
func (c *sceneReadyCoordinator) localizedNLPForScene(scene contract.Scene, lang contract.Language, text string) (*scriptpkg.SceneAnnotations, error) {
	if !c.translatedNLPRequested() || sceneExtractionToggle(c.req, scene) == scriptpkg.ToggleDisabled || lang == c.req.SourceLanguage || strings.TrimSpace(text) == "" {
		return nil, nil
	}
	if err := c.nlpSlots.AcquireCtx(c.ctx); err != nil {
		return nil, err
	}
	defer c.nlpSlots.Release()

	source := scene.Annotations
	if source == nil {
		segment, supported, err := c.runner.waitForVidRushScene(c.ctx, c.runID, scene.Index)
		if err != nil {
			return nil, err
		}
		if !supported {
			// A test or legacy seam may expose only the document barrier. The
			// final fan-out still computes the complete localized surface.
			return nil, nil
		}
		snapshot := []sceneTextSnapshot{{
			ID: scene.ID, Index: 0, Text: scene.Text[c.req.SourceLanguage], Annotations: scene.Annotations,
		}}
		phraseLimit := c.req.MediaPlan.Extraction.MaxImportantPhrasesPerSegment
		includePhrases := c.req.MediaPlan.Extraction.Includes(mediadomain.ExtractionIncludeImportantPhrases)
		var selector PhraseSelector
		if c.runner.vidRushPipeline != nil {
			selector = c.runner.vidRushPipeline.PhraseSelector
		}
		computed, computeErr := computeSegmentEntityAnnotations(c.ctx, selector, snapshot, c.req.SourceLanguage, []scriptpkg.VidRushSegmentResult{segment}, phraseLimit, includePhrases, c.req.MediaPlan.Extraction.ImportantPhrases)
		if computeErr != nil {
			return nil, computeErr
		}
		source = computed[0]
	}

	mini := &contract.GenerateResult{Scenes: []contract.Scene{{
		ID: scene.ID, Index: 0,
		Text:        map[contract.Language]string{lang: text},
		Annotations: source,
	}}}
	localized, err := c.runner.computeLocalizedAnnotations(c.ctx, c.req, mini, map[int]*scriptpkg.SceneAnnotations{0: source})
	if err != nil {
		return nil, err
	}
	if byLanguage := localized[0]; byLanguage != nil {
		return byLanguage[lang], nil
	}
	return nil, nil
}

func (c *sceneReadyCoordinator) launchLocalizedNLP(scene contract.Scene, lang contract.Language, text string) {
	if !c.translatedNLPRequested() || sceneExtractionToggle(c.req, scene) == scriptpkg.ToggleDisabled || lang == c.req.SourceLanguage || strings.TrimSpace(text) == "" {
		return
	}
	c.nlpWg.Add(1)
	go func() {
		defer c.nlpWg.Done()
		annotation, err := c.localizedNLPForScene(scene, lang, text)
		c.mu.Lock()
		defer c.mu.Unlock()
		if err != nil {
			c.nlpErrors = append(c.nlpErrors, fmt.Errorf("translated NLP ready scene %s/%s: %w", scene.ID, lang, err))
			return
		}
		if annotation != nil {
			if c.localized[scene.Index] == nil {
				c.localized[scene.Index] = make(map[contract.Language]*scriptpkg.SceneAnnotations)
			}
			c.localized[scene.Index][lang] = annotation
		}
	}()
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
func (r *Runner) runTranslatedNLP(ctx context.Context, req contract.GenerateRequest, result *contract.GenerateResult) error {
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
func (r *Runner) runTranslatedNLPWithSourceAnnotations(ctx context.Context, req contract.GenerateRequest, result *contract.GenerateResult, sourceAnnotations map[int]*scriptpkg.SceneAnnotations) error {
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
func (r *Runner) computeLocalizedAnnotations(ctx context.Context, req contract.GenerateRequest, result *contract.GenerateResult, sourceAnnotations map[int]*scriptpkg.SceneAnnotations) (map[int]map[contract.Language]*scriptpkg.SceneAnnotations, error) {
	batch, ok, err := r.prepareLocalizedNLPBatch(req, result, sourceAnnotations)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	sourceMatches, outcomes, err := r.resolveTranslatedNLPOutcomes(ctx, result, batch.work, batch.settings, batch.sourceAnnotationsFor)
	if err != nil {
		return nil, err
	}
	return r.projectTranslatedNLPOutcomes(ctx, result, batch.work, outcomes, sourceMatches, batch.settings, batch.sourceAnnotationsFor, batch.documentCorpus)
}

func (r *Runner) projectTranslatedNLPOutcomes(ctx context.Context, result *contract.GenerateResult, work []translatedNLPWork, outcomes []translatedNLPOutcome, sourceMatches [][]localizedSourceMatch, settings translatedNLPSettings, sourceAnnotationsFor func(int) *scriptpkg.SceneAnnotations, documentCorpus map[contract.Language][]string) (map[int]map[contract.Language]*scriptpkg.SceneAnnotations, error) {
	localized := make(map[int]map[contract.Language]*scriptpkg.SceneAnnotations)
	for index, item := range work {
		if outcomes[index].cached != nil {
			setLocalizedAnnotation(localized, item.sceneIndex, item.lang, outcomes[index].cached)
			continue
		}
		entities, completed, err := r.translatedEntitiesForProjection(ctx, result, item, outcomes[index].entities, sourceMatches[index], sourceAnnotationsFor(item.sceneIndex), settings)
		if err != nil {
			return nil, err
		}
		sourceMatches[index] = completed
		insights, err := buildTranslatedInsights(ctx, result.Scenes[item.sceneIndex].ID, item, entities, settings, documentCorpus[item.lang])
		if err != nil {
			return nil, fmt.Errorf("translated phrase selection for scene %s/%s failed: %w", result.Scenes[item.sceneIndex].ID, item.lang, err)
		}
		annotation := projectEntityAnnotations(item.text, string(item.lang), scriptpkg.VidRushSegmentResult{
			SegmentID: result.Scenes[item.sceneIndex].ID, SceneID: result.Scenes[item.sceneIndex].ID,
			Position: result.Scenes[item.sceneIndex].Index, Text: item.text,
			TextHash: SceneTextHash(item.text), Insights: insights,
		})
		if annotation == nil {
			continue
		}
		if err := stampLocalizedSourceIdentity(annotation, completed); err != nil {
			return nil, fmt.Errorf("stamp localized identity for scene %s/%s: %w", result.Scenes[item.sceneIndex].ID, item.lang, err)
		}
		annotation.Language = string(item.lang)
		setLocalizedAnnotation(localized, item.sceneIndex, item.lang, annotation)
	}
	return localized, nil
}

func setLocalizedAnnotation(localized map[int]map[contract.Language]*scriptpkg.SceneAnnotations, sceneIndex int, language contract.Language, annotation *scriptpkg.SceneAnnotations) {
	if localized[sceneIndex] == nil {
		localized[sceneIndex] = make(map[contract.Language]*scriptpkg.SceneAnnotations)
	}
	localized[sceneIndex][language] = annotation
}

func (r *Runner) translatedEntitiesForProjection(ctx context.Context, result *contract.GenerateResult, item translatedNLPWork, entities []VisualEntity, sourceMatches []localizedSourceMatch, sourceAnnotations *scriptpkg.SceneAnnotations, settings translatedNLPSettings) ([]VisualEntity, []localizedSourceMatch, error) {
	completed, err := r.completeLocalizedPersonMatches(ctx, item.text, string(item.lang), sourceAnnotations, entities, sourceMatches)
	if err != nil {
		return nil, nil, fmt.Errorf("localized identity completion for scene %s/%s failed: %w", result.Scenes[item.sceneIndex].ID, item.lang, err)
	}
	entities = mergeTranslatedNamedEntities(entities, localizedSourceVisualEntities(completed))
	if settings.categoryOnly {
		filtered := make([]VisualEntity, 0, len(entities))
		for _, entity := range entities {
			if settings.extraction.IncludesEntityType(string(entity.Type)) {
				filtered = append(filtered, entity)
			}
		}
		entities = limitTranslatedVisualEntities(filtered, settings.entityLimit)
	} else if settings.includeEntities && settings.extraction.Includes(mediadomain.ExtractionIncludeEntities) && hasAdditionalEntityCategory(settings.extraction) {
		entities = limitVisualIdentityEntities(entities, settings.entityLimit)
	} else if settings.includeEntities {
		entities = limitTranslatedVisualEntities(entities, settings.entityLimit)
	} else {
		entities = nil
	}
	return entities, completed, nil
}

func buildTranslatedInsights(ctx context.Context, sceneID string, item translatedNLPWork, entities []VisualEntity, settings translatedNLPSettings, corpus []string) (scriptpkg.SegmentInsights, error) {
	var phraseCandidates []string
	if settings.includePhrases || settings.includeWords {
		selected, err := settings.phraseSelector.SelectPhrases(ctx, PhraseSelectionRequest{
			Text: item.text, BlockedRuneSpans: entityRuneSpans(item.text, entities),
			Limit: settings.phraseLimit, Corpus: corpus,
			Profile: phraseSelectionProfileForLanguage(string(item.lang)),
		})
		if err != nil {
			return scriptpkg.SegmentInsights{}, err
		}
		phraseCandidates = make([]string, 0, len(selected))
		for _, phrase := range selected {
			phraseCandidates = append(phraseCandidates, phrase.Text)
		}
	}
	var importantPhrases []string
	if settings.includePhrases {
		importantPhrases = groundImportantPhrases(item.text, entities, phraseCandidates, settings.phraseLimit)
	}
	var importantWords []string
	if settings.includeWords {
		importantWords = importantWordsFromPhrases(phraseCandidates, settings.wordLimit, phraseSelectionProfileForLanguage(string(item.lang)))
	}
	var specialNames []string
	if settings.includeEntities && (settings.extraction.Includes(mediadomain.ExtractionIncludeEntities) && !hasAdditionalEntityCategory(settings.extraction) || settings.extraction.Includes(mediadomain.ExtractionIncludeSpecialNames)) {
		specialNames = translatedSpecialNames(item.text, nil, entities, settings.entityLimit)
	}
	insights := scriptpkg.SegmentInsights{
		SegmentID: sceneID, TextHash: SceneTextHash(item.text),
		ImportantPhrases: importantPhrases, ImportantWords: importantWords, SpecialNames: specialNames,
	}
	for _, entity := range entities {
		entityType := entity.Type
		if strings.TrimSpace(string(entityType)) == "" {
			entityType = scriptpkg.EntityTypeVisualConcept
		}
		insights.Entities = append(insights.Entities, scriptpkg.ExtractedEntity{
			Value: entity.Text, Type: string(entityType), Confidence: float64(entity.Score), Role: entity.Role,
			EditorialDecision: entity.EditorialDecision, EditorialSurface: entity.EditorialSurface,
			EditorialType: entity.EditorialType, PromptEcho: entity.PromptEcho, VisualAdmission: entity.VisualAdmission,
		})
	}
	return insights, nil
}

type translatedNLPSettings struct {
	extraction      mediadomain.MediaExtractionPolicy
	includeEntities bool
	categoryOnly    bool
	includePhrases  bool
	includeWords    bool
	phraseSelector  PhraseSelector
	entityLimit     int
	nerLimit        int
	phraseLimit     int
	wordLimit       int
	workers         int
}

type translatedNLPBatch struct {
	settings             translatedNLPSettings
	work                 []translatedNLPWork
	sourceAnnotationsFor func(int) *scriptpkg.SceneAnnotations
	documentCorpus       map[contract.Language][]string
}

func (r *Runner) prepareLocalizedNLPBatch(req contract.GenerateRequest, result *contract.GenerateResult, sourceAnnotations map[int]*scriptpkg.SceneAnnotations) (translatedNLPBatch, bool, error) {
	if r == nil || result == nil || r.vidRushPipeline == nil {
		return translatedNLPBatch{}, false, nil
	}
	policy := req.MediaPlan.Extraction
	settings, active, err := r.translatedNLPSettings(policy)
	if err != nil || !active {
		return translatedNLPBatch{}, false, err
	}
	work, corpus := translatedNLPWorkForRequest(req, result)
	if len(work) == 0 {
		return translatedNLPBatch{}, false, nil
	}
	// A detached SceneTextReady snapshot carries the source annotations needed
	// here; nil retains the historical lookup from the result itself.
	sourceAnnotationsFor := func(sceneIndex int) *scriptpkg.SceneAnnotations {
		if sourceAnnotations == nil {
			return result.Scenes[sceneIndex].Annotations
		}
		return sourceAnnotations[sceneIndex]
	}
	return translatedNLPBatch{settings: settings, work: work, sourceAnnotationsFor: sourceAnnotationsFor, documentCorpus: corpus}, true, nil
}

func (r *Runner) translatedNLPSettings(policy mediadomain.MediaExtractionPolicy) (translatedNLPSettings, bool, error) {
	settings := translatedNLPSettings{
		extraction:      policy,
		includeEntities: policy.EntityExtractionRequested(),
		includePhrases:  policy.Includes(mediadomain.ExtractionIncludeImportantPhrases),
		includeWords:    policy.Includes(mediadomain.ExtractionIncludeImportantWords),
	}
	if !settings.includeEntities && !settings.includePhrases && !settings.includeWords {
		return settings, false, nil
	}
	if settings.includePhrases || settings.includeWords {
		if r.vidRushPipeline == nil || r.vidRushPipeline.PhraseSelector == nil {
			return translatedNLPSettings{}, false, fmt.Errorf("translated NLP: phrase selector is required when important phrases or words are requested")
		}
		settings.phraseSelector = r.vidRushPipeline.PhraseSelector
	}
	settings.entityLimit = policy.MaxEntitiesPerSegment
	if settings.entityLimit <= 0 {
		settings.entityLimit = 3
	}
	settings.categoryOnly = policy.HasCategoryOnlyIncludes()
	settings.nerLimit = settings.entityLimit
	if settings.categoryOnly || hasAdditionalEntityCategory(policy) {
		settings.nerLimit = min(max(settings.entityLimit*8, 12), 100)
	}
	settings.phraseLimit = policy.MaxImportantPhrasesPerSegment
	if settings.phraseLimit <= 0 {
		settings.phraseLimit = 3
	}
	settings.wordLimit = policy.MaxImportantWordsPerSegment
	if settings.wordLimit <= 0 {
		settings.wordLimit = 3
	}
	settings.workers = poolSize(r.nlpConcurrency, DefaultNLPConcurrency)
	if limit := r.vidRushPipeline.Backpressure.ExtractionLimit; limit > 0 && limit < settings.workers {
		settings.workers = limit
	}
	return settings, true, nil
}

func translatedNLPWorkForRequest(req contract.GenerateRequest, result *contract.GenerateResult) ([]translatedNLPWork, map[contract.Language][]string) {
	langs := make([]contract.Language, 0, len(req.Languages)+1)
	seen := make(map[contract.Language]struct{}, len(req.Languages)+1)
	for _, lang := range append([]contract.Language{req.SourceLanguage}, req.Languages...) {
		if lang != "" {
			if _, exists := seen[lang]; !exists {
				seen[lang] = struct{}{}
				langs = append(langs, lang)
			}
		}
	}
	work := make([]translatedNLPWork, 0)
	for sceneIndex := range result.Scenes {
		scene := result.Scenes[sceneIndex]
		if sceneExtractionToggle(req, scene) == scriptpkg.ToggleDisabled {
			continue
		}
		for _, lang := range langs {
			if lang == req.SourceLanguage {
				continue
			}
			text := strings.TrimSpace(scene.Text[lang])
			if text != "" {
				work = append(work, translatedNLPWork{sceneIndex: sceneIndex, lang: lang, text: text})
			}
		}
	}
	corpus := make(map[contract.Language][]string, len(langs))
	for _, item := range work {
		corpus[item.lang] = append(corpus[item.lang], item.text)
	}
	return work, corpus
}

func (r *Runner) resolveTranslatedNLPOutcomes(ctx context.Context, result *contract.GenerateResult, work []translatedNLPWork, settings translatedNLPSettings, sourceAnnotationsFor func(int) *scriptpkg.SceneAnnotations) ([][]localizedSourceMatch, []translatedNLPOutcome, error) {
	sourceMatches := make([][]localizedSourceMatch, len(work))
	for index, item := range work {
		matches, err := r.matchLocalizedSources(ctx, item.text, string(item.lang), sourceAnnotationsFor(item.sceneIndex))
		if err != nil {
			return nil, nil, fmt.Errorf("localized identity matching for scene %s/%s failed: %w", result.Scenes[item.sceneIndex].ID, item.lang, err)
		}
		sourceMatches[index] = matches
	}
	outcomes, err := concurrent.Map(ctx, work, settings.workers, func(opCtx context.Context, idx int, item translatedNLPWork) (translatedNLPOutcome, error) {
		if cached := result.Scenes[item.sceneIndex].LocalizedAnnotations[item.lang]; cached != nil {
			return translatedNLPOutcome{cached: cached}, nil
		}
		// Skip translated NER only when grounded source identities already fill
		// the output limit; this matches the later merge and top-N behavior.
		shouldExtract := settings.includeEntities && r.vidRushPipeline != nil && r.vidRushPipeline.NERPort != nil &&
			(settings.categoryOnly || hasAdditionalEntityCategory(settings.extraction) || !sourceMatchesCoverEntityLimit(sourceMatches[idx], settings.entityLimit))
		if !shouldExtract {
			return translatedNLPOutcome{}, nil
		}
		var outcome translatedNLPOutcome
		err := kernobs.MeasureOperation(opCtx, kernobs.OperationInfo{
			Stage: kernobs.StageSceneAnalysis, Component: kernobs.ComponentNLP, Operation: kernobs.OperationExtract,
			Provider: string(item.lang), MetadataJSON: fmt.Sprintf("{\"scene_id\":%q,\"language\":%q,\"surface\":\"translation\"}", result.Scenes[item.sceneIndex].ID, item.lang),
		}, func(measureCtx context.Context) error {
			var extractErr error
			outcome.entities, extractErr = r.vidRushPipeline.NERPort.Extract(measureCtx, string(item.lang), item.text, settings.nerLimit)
			return extractErr
		})
		if err != nil {
			return translatedNLPOutcome{}, fmt.Errorf("translated NER for scene %s/%s failed: %w", result.Scenes[item.sceneIndex].ID, item.lang, err)
		}
		return outcome, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return sourceMatches, outcomes, nil
}

// applyLocalizedAnnotations projects the computed per-(scene, language)
// annotations onto the durable result. It is the ONLY writer of
// Scene.LocalizedAnnotations, so it always runs on the goroutine that owns the
// result (the phase goroutine, after the fan-out join).
func applyLocalizedAnnotations(result *contract.GenerateResult, localized map[int]map[contract.Language]*scriptpkg.SceneAnnotations) {
	if result == nil || len(localized) == 0 {
		return
	}
	for sceneIndex, byLanguage := range localized {
		if sceneIndex < 0 || sceneIndex >= len(result.Scenes) {
			continue
		}
		if result.Scenes[sceneIndex].LocalizedAnnotations == nil {
			result.Scenes[sceneIndex].LocalizedAnnotations = make(map[contract.Language]*scriptpkg.SceneAnnotations, len(byLanguage))
		}
		for language, annotation := range byLanguage {
			result.Scenes[sceneIndex].LocalizedAnnotations[language] = annotation
		}
	}
}

func mergeTranslatedNamedEntities(visual, named []VisualEntity) []VisualEntity {
	if len(named) == 0 {
		return visual
	}
	hasTypedNamed := false
	for _, entity := range visual {
		switch scriptpkg.NormalizeAnnotationType(string(entity.Type)) {
		case "PERSON", "ORG", "EVENT", "WORK_OF_ART", "PRODUCT", "LOGO":
			hasTypedNamed = true
		}
	}
	if !hasTypedNamed {
		return append(append([]VisualEntity(nil), visual...), named...)
	}
	out := make([]VisualEntity, 0, len(visual)+len(named))
	for _, entity := range visual {
		// Preserve brand candidates in the text-fallback lane even when a
		// localized source annotation was matched for another identity.
		if strings.EqualFold(strings.TrimSpace(string(entity.Type)), "BRAND") {
			out = append(out, entity)
			continue
		}
		switch scriptpkg.NormalizeAnnotationType(string(entity.Type)) {
		case "PERSON", "ORG", "EVENT", "WORK_OF_ART", "PRODUCT", "LOGO":
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
	return semantics.LimitStrings(values, limit)
}
