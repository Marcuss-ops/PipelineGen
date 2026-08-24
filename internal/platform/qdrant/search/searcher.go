package search

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
)

// Searcher provides semantic search over Qdrant collections.
// It routes all searches through the runtime alias and validates vectors
// against the manifest.
//
// QDRANT-ALIAS-CACHE (July 2026): the searcher caches the resolved alias
// target locally with a 30s TTL so the search hot-path does not pay an
// extra HTTP round-trip (GetAliasTarget) on every query. The cache is
// invalidated by CollectionManager.PromoteCandidate via the
// OnAliasSwitch callback wired at runtime construction.
type Searcher struct {
	client *transport.Client
	schema *schema.IndexSchema
	log    *zap.Logger

	// Alias cache: avoids an HTTP round-trip on every search query.
	// Protected by cacheMu for read/write; never held across network calls.
	// Concurrent refresh is deduplicated by sfGroup.
	// Invalidated via ResetSearchCache().
	cacheMu      sync.RWMutex
	cachedTarget string
	cachedAt     time.Time

	// sfGroup deduplicates concurrent GetAliasTarget refreshes so
	// only ONE network call is in-flight at a time per alias, even
	// under N-way concurrent cache-miss pressure. The mutex is NOT
	// held during the network call — it only guards cache read/write.
	sfGroup singleflight.Group
}

// aliasCacheTTL is how long the resolved alias target stays valid
// before requiring a fresh GetAliasTarget call. 30s balances freshness
// (blue-green alias switches are rare, operator-paced operations) against
// throughput (saves 1 HTTP round-trip per search query).
const aliasCacheTTL = 30 * time.Second

// NewSearcher creates a Searcher.
func NewSearcher(client *transport.Client, schema *schema.IndexSchema, log *zap.Logger) *Searcher {
	return &Searcher{
		client: client,
		schema: schema,
		log:    log,
	}
}

