package adapters

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

type vidRushProviderOutcome struct {
	provider     string
	candidates   []scriptpkg.SegmentAssetCandidate
	allCacheHits bool
	err          error
}

type vidRushFanoutPlan struct {
	segmentID      string
	textHash       string
	text           string
	title          string
	imageQueries   []string
	firstEntity    string
	youtubeSources []scriptports.VidRushSourceHint
	perQueryLimit  int
	imagesEnabled  bool
	youtubeEnabled bool
}

// VidRushProviderFanout construction (merged from
// vidrush_registry_searchers.go 2026-10-13): the fanout struct and its
// constructors live with the fanout types to keep the searchers file under
// max_lines_per_file_strict=600. No behaviour change.

type VidRushProviderFanout struct {
	images         InternetImageSearcher
	semanticScorer SemanticCandidateScorer
	youtube        scriptports.VidRushAssetProvider
	cache          scriptports.VidRushCachePort
	catalog        entitycatalog.Repository
	metrics        VidRushMetrics
	log            zap.Logger
}

// WithSemanticScorer applies the same pixel-level person-image gate to
// incremental discovery, including segment and catalog cache hits. Without
// this, candidates discovered by this fanout could reach materialization
// without passing the image-stage verifier.
func (f *VidRushProviderFanout) WithSemanticScorer(scorer SemanticCandidateScorer) *VidRushProviderFanout {
	if f != nil {
		f.semanticScorer = scorer
	}
	return f
}

func (f *VidRushProviderFanout) WithLogger(log *zap.Logger) *VidRushProviderFanout {
	if f != nil && log != nil {
		f.log = *log
	}
	return f
}

func NewVidRushProviderFanout(images InternetImageSearcher, metrics ...VidRushMetrics) *VidRushProviderFanout {
	return NewVidRushProviderFanoutWithCatalog(images, nil, nil, metrics...)
}

// NewVidRushProviderFanoutWithYouTube adds the canonical YouTube provider to
// the existing fan-out while preserving the legacy constructor.
func NewVidRushProviderFanoutWithYouTube(images InternetImageSearcher, youtube scriptports.VidRushAssetProvider, metrics ...VidRushMetrics) *VidRushProviderFanout {
	fanout := NewVidRushProviderFanout(images, metrics...)
	fanout.youtube = youtube
	return fanout
}

func NewVidRushProviderFanoutWithCache(images InternetImageSearcher, cache scriptports.VidRushCachePort, metrics ...VidRushMetrics) *VidRushProviderFanout {
	return NewVidRushProviderFanoutWithCatalog(images, cache, nil, metrics...)
}

func NewVidRushProviderFanoutWithCatalog(images InternetImageSearcher, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushMetrics) *VidRushProviderFanout {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &VidRushProviderFanout{images: images, cache: cache, catalog: catalog, metrics: m}
}

// MediaResolver is the unified provider discovery boundary used by the
// production fan-out. A resolver may expose several provider queries, but it
// never owns acquisition or winner selection.
type MediaResolver interface {
	InternetImageSearcher
}

func NewVidRushProviderFanoutWithResolver(resolver MediaResolver, cache scriptports.VidRushCachePort, catalog entitycatalog.Repository, metrics ...VidRushMetrics) *VidRushProviderFanout {
	return NewVidRushProviderFanoutWithCatalog(resolver, cache, catalog, metrics...)
}
