// Package stockintelligence — adapters_postgres.go is the POST-CUTOVER
// retrieval plane for the durable stock resolver. After the media SSOT
// moved to PostgreSQL+pgvector, the resolver must no longer call
// Qdrant (projection) or SQLite (second media truth). The single read
// adapter is pgmedia.MediaSearcher (pgvector HNSW 768d) hydrated from
// media_assets — no second database, no projection drift.
//
// Contracts:
//   - PostgresLocalSearchAdapter implements LocalSearchPort (vector ANN).
//   - PostgresAssetHydrator implements AssetHydratorPort (label truth).
//   - Both own the same single *MediaSearcher instance when wired from
//     the composition root; that is the acceptance invariant proven by
//     certify-media-cutover's QDRANT_MEDIA_*=0 + SQLITE_MEDIA_READERS=0.
package stockintelligence

import (
	"context"
	"fmt"
	"strings"

	appsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
)

// pgVectorStore is the narrow surface stockintelligence needs from the
// pgvector adapter (subset of appsearch.VectorStorePort). The concrete
// type is *pgmedia.MediaSearcher; the interface keeps this package free
// of a hard import on the postgres media package so unit tests can seal
// it with a fake.
type pgVectorStore interface {
	Search(ctx context.Context, req appsearch.VectorSearchRequest) ([]appsearch.VectorSearchResult, error)
}

// pgMediaHydrator is the narrow media_assets hydration surface
// (subset of appsearch.MediaReadRepository) required to turn ANN hits
// into labelled Candidates without touching SQLite.
type pgMediaHydrator interface {
	GetMany(ctx context.Context, actor appsearch.Actor, assetIDs []string) ([]appsearch.MediaAsset, error)
}

// PostgresLocalSearchAdapter is the post-cutover LocalSearchPort.
// It embeds via the E5 sidecar (intfloat/multilingual-e5-base 768d,
// pgvector HNSW) and searches the pgvector SSOT in one query that
// enforces hard filters (workspace/lifecycle/searchable) AND ANN
// ordering via the per-family HNSW partial index (003). The concrete
// embedder is typed as E5Embedder to keep the contract explicit.
type PostgresLocalSearchAdapter struct {
	Searcher pgVectorStore
	Embedder E5Embedder
}

// E5Embedder is the typed embedder contract for the PG plane.
// The canonical implementation is *embeddings.HTTPTextEmbedder.
type E5Embedder interface {
	Embed(ctx context.Context, text string) (E5EmbeddingResult, error)
}

// E5EmbeddingResult mirrors the canonical asset.EmbeddingResult shape
// without importing the asset kernel — dimension/model coupling stays
// in one place.
type E5EmbeddingResult struct {
	Vector []float32
}

// httpTextEmbedderAdapter adapts the canonical asset.Embedder-shaped
// HTTPTextEmbedder into the local E5Embedder so this package does not
// need to import the embeddings platform package in production wiring;
// callers that already have a typed embeddings.HTTPTextEmbedder can
// construct a tiny adapter inline. The factory below handles it.
type httpTextEmbedderAdapter struct {
	embed func(ctx context.Context, text string) ([]float32, error)
}

func (a *httpTextEmbedderAdapter) Embed(ctx context.Context, text string) (E5EmbeddingResult, error) {
	if a == nil || a.embed == nil {
		return E5EmbeddingResult{}, fmt.Errorf("stockintelligence: local E5 embedder not configured")
	}
	vec, err := a.embed(ctx, text)
	if err != nil {
		return E5EmbeddingResult{}, err
	}
	return E5EmbeddingResult{Vector: vec}, nil
}

// NewHTTPTextEmbedderAdapter adapts a raw embed func into E5Embedder.
func NewHTTPTextEmbedderAdapter(embed func(context.Context, string) ([]float32, error)) E5Embedder {
	return &httpTextEmbedderAdapter{embed: embed}
}

// SearchLocal implements LocalSearchPort against the PostgreSQL SSOT.
// Query embedding is produced by the E5 sidecar, then the pgvector
// MediaSearcher performs ANN + hard-filter fusion in one SQL query.
// Caller-visible contract matches QdrantLocalSearchAdapter: limit
// default 20, caller thresholds (10 / 0.6) stay in the resolver.
func (a PostgresLocalSearchAdapter) SearchLocal(ctx context.Context, query string, limit int) ([]Candidate, error) {
	if a.Searcher == nil || a.Embedder == nil {
		return nil, fmt.Errorf("stockintelligence: postgres local search not configured")
	}
	if strings.TrimSpace(query) == "" {
		return []Candidate{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	emb, err := a.Embedder.Embed(ctx, strings.TrimSpace(query))
	if err != nil {
		return nil, fmt.Errorf("stockintelligence: embed query: %w", err)
	}
	if len(emb.Vector) == 0 {
		return nil, fmt.Errorf("stockintelligence: empty query embedding")
	}
	if len(emb.Vector) != models.E5.Dimensions {
		return nil, fmt.Errorf("stockintelligence: query vector dim %d does not match canonical E5 dim %d", len(emb.Vector), models.E5.Dimensions)
	}
	req := appsearch.VectorSearchRequest{
		QueryVector: emb.Vector,
		VectorName:  appsearch.ChannelText,
		Limit:       limit,
		IsSystem:    true,
	}
	hits, err := a.Searcher.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(hits))
	for _, hit := range hits {
		out = append(out, Candidate{
			AssetID:           hit.AssetID,
			GenericSimilarity: float32(hit.Score),
			Source:            "local",
		})
	}
	return out, nil
}

// Compile-time port assertions
var _ LocalSearchPort = (*PostgresLocalSearchAdapter)(nil)
var _ LocalSearchPort = PostgresLocalSearchAdapter{}

// PostgresAssetHydrator adapts the canonical media_assets SSOT
// (PostgresMediaSearcher.GetMany) to the local resolver's label needs.
// Qdrant supplies IDs/scores; PostgreSQL supplies the label truth.
// Orphan hits (no media_assets row) are dropped exactly as in the
// SQLite path; the resolver treats them as non-candidates.
type PostgresAssetHydrator struct {
	Searcher pgMediaHydrator
}

// Hydrate implements AssetHydratorPort on PostgreSQL. The label is
// search_text || name || filename (media_assets SSOT). Using the
// canonical MediaReadRepository keeps the hydration filter
// (SearchableLifecycleStates) in one place — the same filter the
// vector search itself enforces via the ANN query's hard-filter leg.
func (a PostgresAssetHydrator) Hydrate(ctx context.Context, ids []string) (map[string]string, error) {
	if a.Searcher == nil {
		return nil, fmt.Errorf("stockintelligence: postgres asset hydrator not configured")
	}
	clean := dedupeIDs(ids)
	if len(clean) == 0 {
		return map[string]string{}, nil
	}
	rows, err := a.Searcher.GetMany(ctx, appsearch.Actor{IsSystem: true}, clean)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]appsearch.MediaAsset, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make(map[string]string, len(clean))
	for _, id := range clean {
		row, ok := byID[id]
		if !ok {
			continue
		}
		label := strings.TrimSpace(row.SearchText)
		if label == "" {
			label = strings.TrimSpace(row.Name)
		}
		if label != "" {
			out[id] = label
		}
	}
	return out, nil
}

func dedupeIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

var _ AssetHydratorPort = (*PostgresAssetHydrator)(nil)
var _ AssetHydratorPort = PostgresAssetHydrator{}
