package adapters

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/cacheutil"
	"go.uber.org/zap"
)

func loadVidRushPersistentJSON(ctx context.Context, cache scriptports.VidRushCachePort, namespace, key string, dst any) (bool, error) {
	if cache == nil {
		return false, nil
	}
	raw, hit, err := cache.Get(ctx, namespace, key)
	if err != nil || !hit {
		return false, err
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, fmt.Errorf("vidrush cache %s/%s: decode: %w", namespace, key, err)
	}
	return true, nil
}

func storeVidRushPersistentJSON(ctx context.Context, cache scriptports.VidRushCachePort, namespace, key string, value any) error {
	if cache == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("vidrush cache %s/%s: encode: %w", namespace, key, err)
	}
	if err := scriptports.ValidateVidRushCachePayload(raw); err != nil {
		return fmt.Errorf("vidrush cache %s/%s: validate: %w", namespace, key, err)
	}
	return cache.Put(ctx, namespace, key, raw)
}

// sanitizeVidRushDiscoveryCandidates strips durable materialization state from
// provider discovery cache payloads. Discovery may remember URLs, provenance
// and scores; it must never persist a Drive link, content address, local path
// or lifecycle state. Those facts belong to the entity catalog/materialization
// and can be invalidated independently. Reusing them from a discovery cache
// bypasses catalog invalidation and can submit a stale content address to
// RenderingGen, where prefetch fails with an asset content address mismatch.
func sanitizeVidRushDiscoveryCandidates(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	if len(candidates) == 0 {
		return candidates
	}
	out := make([]scriptpkg.SegmentAssetCandidate, len(candidates))
	for i, candidate := range candidates {
		candidate.DriveLink = ""
		candidate.ContentSHA256 = ""
		candidate.LocalPath = ""
		candidate.AcquisitionStatus = ""
		candidate.VerificationStatus = ""
		candidate.PersistenceStatus = ""
		candidate.IndexStatus = ""
		out[i] = candidate
	}
	return out
}

func sanitizeInternetImageCachePayload(payload internetImageCachePayload) internetImageCachePayload {
	payload.Candidates = sanitizeVidRushDiscoveryCandidates(payload.Candidates)
	return payload
}

// ── L1 caches (bounded LRU) ────────────────────────────────────────────
//
// The VidRush L1 caches are no-TTL by contract (a warm replay of the same
// query must hit without re-calling the provider), so they must be BOUNDED
// rather than swept. They are canonical pkg/cacheutil.LRU instances:
// capacity is fixed at construction, eviction is amortized O(1), and there
// is NO background goroutine to leak or to destroy every warm entry at once.
//
// This replaces the historical unbounded sync.Map + 10-minute full-sweep
// janitor (the canonical anti-pattern already retired in cliprender). The
// durable L2 cache (VidRushCachePort, TTL 48h) re-warms L1 on the next
// replay with identical HIT_EXACT semantics, so bounding memory never
// changes results.
const (
	vidrushImageL1Capacity        = 4096
	vidrushBindingL1Capacity      = 4096
	vidrushMaterializedL1Capacity = 1024
)

var (
	vidrushImageCache        = cacheutil.NewLRU(vidrushImageL1Capacity)
	vidrushBindingCache      = cacheutil.NewLRU(vidrushBindingL1Capacity)
	vidrushMaterializedCache = cacheutil.NewLRU(vidrushMaterializedL1Capacity)

	vidrushCacheHits   atomic.Int64
	vidrushCacheMisses atomic.Int64
	vidrushCacheStores atomic.Int64
)

// VidRushCacheStats is the bounded-cache observability snapshot. The caches
// themselves are fixed-capacity LRUs; eviction is implicit when Put exceeds
// capacity, so stores are the forward signal for that pressure.
type VidRushCacheStats struct {
	Hits   int64 `json:"hits"`
	Misses int64 `json:"misses"`
	Stores int64 `json:"stores"`
}

func VidRushCacheMetrics() VidRushCacheStats {
	return VidRushCacheStats{
		Hits: vidrushCacheHits.Load(), Misses: vidrushCacheMisses.Load(), Stores: vidrushCacheStores.Load(),
	}
}

// weightedKeywordValues projects a profile's weighted keyword stream onto the
// legacy string surface consumed by SegmentInsights and the ad-hoc query
// builders. It is the only legal way to read Keywords/VisualTerms back as a
// plain list; values keep the profile's deterministic order.

func segmentCacheKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func cacheLoad(cache *cacheutil.LRU, key string) (any, bool) {
	if cache == nil || key == "" {
		return nil, false
	}
	value, ok := cache.Get(key)
	if ok {
		vidrushCacheHits.Add(1)
	} else {
		vidrushCacheMisses.Add(1)
	}
	return value, ok
}

func cacheStore(cache *cacheutil.LRU, key string, value any) {
	if cache == nil || key == "" {
		return
	}
	vidrushCacheStores.Add(1)
	cache.Put(key, value)
}

type vidRushProviderOutcome struct {
	provider     string
	candidates   []scriptpkg.SegmentAssetCandidate
	allCacheHits bool
	err          error
}

type vidRushFanoutPlan struct {
	segmentID          string
	textHash           string
	text               string
	title              string
	imageQueries       []string
	firstEntity        string
	youtubeSources     []scriptports.VidRushSourceHint
	perQueryLimit      int
	imagesEnabled      bool
	youtubeEnabled     bool
	parallelQueryChild bool
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
	// manifestPins is the opt-in pinned-manifest fast path (see
	// media_manifest_shim.go). Nil keeps every provider call unchanged.
	manifestPins *MediaManifestPins
	// contentStore resolves a catalog candidate's local path from its content
	// address so a warm discovery replay names the local CAS copy.
	contentStore EntityImageContentStore
}

// WithCASStore installs the content-addressed store read side. Optional: nil
// keeps the catalog's Drive-only reuse.
func (f *VidRushProviderFanout) WithCASStore(store EntityImageContentStore) *VidRushProviderFanout {
	if f != nil {
		f.contentStore = store
	}
	return f
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
