// Package vidrush — semantic_chain.go owns the Fase 1-5
// semantic cutover: the new implementations of SegmentEnricher and
// SegmentProviderResolver that replace the legacy extractor/chooser with the
// SceneIR → VisualNER → MediaSampler → Local Stock → MediaCert chain.
//
// The implementations are a big-bang replacement of the legacy entity
// extraction/selection chain: SceneIRSegmentEnricher (built from NERPort)
// replaces the legacy Enricher, and SemanticProviderResolver replaces the
// legacy provider chooser — composed with the still-wired provider fan-out by
// SemanticAndFanoutResolver. The coordinator's barrier wraps in MediaCertBarrier
// so a SUCCEEDED run with CERTIFIED=false fails the job.
//
// The port/value contracts live in semantic_ports.go, which is also
// where the pure grounding / entity fan-out helpers were moved (split
// 2026-09-16), alongside the barrier in mediacert_barrier.go (split
// 2026-09-12). Both splits exist to keep every file under
// max_lines_per_file_strict=600 (godlike/08).
package vidrush

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/phrases"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/semantics"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/sceneir"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SceneIRSegmentEnricher implements SegmentEnricher using the new chain:
// it compiles a SceneIR from the committed scene (Fase 1, immutable source
// identity), then extracts narration-grounded entities via VisualNER (Fase 3).
// The returned VidRushSegmentResult carries the SceneIR's immutable identity
// + the VisualNER entities, so downstream provider search consumes
// SourceText + Profile (never NarrationText).
//
// Extraction surface (September 2026): the extractor mines the committed
// NARRATION, not the per-segment SourceText. For text/clips payloads
// script_params.segments[].source_text is an editorial BRIEF handed to the
// model ("Describe Musk's move…"), so mining it returned prompt fragments
// ("Describe Musk", "Explain Tesla") and mis-typed toponyms as extractable
// entities. The brief stays the immutable SceneIR identity; only the
// extractor input moved to the text the video actually speaks, which is
// also the only text the entity timeline can anchor overlay cards to.
type SceneIRSegmentEnricher struct {
	nerPort        VisualNERPort
	phraseSelector phrases.PhraseSelector
}

// NewSceneIRSegmentEnricher wires the new enricher. nerPort must be non-nil.
// The optional selector is required whenever the plan requests important
// phrases or words; selection then stays in the Rust worker.
func NewSceneIRSegmentEnricher(nerPort VisualNERPort, selectors ...phrases.PhraseSelector) (*SceneIRSegmentEnricher, error) {
	if nerPort == nil {
		return nil, fmt.Errorf("scriptgeneration: VisualNERPort is required for SceneIRSegmentEnricher")
	}
	enricher := &SceneIRSegmentEnricher{nerPort: nerPort}
	if len(selectors) > 0 {
		enricher.phraseSelector = selectors[0]
	}
	return enricher, nil
}