// Search performs an ANN dense vector search using the runtime alias.
func (s *Searcher) Search(ctx context.Context, req schema.SearchRequest) ([]schema.SearchResult, error) {
	if req.QueryVector == nil {
		return nil, fmt.Errorf("search vector must not be nil")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	// Validate vector dimensions against the manifest.
	if req.VectorName != "" {
		spec := s.schema.GetDense(req.VectorName)
		if spec != nil && len(req.QueryVector) != spec.Dimensions {
			return nil, &transport.ErrVectorDimensionMismatch{
				Channel:  req.VectorName,
				Expected: spec.Dimensions,
				Actual:   len(req.QueryVector),
				AssetID:  "(query)",
			}
		}
	}

	start := time.Now()
	collection, err := s.resolveCollection(ctx)
	if err != nil {
		return nil, err
	}

	results, err := s.client.SearchPoints(ctx, collection, req)
	observability.QdrantSearchLatency.WithLabelValues(req.VectorName).Observe(time.Since(start).Seconds())
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	return results, nil
}

// HybridSearch performs a hybrid dense + sparse search via the Qdrant
// Query API with RRF fusion.
//
// Sparse source acceptance (PR2, June 2026): the Searcher accepts
// EITHER of two sparse payloads when SparseVectorName is set:
//
//   - schema.SparseQueryVector (legacy, pre-PR2) — a pre-tokenised
//     {indices, values} vector produced by a client-side BM25
//     encoder. Reserved for diagnostic and bulk-from-csv callers.
//   - SparseText (PR2 server-side) — a raw query string that Qdrant
//     1.18+ runs BM25 inference on server-side. This is the live
//     orchestration path.
//
// The Searcher accepts both so the orchestration layer can choose
// the path without bearing translation responsibility.
//
// QDRANT-004 PR1 (June 2026): fail-closed contract. A hybrid request
// MUST supply at least one sparse payload AND a non-empty
// SparseVectorName. The orchestrator is expected to enforce this
// upstream (ErrHybridRequiresSparse at the application layer); the
// Searcher enforces it AGAIN here as defence-in-depth so a future
// caller cannot accidentally send a malformed hybrid request and
// receive a silently-degraded ANN result. ANN is a separate mode
// (schema.SearchRequest); use Search for explicit dense-only retrieval.
//
// Errors:
//   - transport.ErrSparseRequired (deepest guard): the caller asked for
//     hybrid without a sparse channel name OR without ANY sparse payload.
//     Maps to HTTP 422 from the handler.
//   - transport.ErrVectorDimensionMismatch: dense channel dimension mismatch
//     with the schema.IndexSchema.
//   - "dense vector must not be nil": the dense payload was absent.
//
// Validation order is dense-nil → dense-dim-mismatch → sparse-name-empty →
// sparse-source-empty so a wrong-dim dense payload fails with the precise
// typed error instead of being masked by a sparse-source complaint.
func (s *Searcher) HybridSearch(ctx context.Context, req schema.HybridSearchRequest) ([]schema.SearchResult, error) {
	if req.DenseVector == nil {
		return nil, fmt.Errorf("dense vector must not be nil for hybrid search")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	// Validate dense vector dimensions FIRST so a wrong-dim dense
	// request fails with the precise typed error rather than
	// masking it behind a sparse-required error. The dense channel
	// is the primary artefact: if its dimensions are wrong it
	// cannot produce any meaningful hybrid rank, so callers can
	// debug the dense shape without first having to attach a
	// sparse source to disambiguate the failure.
	spec := s.schema.GetDense(req.DenseVectorName)
	if spec != nil && len(req.DenseVector) != spec.Dimensions {
		return nil, &transport.ErrVectorDimensionMismatch{
			Channel:  req.DenseVectorName,
			Expected: spec.Dimensions,
			Actual:   len(req.DenseVector),
			AssetID:  "(query)",
		}
	}

	// Fail-closed for hybrid: reject before paying for any Qdrant
	// call. Accept EITHER the legacy schema.SparseQueryVector OR the
	// PR2 server-side SparseText (which lets Qdrant 1.18+ run BM25
	// inference). Both are recognised here so the orchestration
	// layer can choose which path to drive without changing the
	// Searcher's fail-closed contract.
	if strings.TrimSpace(req.SparseVectorName) == "" {
		return nil, &transport.ErrSparseRequired{Channel: "(empty)"}
	}
	if req.SparseQueryVector == nil && req.SparseText == "" {
		return nil, &transport.ErrSparseRequired{Channel: req.SparseVectorName}
	}

	start := time.Now()
	collection, err := s.resolveCollection(ctx)
	if err != nil {
		return nil, err
	}

	results, err := s.client.HybridSearchPoints(ctx, collection, req)
	observability.QdrantSearchLatency.WithLabelValues(req.DenseVectorName).Observe(time.Since(start).Seconds())
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	return results, nil
}

// SearchByText creates an embedding from text and performs an ANN search.
// The embedder is injected at construction time so the Searcher doesn't
// need to know about specific models.
func (s *Searcher) SearchByText(ctx context.Context, text string, embedder TextEmbedder, vectorName string, limit int, minScore float64) ([]schema.SearchResult, error) {
	vec, err := embedder.Embed(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("embed query text: %w", err)
	}
	return s.Search(ctx, schema.SearchRequest{
		QueryVector: vec,
		VectorName:  vectorName,
		Limit:       limit,
		MinScore:    minScore,
	})
}

// resolveCollection returns the physical collection name for the runtime
// alias, using a local cache with a 30s TTL to avoid paying an HTTP
// round-trip on every search query.
//
// QDRANT-ALIAS-CACHE (July 2026): the hot-path read uses a RWMutex so
// concurrent searches don't contend. Only a cache miss (first call or
// TTL expired) acquires the write lock and issues GetAliasTarget.
// Invalidation is triggered by ResetSearchCache(), called by
// CollectionManager.PromoteCandidate after every alias switch.
//
// ── PR 5 asymmetry: why the read path resolves the alias but the write path doesn't ──
//
// The Searcher (read path) calls GetAliasTarget to resolve
// s.schema.RuntimeAlias → physical collection name, caches it for 30s,
// and then routes all Search/HybridSearch queries to the resolved
// target. The IndexWriter (write path) does NOT resolve — it passes
// s.schema.RuntimeAlias directly to UpsertPoints / DeletePoints.
//
// This asymmetry is intentional (PR 5, June 2026, fix/qdrant-tenant-scope)
// and must NOT be "corrected" by a future maintainer. Here's why:
//
// READ PATH (resolve, then query):
//   - Qdrant accepts aliases in all REST API paths (including
//     /points/search), so resolving before searching is an optimisation
//     and explicit-validation choice, not an API requirement.
//   - GetAliasTarget serves as a liveness check: if the runtime alias has
//     no target, we fail early with a clear error rather than forwarding
//     an opaque 404 from Qdrant's search handler.
//   - The 30s TTL cache eliminates the per-request alias-resolution cost
//     on the hot path (saving one Qdrant-internal lookup per query).
//   - A stale cache (up to 30s) is safe: after a blue-green alias switch,
//     searches briefly hitting the old collection return correct results
//     (the old collection still exists and is consistent). No data
//     corruption, no write-tearing.
//
// WRITE PATH (write directly through alias):
//   - Qdrant accepts the alias name in PUT /points and POST /points/delete
//     requests natively. The write is server-side atomic: Qdrant resolves
//     the alias at request time within a single operation — no TOCTOU
//     window between a client-side GetAliasTarget and the subsequent write.
//   - The PRE-PR5 approach (GetAliasTarget → write to physical name)
//     opened a race: if an alias switch landed between resolution and
//     write, the batch would be written to the collection the alias
//     JUST MOVED AWAY FROM — silently losing the write from the active
//     index. The new collection would never see those points.
//   - The write path saves one HTTP round-trip per batch (deletes and
//     upserts are already batched, so this is a latency saving, not a
//     correctness concern).
//
// TRADE-OFF SUMMARY:
//   - Read path: extra round-trip on cache miss, safe staleness up to 30s.
//   - Write path: zero extra round-trips, atomic alias resolution, no
//     blue-green race window.
//   - The asymmetry is a feature, not a bug. Resolving aliases on the
//     write path was explicitly removed in PR 5 because correctness
//     (atomic writes) trumps symmetry.
//
// See internal/platform/qdrant/index_writer.go PR 5 comment block
// for the write-side counterpart of this documentation.
//
// ── Concurrency contract (HIGH #9, July 2026) ──
//
// cacheMu is ONLY held for cache reads (RLock) and cache writes (Lock).
// It is NEVER held across the GetAliasTarget network call.
//
// Concurrent refresh is deduplicated by sfGroup (singleflight.Group):
// when N goroutines simultaneously hit a cache miss, only ONE makes
// the GetAliasTarget call. The other N-1 wait for that result via
// singleflight's internal dedup and then double-check the cache.
// This eliminates the thundering-herd problem where N concurrent
// cache-miss callers each acquire cacheMu.Lock() sequentially and
// each make their own network call.
func (s *Searcher) resolveCollection(ctx context.Context) (string, error) {
	// Fast path: read-lock, check cache.
	s.cacheMu.RLock()
	if s.cachedTarget != "" && time.Since(s.cachedAt) < aliasCacheTTL {
		observability.QdrantAliasCacheHitTotal.Inc()
		target := s.cachedTarget
		s.cacheMu.RUnlock()
		return target, nil
	}
	s.cacheMu.RUnlock()

	// Slow path: singleflight-deduplicated refresh.
	// Only ONE goroutine per alias actually calls GetAliasTarget;
	// the rest get the result from the singleflight group.
	result, err, _ := s.sfGroup.Do(s.schema.RuntimeAlias, func() (any, error) {
		// Double-check: another goroutine may have populated the
		// cache between our RUnlock and the singleflight callback
		// execution (e.g. a prior singleflight call completed).
		s.cacheMu.RLock()
		if s.cachedTarget != "" && time.Since(s.cachedAt) < aliasCacheTTL {
			target := s.cachedTarget
			s.cacheMu.RUnlock()
			return target, nil
		}
		s.cacheMu.RUnlock()

		// Cache miss: execute the network call WITHOUT holding cacheMu.
		observability.QdrantAliasCacheMissTotal.Inc()
		collection, callErr := s.client.GetAliasTarget(ctx, s.schema.RuntimeAlias)
		if callErr != nil {
			return "", fmt.Errorf("resolve alias target: %w", callErr)
		}
		if collection == "" {
			return "", fmt.Errorf("runtime alias %q has no target — run EnsureSchema first", s.schema.RuntimeAlias)
		}

		// Populate cache — mutex ONLY for the cache write, not the network call.
		s.cacheMu.Lock()
		s.cachedTarget = collection
		s.cachedAt = time.Now()
		s.cacheMu.Unlock()

		return collection, nil
	})

	if err != nil {
		return "", err
	}
	return result.(string), nil
}

// ResetSearchCache invalidates the alias-target cache. Called by
// CollectionManager.PromoteCandidate after every alias switch so the
// next search query picks up the new physical collection immediately.
func (s *Searcher) ResetSearchCache() {
	s.cacheMu.Lock()
	s.cachedTarget = ""
	s.cachedAt = time.Time{}
	s.cacheMu.Unlock()
}

// ── Embedding contract ───────────────────────────────────────────────

// TextEmbedder is the narrow interface for text embedding.
// Implementations include HTTPTextEmbedder, OllamaClient, etc.
type TextEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// ImageEmbedder generates visual embeddings from image data.
type ImageEmbedder interface {
	EmbedImages(ctx context.Context, imagePaths []string) ([][]float32, error)
}

// AudioEmbedder generates audio embeddings from audio data.
//
// YAGNI (July 2026): CLAP-HTSAT audio embedding model is not available
// in production. The adapter (embedders.go audioEmbedderAdapter) and
// the clipindexer.indexAudioViaAPI path exist but the runtime service
// is not deployed. Uncomment when audio embedding is wired.
// type AudioEmbedder interface {
// 	EmbedAudio(ctx context.Context, audioPaths []string) ([][]float32, error)
// }
