// Package scriptgeneration — vidrush_semantic_chain.go owns the Fase 1-5
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
// The port/value contracts live in vidrush_semantic_ports.go, which is also
// where the pure grounding / entity fan-out helpers were moved (split
// 2026-09-16), alongside the barrier in vidrush_mediacert_barrier.go (split
// 2026-09-12). Both splits exist to keep every file under
// max_lines_per_file_strict=600 (godlike/08).
package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/sceneir"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// SceneIRSegmentEnricher implements SegmentEnricher using the new chain:
// it compiles a SceneIR from the committed scene (Fase 1, immutable source
// identity), then extracts source-grounded entities via VisualNER (Fase 3).
// The returned VidRushSegmentResult carries the SceneIR's immutable identity
// + the VisualNER entities, so downstream provider search consumes
// SourceText + Profile (never NarrationText).
type SceneIRSegmentEnricher struct {
	nerPort VisualNERPort
}

// NewSceneIRSegmentEnricher wires the new enricher. nerPort must be non-nil.
func NewSceneIRSegmentEnricher(nerPort VisualNERPort) (*SceneIRSegmentEnricher, error) {
	if nerPort == nil {
		return nil, fmt.Errorf("scriptgeneration: VisualNERPort is required for SceneIRSegmentEnricher")
	}
	return &SceneIRSegmentEnricher{nerPort: nerPort}, nil
}

// Enrich compiles a SceneIR from the committed scene and extracts entities.
// It is the Fase 1 + Fase 3 replacement for the legacy entity extractor.
func (e *SceneIRSegmentEnricher) Enrich(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, scene scriptpkg.SpecScene) (scriptpkg.VidRushSegmentResult, error) {
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
		ID:         segmentID,
		Position:   scene.Index,
		Text:       narrationText,
		SourceText: sourceText,
	}
	if scene.ExecutionMode != "" {
		segment.ExecutionMode = scene.ExecutionMode
	}
	ir, err := sceneir.Compile(sceneir.CompileInput{Segment: segment, NarrationOverride: narrationText})
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("sceneir enrich: %w", err)
	}

	// The payload can explicitly narrow semantic extraction to the surfaces
	// needed by the current production pass. Keep the historical default of
	// three entities when no limit is supplied.
	extraction := mediadomain.MediaExtractionPolicy{}
	if plan != nil {
		extraction = plan.MediaPlan.Extraction
	}
	includeEntities := extraction.Includes(mediadomain.ExtractionIncludeEntities) || extraction.Includes(mediadomain.ExtractionIncludeSpecialNames)
	includeImportantPhrases := extraction.Includes(mediadomain.ExtractionIncludeImportantPhrases)
	includeImportantWords := extraction.Includes(mediadomain.ExtractionIncludeImportantWords)
	includeSpecialNames := extraction.Includes(mediadomain.ExtractionIncludeSpecialNames)
	entityCount := extraction.MaxEntitiesPerSegment
	if entityCount <= 0 {
		entityCount = 3
	}
	var entities []VisualEntity
	if includeEntities {
		entities, err = e.nerPort.Extract(ctx, ir.SourceText, entityCount)
		if err != nil {
			return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("visualner extract: %w", err)
		}
	}
	if !includeEntities {
		entities = nil
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
	if err := validateVisualEntities(ir, entities); err != nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("visualner contract: %w", err)
	}
	entities = deduplicateVisualEntities(entities)
	// Enforce the caller's per-scene limit before image-query fanout and
	// certification consume the VisualNER identities.
	entities = limitTranslatedVisualEntities(entities, entityLimit)

	extractedEntities := make([]scriptpkg.ExtractedEntity, 0, len(entities))
	for _, ve := range entities {
		if ve.Type == scriptpkg.EntityTypePerson {
			// VisualNER can occasionally include a sentence connector and
			// possessive in the PERSON span ("While Dolly Parton's"). The
			// entity image contract needs the canonical identity for both
			// provider queries and durable overlay IDs.
			ve.Text = normalizeVisualPersonName(ve.Text)
		}
		entityType := ve.Type
		if strings.TrimSpace(string(entityType)) == "" {
			// Test/dry-run ports predating the typed contract are treated as
			// visual concepts; the production Rust adapter always supplies a
			// concrete value from the closed vocabulary.
			entityType = scriptpkg.EntityTypeVisualConcept
		}
		extractedEntities = append(extractedEntities, scriptpkg.ExtractedEntity{
			Value:      ve.Text,
			Type:       string(entityType),
			Confidence: float64(ve.Score),
		})
	}
	imageEntities := imageSearchEntities(entities)
	imageQueries := make([]string, 0, len(imageEntities))
	imageAnchor := visualImageAnchor(ir.SourceText)
	for _, ve := range imageEntities {
		query := strings.TrimSpace(ve.Text)
		if !extraction.EntityImageSurfaceEnabled() && imageAnchor != "" && query != "" && !strings.Contains(strings.ToLower(query), strings.ToLower(imageAnchor)) {
			query = imageAnchor + " " + query
		}
		imageQueries = append(imageQueries, query)
	}
	var phraseCandidates []string
	if includeImportantPhrases || includeImportantWords {
		phraseCandidates = deterministicImportantPhrases(ir.SourceText, entities, phraseLimit, generationPlanLanguage(plan))
	}
	var importantPhrases []string
	if includeImportantPhrases {
		candidates := append([]string(nil), extraction.ImportantPhrases...)
		candidates = append(candidates, phraseCandidates...)
		importantPhrases = groundImportantPhrases(ir.SourceText, entities, candidates, phraseLimit)
	}
	var importantWords []string
	if includeImportantWords {
		importantWords = deterministicImportantWords(phraseCandidates, wordLimit, generationPlanLanguage(plan))
	}
	var specialNames []string
	if includeSpecialNames {
		specialNames = translatedSpecialNames(ir.SourceText, nil, entities, entityLimit)
	}

	// Recompile the same SceneIR with the VisualNER and deterministic editorial
	// surfaces. This keeps the canonical profile as the only semantic owner.
	entityResult := scriptpkg.EntityResult{
		NounChunks:       entitiesToStrings(entities),
		Concepts:         extractedToConcepts(extractedEntities),
		ImportantPhrases: importantPhrases,
		ImportantWords:   importantWords,
		SpecialNames:     specialNames,
	}
	ir, err = sceneir.Compile(sceneir.CompileInput{Segment: segment, NarrationOverride: narrationText, EntityResult: &entityResult})
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("sceneir enrich profile: %w", err)
	}

	visual := scriptpkg.BuildSegmentVisualProfile(ir.Profile)
	visualProfile := &visual
	artlistQueries := scriptpkg.BuildArtlistQueries(ir.Profile, 5)
	// Entity extraction is the source for identity-image queries. PERSON
	// identities are queried independently; the broader visual-profile query
	// builder is not used because it defeats entity-level caching.
	if !includeEntities {
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
			ArtlistQueries: artlistQueries,
			ImageQueries:   imageQueries,
		},
	}
	return result, nil
}

