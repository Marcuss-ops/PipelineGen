// Package scriptgeneration — vidrush_semantic_chain.go owns the Fase 1-5
// semantic cutover: the new implementations of SegmentEnricher,
// SegmentProviderResolver and the barrier MediaCert hook that replace the
// legacy extractor/chooser with the SceneIR → VisualNER → MediaSampler →
// Local Stock → MediaCert chain.
//
// The implementations are a big-bang replacement of the legacy ports
// (per the cutover decision): VidRushPipeline now wires
// SceneIRSegmentEnricher + SemanticProviderResolver instead of the legacy
// enricher/resolver, and the coordinator's barrier wraps in
// MediaCertBarrier so a SUCCEEDED run with CERTIFIED=false fails the job.
//
// The Rust crates (rust/visualner, rust/mediasampler) are invoked through
// the VisualNERPort / MediaSamplerPort interfaces so production can swap
// in the stdio-JSON FFI adapter without the coordinator knowing about
// process spawning. The stockintelligence and mediacert packages are pure
// Go and are called directly.
package scriptgeneration

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediacert"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/stockintelligence"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/sceneir"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VisualEntity is the source-grounded entity produced by the VisualNER
// Rust crate. It mirrors rust/visualner::VisualEntity so the FFI adapter
// can decode the crate's JSON output without translation.
type VisualEntity struct {
	Text     string               `json:"text"`
	Type     scriptpkg.EntityType `json:"type"`
	Score    float32              `json:"score"`
	Start    int                  `json:"start"`
	End      int                  `json:"end"`
	Evidence string               `json:"evidence,omitempty"`
}

// VisualNERPort extracts source-grounded visual entities from a scene's
// source text. The deterministic Rust crate (rust/visualner) is the
// production implementation; the rule it enforces is NO EVIDENCE → NO ENTITY.
type VisualNERPort interface {
	Extract(ctx context.Context, sourceText string, entityCount int) ([]VisualEntity, error)
}

// LocalStockResolverPort is the LOCAL FIRST PROVIDER SECOND resolver. The
// stockintelligence.Service is the production implementation; it consults the
// local Qdrant search + SQLite hydrate first and falls back to the provider
// only when local_candidates < threshold or best_score < minimum_quality.
type LocalStockResolverPort interface {
	Resolve(ctx context.Context, req stockintelligence.ResolveRequest) (stockintelligence.ResolveResult, error)
}

// MediaCertifierPort certifies a completed VidRush run against a spec. The
// mediacert.Certify function is the production implementation. A
// CERTIFIED=false report must fail the job even when JobStatus=SUCCEEDED.
type MediaCertifierPort interface {
	Certify(ctx context.Context, spec mediacert.Spec, result mediacert.MediaResult) (mediacert.Report, error)
}

// MediaCertifierFunc adapts the canonical mediacert.Certify function to the
// pipeline boundary. It deliberately contains no certification rules.
type MediaCertifierFunc func(context.Context, mediacert.Spec, mediacert.MediaResult) (mediacert.Report, error)

func (f MediaCertifierFunc) Certify(ctx context.Context, spec mediacert.Spec, result mediacert.MediaResult) (mediacert.Report, error) {
	return f(ctx, spec, result)
}

// MediaCertSpecResolver creates the run-specific contract from the resolved
// plan instead of using a hard-coded fixture in production.
type MediaCertSpecResolver interface {
	ResolveMediaCertSpec(*scriptpkg.ResolvedGenerationPlan) mediacert.Spec
}

type MediaCertSpecResolverFunc func(*scriptpkg.ResolvedGenerationPlan) mediacert.Spec

func (f MediaCertSpecResolverFunc) ResolveMediaCertSpec(plan *scriptpkg.ResolvedGenerationPlan) mediacert.Spec {
	return f(plan)
}

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
	// VisualNER returns source-grounded noun phrases, while the downstream
	// overlay/document surfaces also need one explicit editorial phrase. Keep
	// that phrase grounded in the same extracted evidence.
	if includeImportantPhrases {
		for _, entity := range entities {
			if strings.Contains(strings.TrimSpace(entity.Text), " ") {
				entityResult.ImportantPhrases = []string{entity.Text}
				break
			}
		}
	}
	ir, err = sceneir.Compile(sceneir.CompileInput{Segment: segment, NarrationOverride: narrationText, EntityResult: &entityResult})
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("sceneir enrich profile: %w", err)
	}

	visual := scriptpkg.BuildSegmentVisualProfile(ir.Profile)
	visualProfile := &visual
	artlistQueries := scriptpkg.BuildArtlistQueries(ir.Profile, 5)
	// Entity extraction is the source for identity-image queries. When a PERSON
	// exists, image lookup is narrowed to that best named identity; otherwise
	// the historical entity fan-out is preserved. Do not replace this with the
	// broader visual-profile query builder, which defeats entity caching.
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