// Enrich compiles a SceneIR from the committed scene and extracts entities.
// It is the Fase 1 + Fase 3 replacement for the legacy entity extractor.
func (e *SceneIRSegmentEnricher) Enrich(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
	segment, narrationText, err := canonicalEnrichmentSegment(plan, scene)
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	// Extraction surface: the committed narration (never the per-segment
	// SourceText brief — see the type comment). narrationText already fell
	// back to the canonical source above, so an evidence-only segment with
	// no narration keeps the historical source-grounded extraction.
	sourceForExtraction := narrationText

	selection, err := e.selectSemanticEntities(ctx, plan, sourceForExtraction)
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	extraction := selection.policy
	includeEntities := selection.includeEntities
	includeImportantPhrases := selection.includeImportantPhrases
	includeImportantWords := selection.includeImportantWords
	includeSpecialNames := selection.includeSpecialNames
	entities := selection.entities

	projection := projectSemanticEntities(entities, extraction)
	extractedEntities, imageQueries := projection.extracted, projection.imageQueries
	editorial, err := e.selectEditorialSurfaces(ctx, plan, sourceForExtraction, entities, selection)
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, err
	}
	importantPhrases, importantWords, specialNames := editorial.importantPhrases, editorial.importantWords, editorial.specialNames

	// The single canonical compile: the VisualNER and deterministic editorial
	// surfaces are folded in and the profile is built exactly once. This keeps
	// the canonical profile as the only semantic owner.
	entityResult := scriptpkg.EntityResult{
		NounChunks:       entitiesToStrings(entities),
		Concepts:         extractedToConcepts(extractedEntities),
		ImportantPhrases: importantPhrases,
		ImportantWords:   importantWords,
		SpecialNames:     specialNames,
	}
	ir, err := sceneir.Compile(sceneir.CompileInput{Segment: segment, NarrationOverride: narrationText, EntityResult: &entityResult})
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("sceneir enrich profile: %w", err)
	}

	visual := scriptpkg.BuildSegmentVisualProfile(ir.Profile)
	visualProfile := &visual
	// Entity extraction is the source for identity-image queries. PERSON
	// identities are queried independently; the broader visual-profile query
	// builder is not used because it defeats entity-level caching.
	if !includeEntities || (!categoryIncludesImageableIdentity(extraction) &&
		(extraction.HasCategoryOnlyIncludes() || semantics.HasAdditionalEntityCategory(extraction))) ||
		(extraction.HasCategoryOnlyIncludes() && !extraction.Includes(mediadomain.ExtractionIncludeBrands) && !extraction.Includes(mediadomain.ExtractionIncludePersons)) {
		imageQueries = nil
	}
	result := scriptpkg.VidRushSegmentResult{
		SegmentID:       ir.SegmentID,
		SceneID:         scene.ID,
		Position:        ir.Position,
		Text:            narrationText,
		TextHash:        ir.Profile.TextHash,
		SourceText:      ir.SourceText,
		SourceTextHash:  ir.SourceTextHash,
		ExecutionMode:   scene.ExecutionMode,
		SemanticProfile: &ir.Profile,
		Insights: scriptpkg.SegmentInsights{
			SegmentID:     ir.SegmentID,
			TextHash:      ir.Profile.TextHash,
			EntityIndex:   entityIndexVersion(extractedEntities),
			VisualProfile: visualProfile,
			Entities:      extractedEntities,
			ImportantPhrases: func() []string {
				if !includeImportantPhrases {
					return nil
				}
				return append([]string(nil), ir.Profile.ImportantPhrases...)
			}(),
			ImportantWords: func() []string {
				if !includeImportantWords {
					return nil
				}
				return append([]string(nil), importantWords...)
			}(),
			SpecialNames: func() []string {
				if !includeSpecialNames {
					return nil
				}
				return append([]string(nil), specialNames...)
			}(),
			ImageQueries: imageQueries,
		},
	}
	return result, nil
}

func canonicalEnrichmentSegment(plan *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.CanonicalSegment, string, error) {
	segmentID := strings.TrimSpace(scene.SegmentID)
	if segmentID == "" {
		segmentID = strings.TrimSpace(scene.ID)
	}
	sourceText := canonicalSourceText(plan, scene, segmentID)
	narrationText := strings.TrimSpace(scene.Text)
	if narrationText == "" {
		narrationText = sourceText
	}
	segment := scriptpkg.CanonicalSegment{
		ID: segmentID, Position: scene.Index, Text: narrationText, SourceText: sourceText,
	}
	if scene.ExecutionMode != "" {
		segment.ExecutionMode = scene.ExecutionMode
	}
	// Normalize and validate the source identity before expensive extraction;
	// SceneIR compilation later builds the semantic profile exactly once.
	normalized := scriptpkg.NormalizeCanonicalSegment(segment)
	if err := normalized.Validate(); err != nil {
		return scriptpkg.CanonicalSegment{}, "", fmt.Errorf("sceneir enrich: %w: %s", sceneir.ErrCompileInputInvalid, err.Error())
	}
	return segment, narrationText, nil
}

type semanticEntityProjection struct {
	extracted    []scriptpkg.ExtractedEntity
	imageQueries []string
}