// deduplicateVisualEntities collapses grammatical variants of one identity
// before the entity/image fanout. VisualNER may return both a canonical name
// and a sentence surface such as "While Dolly Parton's"; those are one
// person, hence one image query and one overlay.
func deduplicateVisualEntities(entities []VisualEntity) []VisualEntity {
	if len(entities) < 2 {
		return entities
	}
	out := make([]VisualEntity, 0, len(entities))
	seen := make(map[string]struct{}, len(entities))
	for _, entity := range entities {
		value := strings.TrimSpace(entity.Text)
		if entity.Type == scriptpkg.EntityTypePerson {
			value = normalizeVisualPersonName(value)
		}
		key := strings.ToLower(strings.Join(strings.Fields(value), " "))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		entity.Text = value
		out = append(out, entity)
	}
	return out
}

// visualImageAnchor extracts the subject phrase from the first source clause.
// VisualNER may return useful ingredients or actions while omitting the main
// subject; retaining this short source-grounded anchor prevents searches for
// "olive oil" or "wide pan" from drifting to unrelated stock imagery.
func visualImageAnchor(source string) string {
	first := strings.TrimSpace(strings.SplitN(source, ".", 2)[0])
	if first == "" {
		return ""
	}
	lower := strings.ToLower(first)
	for _, marker := range []string{" consists ", " contains ", " combines ", " is ", " are ", " was ", " were "} {
		if idx := strings.Index(lower, marker); idx > 0 {
			return strings.TrimSpace(first[:idx])
		}
	}
	return first
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

func validateVisualEntities(ir sceneir.SceneIR, entities []VisualEntity) error {
	for i, entity := range entities {
		text := strings.TrimSpace(entity.Text)
		if text == "" || entity.Start < 0 || entity.End <= entity.Start || entity.End > len(ir.SourceText) {
			return fmt.Errorf("entity[%d] has invalid source span", i)
		}
		if ir.SourceText[entity.Start:entity.End] != entity.Evidence ||
			!strings.EqualFold(ir.SourceText[entity.Start:entity.End], text) {
			return fmt.Errorf("entity[%d] %q is not grounded in source_text", i, text)
		}
	}
	return nil
}

func groundNamedVisualEntities(source string, candidates []VisualEntity) []VisualEntity {
	runes := []rune(source)
	grounded := make([]VisualEntity, 0, len(candidates))
	for _, candidate := range candidates {
		span, ok := findEntitySpan(source, candidate.Text)
		if !ok || span.StartRune < 0 || span.EndRune > len(runes) {
			continue
		}
		start := len(string(runes[:span.StartRune]))
		end := len(string(runes[:span.EndRune]))
		candidate.Text = span.Text
		candidate.Start = start
		candidate.End = end
		candidate.Evidence = source[start:end]
		grounded = append(grounded, candidate)
	}
	return grounded
}

func entitiesToStrings(entities []VisualEntity) []string {
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

func (r *SemanticAndFanoutResolver) ResolveProviders(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	updated, err := r.semantic.ResolveProviders(ctx, plan, segment)
	if err != nil {
		return segment, err
	}
	if plan == nil {
		return updated, nil
	}
	fanoutPlan := *plan
	fanoutPlan.MediaPlan = plan.MediaPlan.Clone()
	// Artlist was already handled by the semantic resolver. Prevent a second
	// live search while preserving the caller's image/generation policy.
	fanoutPlan.MediaPlan.ProviderPolicy.Artlist = mediadomain.MediaToggleDisabled
	return r.fanout.ResolveProviders(ctx, &fanoutPlan, updated)
}

// NewSemanticProviderResolver wires the new resolver. Both ports must be non-nil.
func NewSemanticProviderResolver(stockResolver LocalStockResolverPort, samplerPort scriptports.MediaSamplerPort) (*SemanticProviderResolver, error) {
	if stockResolver == nil {
		return nil, fmt.Errorf("scriptgeneration: LocalStockResolverPort is required for SemanticProviderResolver")
	}
	if samplerPort == nil {
		return nil, fmt.Errorf("scriptgeneration: MediaSamplerPort is required for SemanticProviderResolver")
	}
	return &SemanticProviderResolver{stockResolver: stockResolver, samplerPort: samplerPort}, nil
}

// ResolveProviders resolves candidates LOCAL FIRST and ranks them via the
// MediaSampler. It is the Fase 4 + Fase 5 replacement for the legacy chooser.
func (r *SemanticProviderResolver) ResolveProviders(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	// The stock resolver owns video discovery and must never bypass the plan's
	// provider policy. Image-only/generation-only plans still use the later
	// image materialization stages, but must not probe local video stock or
	// fall back to Artlist.
	if plan == nil || !plan.MediaPlan.ProviderPolicy.Artlist.AsBool() {
		segment.Cache.InternetImagesProviderSearches = 0
		return segment, nil
	}
	subject := ""
	terms := []string{}
	if segment.Insights.VisualProfile != nil {
		subject = segment.Insights.VisualProfile.Subject
		terms = segment.Insights.VisualProfile.Terms
	}
	query := subject
	if query == "" && len(terms) > 0 {
		query = terms[0]
	}

	stockReq := stockintelligence.ResolveRequest{
		SegmentID:   segment.SegmentID,
		Subject:     subject,
		VisualTerms: terms,
		Query:       query,
	}
	stockRes, err := r.stockResolver.Resolve(ctx, stockReq)
	if err != nil {
		return segment, fmt.Errorf("stockintelligence resolve: %w", err)
	}

	samplerCands := make([]scriptpkg.SegmentAssetCandidate, 0, len(stockRes.Candidates))
	for _, c := range stockRes.Candidates {
		samplerCands = append(samplerCands, scriptpkg.SegmentAssetCandidate{
			AssetID: c.AssetID, Entity: c.Label, RelevanceScore: float64(c.GenericSimilarity), SegmentID: c.OwnerSegmentID,
		})
	}
	winnerID, err := r.samplerPort.Sample(ctx, segment.SegmentID, subject, terms, samplerCands, false)
	if err != nil {
		return segment, fmt.Errorf("mediasampler sample: %w", err)
	}

	if winnerID != "" {
		primary := scriptpkg.SegmentAssetCandidate{
			SegmentID: segment.SegmentID,
			AssetID:   winnerID,
			Provider:  scriptpkg.VidRushProviderArtlist,
			Score:     0.9,
		}
		for _, c := range stockRes.Candidates {
			if c.AssetID == winnerID {
				primary.Entity = c.Label
				primary.Query = query
				primary.RelevanceScore = float64(c.GenericSimilarity)
				break
			}
		}
		segment.Assets.PrimaryVideo = &primary
	}
	// Record the provider live request count on the segment's cache state so
	// MediaCert can assert the LOCAL FIRST PROVIDER SECOND invariant.
	segment.Cache.InternetImagesProviderSearches = stockRes.ProviderLiveRequests
	return segment, nil
}
