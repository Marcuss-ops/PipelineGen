// Package adapters — media_resolver_image_stage.go: the image-discovery
// stage of the canonical media resolver (provider candidates only; it never
// selects a winner).
//
// The entity-card half — which candidate may bind as a scene entity's image —
// lives in media_resolver_entity_binding.go (split 2026-09-12 to keep both
// halves under max_lines_per_file_strict=600, godlike/08).
package adapters

import (
	"context"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// normalizeEntityMatch is the identity join for entity-image candidates:
// case-folded, accent-stripped and English-possessive-stripped.
func normalizeEntityMatch(value string) string {
	decomposed := norm.NFD.String(strings.ToLower(strings.TrimSpace(value)))
	var b strings.Builder
	for _, r := range decomposed {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	// NER commonly emits an English possessive when the name is the subject
	// of a sentence ("Dwayne Johnson's journey"), while image candidates are
	// keyed by the canonical identity ("Dwayne Johnson").
	return strings.TrimSuffix(strings.TrimSuffix(b.String(), "'s"), "’s")
}

// internetImageCachePayload stores only the image-provider delta so a cache
type internetImageCachePayload struct {
	Candidates []scriptpkg.SegmentAssetCandidate
}

// entityImageL1Capacity bounds the canonical entity-key L1 (see
// vidrush_helpers.go for the VidRush L1 cache contract).
const entityImageL1Capacity = 4096

var (
	// entityImageCache is the bounded L1 for canonical entity key ->
	// []SegmentAssetCandidate (see vidrush_helpers.go for the L1 contract).
	entityImageCache = cacheutil.NewLRU(entityImageL1Capacity)
	// entityImageLocks is the canonical reference-counted per-key locker: it
	// serialises concurrent provider/catalog lookups for the same entity key
	// while leaving different keys fully parallel. Unlike the retired
	// sync.Map-of-mutexes registry it never leaks entries — the key is removed
	// when the last holder releases (pkg/concurrent.KeyedLocker).
	entityImageLocks = concurrent.NewKeyedLocker()
)

// MediaResolverImageStage handles image discovery for the canonical media
// resolver path. It attaches provider candidates but never selects a winner.
// attaches every unique result returned for the segment queries.
type MediaResolverImageStage struct {
	searcher InternetImageSearcher
	metrics  VidRushMetrics
	cache    scriptports.VidRushCachePort
	catalog  entitycatalog.Repository
	// semanticScorer may be absent for contextual images. Person-identity
	// candidates still fail closed at the binding boundary without a visual
	// verdict.
	semanticScorer SemanticCandidateScorer
	// manifestPins is the opt-in pinned-manifest fast path (see
	// media_manifest_shim.go). Nil keeps every call site byte-for-byte
	// identical to the un-pinned behavior.
	manifestPins *MediaManifestPins
}

func NewMediaResolverImageStageWithCatalog(searcher InternetImageSearcher, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushMetrics) *MediaResolverImageStage {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &MediaResolverImageStage{searcher: searcher, metrics: m, cache: cache, catalog: catalog}
}

// WithSemanticScorer attaches the optional semantic candidate scorer. Person
// identity candidates require a trusted verdict before selection.
func (p *MediaResolverImageStage) WithSemanticScorer(scorer SemanticCandidateScorer) *MediaResolverImageStage {
	if p != nil {
		p.semanticScorer = scorer
	}
	return p
}

func (p *MediaResolverImageStage) Name() ProcessorName { return ProcessorInternetImages }

func (p *MediaResolverImageStage) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *MediaResolverImageStage) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	options := internetImageOptions(plan)
	if result, handled := p.handleBypassOrUnavailable(plan, input, options); handled {
		return result, nil
	}
	if len(input.VidRushSegments) == 0 {
		return &PostProcessResult{}, nil
	}
	return p.processInternetImageSegments(ctx, plan, input, options)
}

type internetImageProcessOptions struct {
	cacheOnly           bool
	entityImagesEnabled bool
}

func (p *MediaResolverImageStage) handleBypassOrUnavailable(plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, options internetImageProcessOptions) (*PostProcessResult, bool) {
	if !plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() && !options.entityImagesEnabled {
		segments := markInternetImagesBypassed(input.VidRushSegments)
		if len(segments) == 0 {
			return &PostProcessResult{}, true
		}
		return &PostProcessResult{VidRushSegments: segments, Changed: true}, true
	}
	if !hasImageSearchSegments(plan, input) {
		return &PostProcessResult{VidRushSegments: markInternetImagesBypassed(input.VidRushSegments), Changed: true}, true
	}
	if p.searcher == nil && !options.cacheOnly {
		return &PostProcessResult{Changed: true, Warnings: []string{"internet_images: InternetImageSearcher not configured"}}, true
	}
	return nil, false
}

func markInternetImagesBypassed(input []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input))
	for _, segment := range input {
		cloned := CloneVidRushSegmentResult(segment)
		cloned.Cache.InternetImages = "BYPASSED"
		segments = append(segments, cloned)
	}
	return segments
}