func projectSemanticEntities(entities []contract.VisualEntity, policy mediadomain.MediaExtractionPolicy) semanticEntityProjection {
	projection := semanticEntityProjection{extracted: make([]scriptpkg.ExtractedEntity, 0, len(entities))}
	for i := range entities {
		entity := &entities[i]
		if entity.Type == scriptpkg.EntityTypePerson {
			entity.Text = semantics.NormalizeVisualPersonName(entity.Text)
		}
		intents := make([]string, 0, len(entity.SearchIntents))
		for _, intent := range entity.SearchIntents {
			if imageQueryAnchoredToEntity(intent, entity.Text) {
				intents = append(intents, intent)
			}
		}
		entity.SearchIntents = intents
		entityType := entity.Type
		if strings.TrimSpace(string(entityType)) == "" {
			entityType = scriptpkg.EntityTypeVisualConcept
		}
		projection.extracted = append(projection.extracted, scriptpkg.ExtractedEntity{
			Value: entity.Text, Type: string(entityType), Confidence: float64(entity.Score),
			IdentityID: entity.IdentityID, CanonicalID: entity.CanonicalID, Role: entity.Role,
			SearchIntents: append([]string(nil), entity.SearchIntents...), EntityIndex: entity.EntityIndex,
			EditorialDecision: entity.EditorialDecision, EditorialSurface: entity.EditorialSurface,
			EditorialType: entity.EditorialType, PromptEcho: entity.PromptEcho, VisualAdmission: entity.VisualAdmission,
		})
	}
	imageEntities := imageSearchEntities(entities, policy.HasCategoryOnlyIncludes() || semantics.HasAdditionalEntityCategory(policy))
	projection.imageQueries = make([]string, 0, len(imageEntities))
	for _, entity := range imageEntities {
		projection.imageQueries = append(projection.imageQueries, entity.SearchIntents...)
	}
	if policy.HasCategoryOnlyIncludes() && !categoryIncludesImageableIdentity(policy) {
		projection.imageQueries = nil
	}
	return projection
}

type semanticEditorialSurfaces struct {
	importantPhrases []string
	importantWords   []string
	specialNames     []string
}

func (e *SceneIRSegmentEnricher) selectEditorialSurfaces(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, sourceText string, entities []contract.VisualEntity, selection semanticEntitySelection) (semanticEditorialSurfaces, error) {
	var phraseCandidates []string
	if selection.includeImportantPhrases || selection.includeImportantWords {
		if e.phraseSelector == nil {
			return semanticEditorialSurfaces{}, fmt.Errorf("scriptgeneration: phrase selector is required when important phrases or words are requested")
		}
		selected, err := e.phraseSelector.SelectPhrases(ctx, phrases.PhraseSelectionRequest{
			Text: sourceText, BlockedRuneSpans: semantics.EntityRuneSpans(sourceText, entities),
			Limit: selection.phraseLimit, Profile: phrases.ProfileForLanguage(generationPlanLanguage(plan)),
		})
		if err != nil {
			return semanticEditorialSurfaces{}, fmt.Errorf("select important phrases: %w", err)
		}
		phraseCandidates = make([]string, 0, len(selected))
		for _, phrase := range selected {
			phraseCandidates = append(phraseCandidates, phrase.Text)
		}
	}
	var surfaces semanticEditorialSurfaces
	if selection.includeImportantPhrases {
		// Operator hints are external input, so guard proper-name pairs at ingress.
		candidates := make([]string, 0, len(selection.policy.ImportantPhrases)+len(phraseCandidates))
		for _, hint := range selection.policy.ImportantPhrases {
			if !phrases.ContainsProperNamePair(hint) {
				candidates = append(candidates, hint)
			}
		}
		candidates = append(candidates, phraseCandidates...)
		surfaces.importantPhrases = semantics.GroundImportantPhrases(sourceText, entities, candidates, selection.phraseLimit)
	}
	if selection.includeImportantWords {
		surfaces.importantWords = phrases.ImportantWordsFromPhrases(phraseCandidates, selection.wordLimit, phrases.ProfileForLanguage(generationPlanLanguage(plan)))
	}
	if selection.includeSpecialNames {
		surfaces.specialNames = semantics.TranslatedSpecialNames(sourceText, nil, entities, selection.entityLimit)
	}
	return surfaces, nil
}

