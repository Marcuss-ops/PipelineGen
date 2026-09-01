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
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/sceneir"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VisualEntity is the source-grounded entity produced by the VisualNER
// Rust crate. It mirrors rust/visualner::VisualEntity so the FFI adapter
// can decode the crate's JSON output without translation.
type VisualEntity struct {
	Text     string  `json:"text"`
	Score    float32 `json:"score"`
	Start    int     `json:"start"`
	End      int     `json:"end"`
	Evidence string  `json:"evidence,omitempty"`
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

	entityCount := 3
	entities, err := e.nerPort.Extract(ctx, ir.SourceText, entityCount)
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("visualner extract: %w", err)
	}
	if err := validateVisualEntities(ir, entities); err != nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("visualner contract: %w", err)
	}

	extractedEntities := make([]scriptpkg.ExtractedEntity, 0, len(entities))
	imageQueries := make([]string, 0, len(entities))
	for _, ve := range entities {
		extractedEntities = append(extractedEntities, scriptpkg.ExtractedEntity{
			Value:      ve.Text,
			Type:       "VISUAL_SUBJECT",
			Confidence: float64(ve.Score),
		})
		imageQueries = append(imageQueries, ve.Text)
	}
	// Recompile the same SceneIR with the extractor result. This keeps the
	// canonical profile as the only semantic owner while making the newly
	// grounded visual entities available to the canonical query builders.
	entityResult := scriptpkg.EntityResult{
		NounChunks: entitiesToStrings(entities),
		Concepts:   extractedToConcepts(extractedEntities),
	}
	ir, err = sceneir.Compile(sceneir.CompileInput{Segment: segment, NarrationOverride: narrationText, EntityResult: &entityResult})
	if err != nil {
		return scriptpkg.VidRushSegmentResult{}, fmt.Errorf("sceneir enrich profile: %w", err)
	}

	visual := scriptpkg.BuildSegmentVisualProfile(ir.Profile)
	visualProfile := &visual
	artlistQueries := scriptpkg.BuildArtlistQueries(ir.Profile, 5)
	imageQueries = scriptpkg.BuildImageQueries(ir.Profile, entityCount)
	result := scriptpkg.VidRushSegmentResult{
		SegmentID:       ir.SegmentID,
		SceneID:         scene.ID,
		Position:        ir.Position,
		Text:            ir.SourceText,
		TextHash:        ir.SourceTextHash,
		ExecutionMode:   scene.ExecutionMode,
		SemanticProfile: &ir.Profile,
		Insights: scriptpkg.SegmentInsights{
			SegmentID:      ir.SegmentID,
			TextHash:       ir.SourceTextHash,
			VisualProfile:  visualProfile,
			Entities:       extractedEntities,
			ArtlistQueries: artlistQueries,
			ImageQueries:   imageQueries,
		},
	}
	return result, nil
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
				violations = append(violations, string(c.Name))
			}
		}
		return nil, fmt.Errorf("vidrush semantic certification failed: CERTIFIED=false (%s)", strings.Join(violations, ", "))
	}
	return segments, nil
}

// toMediaResultSegments projects the VidRushSegmentResult slice into the
// mediacert.ResultSegment shape so the certifier can check identity, profile,
// grounding, ownership, relevance and fanout without depending on the full
// VidRush wire shape.
func toMediaResultSegments(segments []scriptpkg.VidRushSegmentResult) []mediacert.ResultSegment {
	out := make([]mediacert.ResultSegment, 0, len(segments))
	for _, seg := range segments {
		out = append(out, mediacert.ResultSegment{
			SegmentID:      seg.SegmentID,
			Position:       seg.Position,
			SourceText:     seg.Text,
			SourceTextHash: seg.TextHash,
			Insights:       seg.Insights,
			Assets:         seg.Assets,
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
