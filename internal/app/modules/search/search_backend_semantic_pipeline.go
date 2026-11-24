package search

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

// semanticQueryPlan freezes everything Search needs after the embedding:
// workspace/filters, the two score floors, the requested limit and the wider
// rerank fetch window. Keeping it in one value prevents the ANN and hybrid
// branches from drifting on floor or limit resolution.
type semanticQueryPlan struct {
	vector         []float32
	scope          search.SearchScope
	filter         search.AssetFilter
	fetchLimit     int
	annMinScore    float64
	hybridMinScore float64
}

// embedSearchQuery encodes the text channel and measures the embedder leg.
// A zero-length vector is not an error: the caller treats it as "nothing to
// retrieve" and lets the aggregator fanout continue.
func (b *semanticSearchBackend) embedSearchQuery(ctx context.Context, q search.Query) ([]float32, int64, error) {
	started := time.Now()
	vec, err := b.embeddings.EmbedQuery(ctx, search.ChannelText, q.Text)
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		return nil, elapsed, fmt.Errorf("semantic backend: embed channel %q: %w", search.ChannelText, err)
	}
	if len(vec) == 0 {
		b.warn("semantic backend: embedder returned zero-length vector",
			zap.String("channel", search.ChannelText),
			zap.String("text", q.Text))
	}
	return vec, elapsed, nil
}

// buildSemanticQueryPlan resolves filters, score floors, the page limit and the
// rerank fetch window. q.MinScore overrides BOTH floors so per-request tuning
// stays uniform across ANN and hybrid retrieval.
func buildSemanticQueryPlan(b *semanticSearchBackend, q search.Query, vec []float32) semanticQueryPlan {
	scope, filter := compileSemanticFilters(q)

	annMinScore := semanticMinScore
	hybridMinScore := semanticHybridMinScore
	if q.MinScore > 0 {
		annMinScore = q.MinScore
		hybridMinScore = q.MinScore
	}

	limit := q.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}
	if limit > search.MaxLimit {
		limit = search.MaxLimit
	}

	fetchLimit := limit
	if b.reranker != nil && b.reranker.IsEnabled() {
		if w := b.reranker.TopK(); w > fetchLimit {
			fetchLimit = w
		}
		if fetchLimit > search.MaxLimit {
			fetchLimit = search.MaxLimit
		}
	}

	return semanticQueryPlan{
		vector:         vec,
		scope:          scope,
		filter:         filter,
		fetchLimit:     fetchLimit,
		annMinScore:    annMinScore,
		hybridMinScore: hybridMinScore,
	}
}

// executeSemanticVectorSearch runs the ANN or hybrid leg and measures the
// pgvector leg so latency logs can separate embedder from storage.
func (b *semanticSearchBackend) executeSemanticVectorSearch(ctx context.Context, q search.Query, plan semanticQueryPlan) ([]search.VectorSearchResult, int64, error) {
	started := time.Now()
	var (
		results []search.VectorSearchResult
		err     error
	)
	switch q.Mode {
	case search.RetrievalModeHybrid:
		// TranscriptVector is intentionally absent: passing the same dense
		// vector would silently inflate pgvector RRF fusion.
		results, err = b.vectorStore.HybridSearch(ctx, search.HybridSearchRequest{
			DenseVector:      plan.vector,
			DenseVectorName:  semanticDenseVectorName,
			SparseText:       q.Text,
			SparseVectorName: semanticSparseVectorName,
			Limit:            plan.fetchLimit,
			MinScore:         plan.hybridMinScore,
			Source:           plan.filter.Source,
			Category:         plan.filter.Category,
			MediaType:        plan.filter.MediaType,
			Language:         plan.filter.Language,
			AssetKind:        plan.filter.AssetKind,
			SemanticRole:     plan.filter.SemanticRole,
			LifecycleState:   plan.filter.LifecycleState,
			WorkspaceID:      plan.scope.WorkspaceID,
			IsSystem:         plan.scope.IsSystem,
		})
	default: // SearchModeANN (or empty string → ANN)
		results, err = b.vectorStore.Search(ctx, search.VectorSearchRequest{
			QueryVector:    plan.vector,
			VectorName:     semanticDenseVectorName,
			Limit:          plan.fetchLimit,
			MinScore:       plan.annMinScore,
			Source:         plan.filter.Source,
			Category:       plan.filter.Category,
			MediaType:      plan.filter.MediaType,
			Language:       plan.filter.Language,
			AssetKind:      plan.filter.AssetKind,
			SemanticRole:   plan.filter.SemanticRole,
			LifecycleState: plan.filter.LifecycleState,
			WorkspaceID:    plan.scope.WorkspaceID,
			IsSystem:       plan.scope.IsSystem,
		})
	}
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		return nil, elapsed, fmt.Errorf("semantic backend: vector search: %w", err)
	}
	return results, elapsed, nil
}