type semanticEntitySelection struct {
	policy                  mediadomain.MediaExtractionPolicy
	includeEntities         bool
	includeImportantPhrases bool
	includeImportantWords   bool
	includeSpecialNames     bool
	entityLimit             int
	phraseLimit             int
	wordLimit               int
	entities                []contract.VisualEntity
}

func (e *SceneIRSegmentEnricher) selectSemanticEntities(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, sourceText string) (semanticEntitySelection, error) {
	selection := semanticEntitySelection{}
	if plan != nil {
		selection.policy = plan.MediaPlan.Extraction
	}
	policy := selection.policy
	selection.includeEntities = policy.EntityExtractionRequested()
	selection.includeImportantPhrases = policy.Includes(mediadomain.ExtractionIncludeImportantPhrases)
	selection.includeImportantWords = policy.Includes(mediadomain.ExtractionIncludeImportantWords)
	selection.includeSpecialNames = policy.Includes(mediadomain.ExtractionIncludeSpecialNames) ||
		(policy.Includes(mediadomain.ExtractionIncludeEntities) && !semantics.HasAdditionalEntityCategory(policy))
	selection.entityLimit = policy.MaxEntitiesPerSegment
	if selection.entityLimit <= 0 {
		selection.entityLimit = 3
	}
	selection.phraseLimit = policy.MaxImportantPhrasesPerSegment
	if selection.phraseLimit <= 0 {
		selection.phraseLimit = 3
	}
	selection.wordLimit = policy.MaxImportantWordsPerSegment
	if selection.wordLimit <= 0 {
		selection.wordLimit = 3
	}
	if selection.includeEntities {
		language := strings.TrimSpace(generationPlanLanguage(plan))
		if language == "" {
			return semanticEntitySelection{}, fmt.Errorf("visualner extract: source language is required")
		}
		nerLimit := selection.entityLimit
		if policy.HasCategoryOnlyIncludes() || semantics.HasAdditionalEntityCategory(policy) {
			// Typed requests need a wider candidate window because VisualNER
			// ranks imageable names before values and applies top-N internally.
			nerLimit = min(max(selection.entityLimit*8, 12), 100)
		}
		entities, err := e.nerPort.Extract(ctx, language, sourceText, nerLimit)
		if err != nil {
			return semanticEntitySelection{}, fmt.Errorf("visualner extract: %w", err)
		}
		selection.entities = entities
	}
	if !selection.includeEntities {
		selection.entities = nil
		// Phrase and word selection remains independent of VisualNER.
		selection.includeSpecialNames = false
	}
	if err := ValidateVisualEntities(sourceText, selection.entities); err != nil {
		return semanticEntitySelection{}, fmt.Errorf("visualner contract: %w", err)
	}
	selection.entities = filterSemanticEntities(policy, selection.entities, selection.entityLimit)
	return selection, nil
}

func filterSemanticEntities(policy mediadomain.MediaExtractionPolicy, entities []contract.VisualEntity, limit int) []contract.VisualEntity {
	semantics.NormalizeVisualEntityTypes(entities)
	if policy.HasCategoryOnlyIncludes() {
		filtered := make([]contract.VisualEntity, 0, len(entities))
		for _, entity := range entities {
			if policy.IncludesEntityType(string(entity.Type)) {
				filtered = append(filtered, entity)
			}
		}
		entities = filtered
	}
	if !policy.Includes(mediadomain.ExtractionIncludeImportantPhrases) && !policy.Includes(mediadomain.ExtractionIncludeImportantWords) {
		entities = semantics.FilterOverlayCategoryEntities(entities)
	}
	entities = semantics.DeduplicateVisualEntities(entities)
	if policy.HasCategoryOnlyIncludes() {
		return semantics.LimitTranslatedVisualEntities(entities, limit)
	}
	if policy.Includes(mediadomain.ExtractionIncludeEntities) && semantics.HasAdditionalEntityCategory(policy) {
		return semantics.LimitVisualIdentityEntities(entities, limit)
	}
	return semantics.LimitTranslatedVisualEntities(entities, limit)
}

func categoryIncludesImageableIdentity(policy mediadomain.MediaExtractionPolicy) bool {
	// Broad entities includes named identities; a value selector added beside
	// it must not suppress their image lane. Category-only value requests stay
	// non-imageable unless persons or brands were explicitly selected too.
	return policy.Includes(mediadomain.ExtractionIncludeEntities) ||
		policy.Includes(mediadomain.ExtractionIncludePersons) ||
		policy.Includes(mediadomain.ExtractionIncludeBrands)
}

