// Package scriptgeneration — vidrush_semantic_chain.go owns the Fase 1-5
// semantic cutover: the new implementations of SegmentEnricher and
// SegmentProviderResolver that replace the legacy extractor/chooser with the
// SceneIR → VisualNER → MediaSampler → Local Stock → MediaCert chain.
//
// The implementations are a big-bang replacement of the legacy ports
// (per the cutover decision): VidRushPipeline now wires
// SceneIRSegmentEnricher + SemanticProviderResolver instead of the legacy
// enricher/resolver, and the coordinator's barrier wraps in MediaCertBarrier
// so a SUCCEEDED run with CERTIFIED=false fails the job.
//
// The port/value contracts live in vidrush_semantic_ports.go and the barrier
// in vidrush_mediacert_barrier.go (split 2026-09-12 to keep every file under
// max_lines_per_file_strict=600, godlike/08).
package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"unicode"

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
	nerPort         VisualNERPort
	phraseExtractor ImportantPhraseExtractor
}

// NewSceneIRSegmentEnricher wires the new enricher. nerPort must be non-nil.
func NewSceneIRSegmentEnricher(nerPort VisualNERPort) (*SceneIRSegmentEnricher, error) {
	if nerPort == nil {
		return nil, fmt.Errorf("scriptgeneration: VisualNERPort is required for SceneIRSegmentEnricher")
	}
	return &SceneIRSegmentEnricher{nerPort: nerPort}, nil
}

// SetImportantPhraseExtractor injects the NLP phrase extractor. The
// enrichment path never invents phrases when this port is absent; this keeps
// deterministic entity NER and model-driven phrase extraction distinct.
func (e *SceneIRSegmentEnricher) SetImportantPhraseExtractor(extractor ImportantPhraseExtractor) {
	if e != nil {
		e.phraseExtractor = extractor
	}
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
		Text:       sourceText,
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
	if err := validateVisualEntities(ir, entities); err != nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("visualner contract: %w", err)
	}

	extractedEntities := make([]scriptpkg.ExtractedEntity, 0, len(entities))
	for _, ve := range entities {
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
	// Recompile the same SceneIR with the extractor result. This keeps the
	// canonical profile as the only semantic owner while making the newly
	// grounded visual entities available to the canonical query builders.
	entityResult := scriptpkg.EntityResult{
		NounChunks: entitiesToStrings(entities),
		Concepts:   extractedToConcepts(extractedEntities),
	}
	// Important phrases come from the injected NLP/model extractor. This
	// runner only validates and grounds its output; it never derives phrases
	// from the entity list.
	if includeImportantPhrases && e.phraseExtractor != nil {
		phrases, phraseErr := e.phraseExtractor.ExtractImportantPhrases(
			ctx, ir.SourceText, extraction.MaxImportantPhrasesPerSegment,
			generationPlanLanguage(plan), generationPlanModel(plan),
		)
		if phraseErr != nil {
			return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("important phrase extract: %w", phraseErr)
		}
		entityResult.ImportantPhrases = groundImportantPhrases(ir.SourceText, entities, phrases, extraction.MaxImportantPhrasesPerSegment)
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
		Text:            ir.SourceText,
		TextHash:        ir.SourceTextHash,
		ExecutionMode:   scene.ExecutionMode,
		SemanticProfile: &ir.Profile,
		Insights: scriptpkg.SegmentInsights{
			SegmentID:     ir.SegmentID,
			TextHash:      ir.SourceTextHash,
			VisualProfile: visualProfile,
			Entities:      extractedEntities,
			ImportantPhrases: func() []string {
				if !includeImportantPhrases {
					return nil
				}
				return append([]string(nil), ir.Profile.ImportantPhrases...)
			}(),
			ArtlistQueries: artlistQueries,
			ImageQueries:   imageQueries,
		},
	}
	return result, nil
}

func generationPlanLanguage(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil {
		return ""
	}
	return plan.Language
}

func generationPlanModel(plan *scriptpkg.ResolvedGenerationPlan) string {
	if plan == nil {
		return ""
	}
	return plan.Model
}

func groundImportantPhrases(source string, entities []VisualEntity, phrases []string, limit int) []string {
	if limit <= 0 {
		limit = len(phrases)
	}
	out := make([]string, 0, min(limit, len(phrases)))
	seen := make(map[string]struct{}, len(phrases))

	// Entity spans do not depend on the phrase being tested, so they are
	// resolved ONCE per segment instead of once per (phrase, entity) pair. The
	// previous nested resolution made this gate O(phrases × entities × source
	// length) on every scene.
	entitySpans := make([]scriptpkg.AnnotationSpan, 0, len(entities))
	for _, entity := range entities {
		if entitySpan, entityOK := findEntitySpan(source, entity.Text); entityOK {
			entitySpans = append(entitySpans, entitySpan)
		}
	}

	for _, phrase := range phrases {
		phrase = strings.TrimSpace(phrase)
		if len(strings.Fields(phrase)) < 2 {
			continue
		}
		// Keep the phrase surface free of proper-name runs even when the
		// entity extractor missed a name. This is a validation gate for the
		// model output, not a phrase generator or a replacement value.
		if containsProperNamePair(phrase) {
			continue
		}
		span, ok := findEntitySpan(source, phrase)
		if !ok {
			continue
		}
		// A phrase is editorial text, not an entity-bearing label. Reject
		// model spans that overlap any extracted named entity, including
		// partial forms such as "LeBron James and Johann" or "Sebastian
		// Bach". Those belong to the entity surface only.
		phraseOverlapsEntity := false
		for _, entitySpan := range entitySpans {
			if span.StartRune < entitySpan.EndRune && entitySpan.StartRune < span.EndRune {
				phraseOverlapsEntity = true
				break
			}
		}
		if phraseOverlapsEntity {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(span.Text))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, span.Text)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func containsProperNamePair(value string) bool {
	previousTitle := false
	// FieldsSeq iterates without materialising the []string that
	// strings.Fields would allocate for every candidate phrase.
	for raw := range strings.FieldsSeq(value) {
		word := strings.Trim(raw, ".,;:!?\"'’()[]{}")
		currentTitle := false
		for _, r := range word {
			currentTitle = unicode.IsUpper(r)
			break
		}
		if currentTitle && previousTitle {
			return true
		}
		previousTitle = currentTitle
	}
	return false
}

// imageSearchEntities derives the identity surface from the normal NLP
// entities. PERSON is the canonical named-identity surface for image search;
// every grounded PERSON is sent to the image provider so each identity can be
// indexed independently. If no PERSON exists, the historical entity fan-out
// is preserved.
func imageSearchEntities(entities []VisualEntity) []VisualEntity {
	if len(entities) == 0 {
		return nil
	}
	persons := make([]VisualEntity, 0, len(entities))
	for _, entity := range entities {
		if !strings.EqualFold(string(entity.Type), string(scriptpkg.EntityTypePerson)) {
			continue
		}
		persons = append(persons, entity)
	}
	if len(persons) > 0 {
		return persons
	}
	return append([]VisualEntity(nil), entities...)
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