// logSemanticSearchLatency keeps the STK-09 split (embed vs pgvector vs total)
// observable without threading timing values through every phase.
func (b *semanticSearchBackend) logSemanticSearchLatency(q search.Query, embedMS, vectorMS int64, started time.Time, hits, fetchLimit int) {
	if b.log == nil {
		return
	}
	b.log.Info("semantic search latency",
		zap.String("mode", string(q.Mode)),
		zap.Int64("embed_ms", embedMS),
		zap.Int64("pgvector_ms", vectorMS),
		zap.Int64("total_ms", time.Since(started).Milliseconds()),
		zap.Int("vector_hits", hits),
		zap.Int("fetch_limit", fetchLimit))
}

// dedupeSemanticVectorResults keeps the highest score per asset. pgvector RRF
// fusion can return the same point through multiple vector channels.
func dedupeSemanticVectorResults(results []search.VectorSearchResult) ([]string, map[string]float64) {
	assetIDs := make([]string, 0, len(results))
	scoreByID := make(map[string]float64, len(results))
	for _, r := range results {
		if r.AssetID == "" {
			continue
		}
		if existing, ok := scoreByID[r.AssetID]; ok {
			if r.Score > existing {
				scoreByID[r.AssetID] = r.Score
			}
			continue
		}
		assetIDs = append(assetIDs, r.AssetID)
		scoreByID[r.AssetID] = r.Score
	}
	return assetIDs, scoreByID
}

// hydrateSemanticCandidates loads the canonical catalog rows, applies the
// post-hydration filters (lifecycle, tags AND-semantics, duration floor) and
// builds the signed delivery URL. It returns the candidates plus the asset map
// the reranker needs for its text projection.
func (b *semanticSearchBackend) hydrateSemanticCandidates(
	ctx context.Context,
	q search.Query,
	scope search.SearchScope,
	assetIDs []string,
	scoreByID map[string]float64,
) ([]search.Candidate, map[string]search.MediaAsset, error) {
	ws := search.Actor{WorkspaceID: scope.WorkspaceID}
	assets, err := b.mediaReader.GetMany(ctx, ws, assetIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("semantic backend: hydrate: %w", err)
	}

	candidates := make([]search.Candidate, 0, len(assets))
	assetsByID := make(map[string]search.MediaAsset, len(assets))
	for _, a := range assets {
		// Defense in depth against replication lag or a non-canonical reader:
		// only searchable lifecycle states may reach the API response.
		if !isSearchableLifecycleState(a.LifecycleState) {
			continue
		}
		if !matchesAllTags(a.Tags, q.Filters.Tags) {
			continue
		}
		if q.Filters.DurationMsMin > 0 && a.DurationMs < q.Filters.DurationMsMin {
			continue
		}
		assetsByID[a.ID] = a

		// Admin users access files directly and need no signed URL.
		var url string
		if !q.Actor.IsAdmin {
			var urlErr error
			url, urlErr = b.delivery.BuildAuthorizedURL(ctx, ws, a.ID)
			if urlErr != nil {
				b.warn("semantic backend: failed to build delivery URL",
					zap.String("asset_id", a.ID),
					zap.Error(urlErr))
				continue
			}
		}

		candidates = append(candidates, search.Candidate{
			AssetID: a.ID,
			// Source carries physical provenance, not the backend name.
			Source:       semanticSourceProvenance(a.Source),
			SourceRef:    a.ID,
			AssetKind:    a.AssetKind,
			SemanticRole: a.SemanticRole,
			MediaType:    a.MediaType,
			Title:        semanticDisplayTitle(a.Title, a.Name),
			Name:         a.Name,
			// Raw Drive links stay out of public candidates; PreviewURL is the
			// signed delivery surface and the only client-facing locator.
			PreviewURL: url,
			Score:      scoreByID[a.ID],
		})
	}
	return candidates, assetsByID, nil
}