func entityIndexVersion(entities []scriptpkg.ExtractedEntity) string {
	for _, entity := range entities {
		if version := strings.TrimSpace(entity.EntityIndex); version != "" {
			return version
		}
	}
	return ""
}

// canonicalSourceText selects the source wording committed by the plan.
// Generated scene copy is narration only and must never replace it.
func canonicalSourceText(plan *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene, segmentID string) string {
	// The commit boundary may carry the exact per-segment evidence. Prefer it
	// over plan lookup so concurrent/generated scene IDs can never fall back to
	// the global source brief or narration text.
	if scene.Metadata != nil && strings.TrimSpace(scene.Metadata.SourceText) != "" {
		return strings.TrimSpace(scene.Metadata.SourceText)
	}
	if plan != nil {
		for _, candidate := range plan.Segments {
			if strings.EqualFold(strings.TrimSpace(candidate.ID), segmentID) && strings.TrimSpace(candidate.SourceText) != "" {
				return strings.TrimSpace(candidate.SourceText)
			}
		}
		if scene.Index >= 0 && scene.Index < len(plan.Segments) {
			if source := strings.TrimSpace(plan.Segments[scene.Index].SourceText); source != "" {
				return source
			}
		}
	}
	return strings.TrimSpace(scene.Text)
}

// ValidateVisualEntities enforces the VisualNER grounding contract against the
// canonical source text the entities were extracted from. It takes the source
// string rather than a compiled SceneIR because the contract is about the
// immutable source identity, not about the compiled profile: binding it to a
// throwaway compile would make the check depend on work it never reads.
func ValidateVisualEntities(sourceText string, entities []contract.VisualEntity) error {
	for i, entity := range entities {
		text := strings.TrimSpace(entity.Text)
		if text == "" || entity.Start < 0 || entity.End <= entity.Start || entity.End > len(sourceText) {
			return fmt.Errorf("entity[%d] has invalid source span", i)
		}
		if sourceText[entity.Start:entity.End] != entity.Evidence ||
			!strings.EqualFold(sourceText[entity.Start:entity.End], text) {
			return fmt.Errorf("entity[%d] %q is not grounded in source_text", i, text)
		}
	}
	return nil
}

func entitiesToStrings(entities []contract.VisualEntity) []string {
	out := make([]string, 0, len(entities))
	for _, entity := range entities {
		out = append(out, entity.Text)
	}
	return out
}

func extractedToConcepts(entities []scriptpkg.ExtractedEntity) []scriptpkg.Entity {
	out := make([]scriptpkg.Entity, 0, len(entities))
	for _, entity := range entities {
		out = append(out, scriptpkg.Entity{Value: entity.Value, Type: entity.Type, Score: float32(entity.Confidence)})
	}
	return out
}

// SemanticProviderResolver implements SegmentProviderResolver using the new
// chain: it resolves candidates LOCAL FIRST via the stockintelligence
// resolver (Fase 5), then ranks them via the MediaSampler (Fase 4). The
// winner is bound as the segment's primary asset; the provider live path is
// consulted only when local-first did not satisfy the thresholds.
type SemanticProviderResolver struct {
	stockResolver LocalStockResolverPort
	samplerPort   scriptports.MediaSamplerPort
}

// SemanticAndFanoutResolver composes local-first video selection with the
// canonical provider fanout. The semantic cutover must not suppress image
// discovery: stock intelligence owns primary-video selection, while the
// existing fanout remains responsible for internet images/generation.
type SemanticAndFanoutResolver struct {
	semantic SegmentProviderResolver
	fanout   SegmentProviderResolver
}

func NewSemanticAndFanoutResolver(semantic, fanout SegmentProviderResolver) (SegmentProviderResolver, error) {
	if semantic == nil || fanout == nil {
		return nil, fmt.Errorf("scriptgeneration: semantic and provider fanout are required")
	}
	return &SemanticAndFanoutResolver{semantic: semantic, fanout: fanout}, nil
}