// imageSearchEntities derives the identity surface from the normal NLP
// entities. PERSON is the canonical named-identity surface for image search;
// when a text contains one or more PERSON entities, only the best one is sent
// to the image provider. All extracted entities remain available in the NLP
// result and overlay annotations.
func imageSearchEntities(entities []VisualEntity) []VisualEntity {
	if len(entities) == 0 {
		return nil
	}
	best := -1
	for i, entity := range entities {
		if !strings.EqualFold(string(entity.Type), string(scriptpkg.EntityTypePerson)) {
			continue
		}
		if best < 0 || visualEntityRanksBefore(entity, entities[best]) {
			best = i
		}
	}
	if best < 0 {
		return entities
	}
	return []VisualEntity{entities[best]}
}

func visualEntityRanksBefore(candidate, current VisualEntity) bool {
	if candidate.Score != current.Score {
		return candidate.Score > current.Score
	}
	if candidate.Start != current.Start {
		return candidate.Start < current.Start
	}
	if candidate.End != current.End {
		return candidate.End < current.End
	}
	return strings.TrimSpace(candidate.Text) < strings.TrimSpace(current.Text)
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

// MediaCertBarrier wraps a VidRushBarrier and runs mediacert.Certify on the
// completed results before returning them. A CERTIFIED=false report fails the
// job even when the underlying barrier returned no error. This is the explicit
// rejection of the count-only test that declared success at a semantically
// broken pipeline (e.g. a boxing clip bound to Greek Salad).
type MediaCertBarrier struct {
	inner     VidRushBarrier
	certifier MediaCertifierPort
	spec      mediacert.Spec
}

// NewMediaCertBarrier wraps a barrier with a MediaCertifierPort + Spec. The
// spec is the golden Mediterranean fixture's expected contract in production;
// tests pass a synthetic spec. inner and certifier must be non-nil.
func NewMediaCertBarrier(inner VidRushBarrier, certifier MediaCertifierPort, spec mediacert.Spec) (*MediaCertBarrier, error) {
	if inner == nil {
		return nil, fmt.Errorf("scriptgeneration: inner VidRushBarrier is required for MediaCertBarrier")
	}
	if certifier == nil {
		return nil, fmt.Errorf("scriptgeneration: MediaCertifierPort is required for MediaCertBarrier")
	}
	return &MediaCertBarrier{inner: inner, certifier: certifier, spec: spec}, nil
}

// WaitForVidRush delegates to the inner barrier, then certifies the result.
// A CERTIFIED=false report returns an error so the runner fails the job.
func (b *MediaCertBarrier) WaitForVidRush(ctx context.Context, runID string) ([]scriptpkg.VidRushSegmentResult, error) {
	segments, err := b.inner.WaitForVidRush(ctx, runID)
	if err != nil {
		return nil, err
	}
	segments = filterEntityRenderSurface(segments)
	result := mediacert.MediaResult{
		JobStatus: "SUCCEEDED",
		Segments:  toMediaResultSegments(segments),
	}
	report, err := b.certifier.Certify(ctx, b.spec, result)
	if err != nil {
		return nil, fmt.Errorf("mediacert certify: %w", err)
	}
	if !report.Certified {
		var violations []string
		for _, c := range report.Checks {
			if !c.Passed {
				detail := string(c.Name)
				if len(c.Violations) > 0 {
					detail += ": " + c.Violations[0].Detail
				}
				violations = append(violations, detail)
			}
		}
		return nil, fmt.Errorf("vidrush semantic certification failed: CERTIFIED=false (%s)", strings.Join(violations, ", "))
	}
	return segments, nil
}

// filterEntityRenderSurface is the explicit product policy for the entity
// render path: only imageable named entities and important phrases cross the
// VidRush→render boundary. Value entities, concepts, keywords and important
// words remain useful to other editorial paths, but must not become entity
// overlays or affect the entity certification counts.
func filterEntityRenderSurface(segments []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	out := make([]scriptpkg.VidRushSegmentResult, len(segments))
	for i, seg := range segments {
		out[i] = seg
		entities := make([]scriptpkg.ExtractedEntity, 0, len(seg.Insights.Entities))
		allowedValues := make(map[string]struct{}, len(seg.Insights.Entities))
		for _, entity := range seg.Insights.Entities {
			kind := scriptpkg.NormalizeAnnotationType(entity.Type)
			if !scriptpkg.IsAnnotationEntityKind(kind) {
				continue
			}
			entity.Type = kind
			entity.Value = strings.TrimSpace(entity.Value)
			if entity.Value == "" {
				continue
			}
			entities = append(entities, entity)
			allowedValues[strings.ToLower(entity.Value)] = struct{}{}
		}
		// Entity-image extraction deliberately selects the best PERSON as the
		// protagonist query. Keep that same selected identity on the render
		// surface; otherwise the full NLP entity list (which remains available
		// in the source result) creates a false fanout mismatch at certification.
		// Only narrow when at least one query matches an imageable entity, so
		// generic non-entity runs retain their historical entity surface.
		queryValues := make(map[string]struct{}, len(seg.Insights.ImageQueries))
		for _, query := range seg.Insights.ImageQueries {
			if value := normalizeProtagonistQuery(query); value != "" {
				queryValues[value] = struct{}{}
			}
		}
		if len(queryValues) > 0 && len(queryValues) < len(entities) {
			// Provider enrichment may decorate a selected query (for example
			// with "portrait") and therefore not compare equal to the source
			// surface. Select exactly one source entity per distinct query, with
			// the first PERSON as the deterministic protagonist fallback. This
			// keeps the render/certification surface aligned with the image fanout
			// while the complete NLP entity list remains upstream in the result.
			matched := make([]scriptpkg.ExtractedEntity, 0, len(queryValues))
			used := make(map[string]struct{}, len(queryValues))
			for query := range queryValues {
				for _, entity := range entities {
					if normalizeProtagonistQuery(entity.Value) == query {
						matched = append(matched, entity)
						used[normalizeProtagonistQuery(entity.Value)] = struct{}{}
						break
					}
				}
			}
			if len(matched) < len(queryValues) {
				for _, entity := range entities {
					if !strings.EqualFold(entity.Type, "PERSON") {
						continue
					}
					key := normalizeProtagonistQuery(entity.Value)
					if _, ok := used[key]; ok {
						continue
					}
					matched = append(matched, entity)
					used[key] = struct{}{}
					if len(matched) == len(queryValues) {
						break
					}
				}
			}
			if len(matched) < len(queryValues) {
				for _, entity := range entities {
					key := normalizeProtagonistQuery(entity.Value)
					if _, ok := used[key]; ok {
						continue
					}
					matched = append(matched, entity)
					used[key] = struct{}{}
					if len(matched) == len(queryValues) {
						break
					}
				}
			}
			if len(matched) > 0 {
				entities = matched
				allowedValues = make(map[string]struct{}, len(entities))
				for _, entity := range entities {
					allowedValues[strings.ToLower(entity.Value)] = struct{}{}
				}
			}
		}
		out[i].Insights.Entities = entities
		out[i].Insights.ImportantWords = nil
		// This render surface is intentionally narrower than the full media
		// retrieval surface: no stock-video or YouTube query may leak into an
		// entity-only run. The caller can run those providers in a separate
		// clip path, but they are not extracted here.
		out[i].Insights.ArtlistQueries = nil
		out[i].Insights.YouTubeQueries = nil
		// The entity value is the only allowed image query at this boundary.
		// Do not trust provider-generated/enriched queries: they can introduce
		// a second subject or a generic scene image and break entity↔asset
		// provenance. One deterministic query is emitted per imageable entity.
		queries := make([]string, 0, len(entities))
		seenQueries := make(map[string]struct{}, len(entities))
		for _, entity := range entities {
			q := strings.TrimSpace(entity.Value)
			key := strings.ToLower(q)
			if q == "" || key == "" {
				continue
			}
			if _, ok := allowedValues[key]; !ok {
				continue
			}
			if _, ok := seenQueries[key]; ok {
				continue
			}
			seenQueries[key] = struct{}{}
			queries = append(queries, q)
		}
		out[i].Insights.ImageQueries = queries
	}
	return out
}

func normalizeProtagonistQuery(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(strings.TrimSuffix(value, "'s"), "’s")
	return strings.Join(strings.Fields(value), " ")
}

// toMediaResultSegments projects the VidRushSegmentResult slice into the
// mediacert.ResultSegment shape so the certifier can check identity, profile,
// grounding, ownership, relevance and fanout without depending on the full
// VidRush wire shape.
func toMediaResultSegments(segments []scriptpkg.VidRushSegmentResult) []mediacert.ResultSegment {
	out := make([]mediacert.ResultSegment, 0, len(segments))
	for _, seg := range segments {
		profile := seg.CanonicalSemanticProfile()
		insights := seg.Insights
		if insights.VisualProfile == nil {
			visual := scriptpkg.BuildSegmentVisualProfile(profile)
			insights.VisualProfile = &visual
		}
		out = append(out, mediacert.ResultSegment{
			SegmentID:       seg.SegmentID,
			Position:        seg.Position,
			SourceText:      seg.Text,
			SourceTextHash:  seg.TextHash,
			SemanticProfile: &profile,
			Insights:        insights,
			Assets:          seg.Assets,
		})
	}
	return out
}

// Compile-time contract assertions: the new implementations satisfy the
// existing port interfaces so VidRushPipeline can swap them in without the
// coordinator knowing about the new chain.
var (
	_ SegmentEnricher         = (*SceneIRSegmentEnricher)(nil)
	_ SegmentProviderResolver = (*SemanticProviderResolver)(nil)
	_ VidRushBarrier          = (*MediaCertBarrier)(nil)
)
