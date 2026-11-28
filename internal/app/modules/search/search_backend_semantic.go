// Package search — search_backend_semantic.go is the Fase 6 (July 2026)
// semantic SearchBackend that connects the canonical search.Aggregator
// to pgvector for real ANN and Hybrid retrieval. It replaces the
// historical PR-SEARCH-LEGACY-MEDIASEARCH-BACKEND-REMOVAL stub
// (which wrapped *mediasearch.Service in a single-port shape) with
// the canonical two-port architecture:
//
//	EmbeddingChannelRegistry (multi-channel embedding)
//	VectorStorePort          (locator-free ANN/hybrid retrieval)
//
// Plus PostgreSQL hydration (MediaReadRepository) and signed delivery
// URLs (AssetDeliveryService). Per AGENTS.md Pattern 0, every
// external dependency flows through a typed port so tests can swap
// in stubs without touching pgvector or SQLite.
//
// PR-EMBEDDING-CHANNEL-REGISTRY (July 2026): the backend now
// consumes the multi-channel EmbeddingChannelRegistry port
// (internal/capabilities/assets/search/ports.go) instead of the historical
// single-text QueryEmbedder. Today's wiring exercises only the
// ChannelText path (768d multilingual-e5-base); the registry
// architecture guarantees the BACKEND doesn't change when new
// channel encoders land (e.g. SigLIP-text for PR-CROSS-MODAL-
// TEXT-TO-VISUAL, deadline 2026-08-15) — composition root adds
// the encoder to registry.adapters and the backend fans out via
// EmbedQuery(channel, text) without touching this file.
//
// Wave 19 cross-capability rule: this file IS in internal/app/
// (composition root) and imports from multiple application/*
// packages. That is the canonical bridge pattern per
// search_backends.go's preamble — the only place where multiple
// internal/capabilities/* domains are imported at once.
package search

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
)

// Canonical vector names for the semantic backend. Mirrors the
// pgvector IndexSchema (see AGENTS.md §pgvector Entity Associations):
//
//	text      = 768-dim intfloat/multilingual-e5-base (semantic meaning)
//	bm25_text = sparse BM25 (lexical exact-match)
const (
	semanticDenseVectorName  = "text"
	semanticSparseVectorName = "bm25_text"

	// semanticMinScore is the ANN (cosine-similarity) floor below which
	// pgvector hits are dropped pre-hydration. Kept deliberately low: the
	// sampler owns acceptance after hydration, and this backend must not
	// compensate for incompatible embedding spaces with a score hack.
	semanticMinScore = 0.01

	// RRF scores are rank positions, not cosine similarity. A fixed floor
	// discarded every hit in some narrow source/media-type searches, even
	// when ANN found valid assets. Let the ranking order decide; callers can
	// still apply an explicit min_score.
	semanticHybridMinScore float64 = 0

	// semanticRerankTopResults is the final precision limit of the
	// enabled reranker recipe (pgvector top_k window → BGE re-score →
	// top 5 results). After blending, the semantic backend returns only
	// the semanticRerankTopResults most relevant assets; the aggregator
	// still trims the merged page to the requested size.
	semanticRerankTopResults = 5
)

// semanticSearchBackend is the Fase 6 + PR-EMBEDDING-CHANNEL-REGISTRY
// vector-backed SearchBackend. It fans out a search.Query through the
// canonical two-port architecture:
//
//  1. embeddings.EmbedQuery(ctx, ChannelText, q.Text) → dense vector
//     (delegated to EmbeddingChannelRegistry so new channel encoders
//     plug in at composition root without backend changes)
//  2. vectorStore.Search or .HybridSearch      → pgvector results
//  3. mediaReader.GetMany                      → PostgreSQL hydration (canonical metadata)
//  4. delivery.BuildAuthorizedURL              → signed delivery URL per hit
//
// Workspace isolation, lifecycle ACTIVE, and equality filters
// (source, category, media_type, language) are applied at the
// pgvector layer via the VectorStorePort adapter. Tags (AND
// semantics) and DurationMsMin are enforced post-hydration.
//
// PR-EMBEDDING-CHANNEL-REGISTRY (July 2026): Embeddings is the
// canonical multi-channel embedding port (Pattern 0); the legacy
// `embedder search.QueryEmbedder` field is gone — the registry
// owns text-channel encoding now and future cross-modal channels
// plug in here.
type semanticSearchBackend struct {
	embeddings  search.EmbeddingChannelRegistry
	vectorStore search.VectorStorePort
	mediaReader search.MediaReadRepository
	delivery    search.AssetDeliveryService
	log         *zap.Logger
	reranker    RerankerClient
}

// Compile-time assertion: semanticSearchBackend satisfies the
// search.SearchBackend contract. Future drift on the interface
// is a build failure, not a runtime panic.
var _ search.SearchBackend = (*semanticSearchBackend)(nil)

// Name returns the stable backend identifier used by
// BackendRegistry.Register and Aggregator.Eligible.
func (b *semanticSearchBackend) Name() string {
	return "semantic"
}

// Capabilities advertises every media type the semantic backend
// can return. pgvector indexes all four, so the backend is
// eligible for any Query.MediaTypes ∩ these four.
func (b *semanticSearchBackend) Capabilities() []search.Capability {
	return []search.Capability{
		search.CapVideo,
		search.CapImage,
		search.CapAudio,
		search.CapMusic,
	}
}

// Universe reports SearchCatalog: the semantic backend searches pgvector
// and hydrates from SQLite (no live provider call).
func (b *semanticSearchBackend) Universe() search.SearchUniverse {
	return search.SearchCatalog
}

// Search runs the full semantic pipeline. Hash-only queries are
// not this backend's domain (local backend owns hash matches);
// they return nil,nil without error so the Aggregator fanout
// continues.
func (b *semanticSearchBackend) Search(ctx context.Context, q search.Query) ([]search.Candidate, error) {
	// Hash-match queries belong to the local backend.
	if q.Hash != "" {
		return nil, nil
	}

	// Nothing to embed → nothing to retrieve.
	if strings.TrimSpace(q.Text) == "" {
		return nil, nil
	}

	started := time.Now()
	vec, embedMS, err := b.embedSearchQuery(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(vec) == 0 {
		return nil, nil
	}

	plan := buildSemanticQueryPlan(b, q, vec)
	results, vectorMS, err := b.executeSemanticVectorSearch(ctx, q, plan)
	if err != nil {
		return nil, err
	}
	b.logSemanticSearchLatency(q, embedMS, vectorMS, started, len(results), plan.fetchLimit)
	if len(results) == 0 {
		return nil, nil
	}

	assetIDs, scoreByID := dedupeSemanticVectorResults(results)
	if len(assetIDs) == 0 {
		return nil, nil
	}

	candidates, assetsByID, err := b.hydrateSemanticCandidates(ctx, q, plan.scope, assetIDs, scoreByID)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	candidates, err = b.rerankCandidates(ctx, q, candidates, assetsByID)
	if err != nil {
		return nil, err
	}
	return search.RankByScore(candidates), nil
}

// semanticSourceProvenance returns the catalog row's provenance for use as a
// Candidate.Source, falling back to "semantic" when the row does not carry one.
// The fallback preserves the historical label only for the genuinely unknown
// case; every row that knows its provenance now reports it.
func semanticSourceProvenance(rowSource string) string {
	if s := strings.TrimSpace(rowSource); s != "" {
		return s
	}
	return "semantic"
}

// ── Filter compilation ────────────────────────────────────────────────
//
// compileSemanticFilters is the SINGLE canonical mapping from
// search.Query to the typed filter envelopes consumed by
// VectorStorePort. Do NOT duplicate switch or mapping logic in
// multiple files — every ANN and Hybrid path routes through this
// function.
//
// Returns:
//   - SearchScope: workspace isolation envelope (WorkspaceID from
//     q.Actor). IsSystem=false — every user-facing call enforces
//     the workspace must-clause.
//   - AssetFilter: equality filters (Source, Category, MediaType,
//     Language) + lifecycle allowlist (ACTIVE only). Empty fields
//     drop out of the pgvector filter (no zero-value must-clauses).
//
// The pgvector adapter (platform/pgvector/search_adapter.go)
// internally calls CompileVectorFilter with these values.
// semanticDisplayTitle picks the operator-facing label for a semantic hit: the
// SSOT title when present, else the canonical asset name.
func semanticDisplayTitle(title, name string) string {
	if strings.TrimSpace(title) != "" {
		return title
	}
	return name
}

func compileSemanticFilters(q search.Query) (search.SearchScope, search.AssetFilter) {
	category := strings.TrimSpace(q.Filters.Category)
	if category == "" {
		category = search.InferCategoryFromQuery(q.Text)
	}
	return search.SearchScope{
			WorkspaceID: strings.TrimSpace(q.Actor.WorkspaceID),
			IsSystem:    q.Actor.IsSystem || q.Actor.IsAdmin,
		},
		search.AssetFilter{
			Source:    strings.TrimSpace(q.Filters.Source),
			Category:  category,
			MediaType: strings.TrimSpace(q.Filters.MediaType),
			Language:  strings.TrimSpace(q.Filters.Language),
			// Canonical taxonomy dimensions. Source is provenance; the asset
			// FAMILY (e.g. stock_video) and its usage intent (stock) are these.
			// Without them the only way to ask for "stock clips" is by
			// provenance, which excludes every YouTube-acquired stock clip.
			AssetKind:    strings.TrimSpace(q.Filters.AssetKind),
			SemanticRole: strings.TrimSpace(q.Filters.SemanticRole),
			// LifecycleState: include both ACTIVE and PUBLISHED
			// because the stock pipeline indexes assets with
			// lifecycle_state=PUBLISHED (not ACTIVE). The pgvector
			// adapter defaults to ACTIVE-only when empty, so we
			// must explicitly include both states.
			// PR-SEMANTIC-LIFECYCLE-FIX (July 2026): added PUBLISHED
			// to match the actual indexed data.
			LifecycleState: []string{"ACTIVE", "PUBLISHED"},
		}
}

// ── Logger safety ────────────────────────────────────────────────────

// warn logs a warning through b.log without panicking if the
// logger is nil (composition root may legitimately pass nil in
// stripped-down bootstrap or test paths).
func (b *semanticSearchBackend) warn(msg string, fields ...zap.Field) {
	if b.log == nil {
		return
	}
	b.log.Warn(msg, fields...)
}

func (b *semanticSearchBackend) rerankCandidates(ctx context.Context, q search.Query, candidates []search.Candidate, assetsByID map[string]search.MediaAsset) ([]search.Candidate, error) {
	// An explicitly disabled or absent optional capability leaves the
	// canonical pgvector ranking untouched. Once enabled, however, every
	// reranker failure is returned to the caller: silently returning a
	// successful but degraded search would misrepresent availability.
	if b.reranker == nil || !b.reranker.IsEnabled() || len(candidates) == 0 {
		return candidates, nil
	}

	prof := search.Resolve(q.Filters.Source)
	topK := b.reranker.TopK()
	if prof.RerankTopK > 0 && (topK <= 0 || prof.RerankTopK < topK) {
		topK = prof.RerankTopK
	}
	if topK <= 0 || topK > len(candidates) {
		topK = len(candidates)
	}

	ordered := search.RankByScore(candidates)
	requestCandidates := make([]reranker.Candidate, 0, topK)
	for _, cand := range ordered[:topK] {
		asset, ok := assetsByID[cand.AssetID]
		if !ok {
			continue
		}
		assetProfile := search.Resolve(asset.Source)
		requestCandidates = append(requestCandidates, reranker.Candidate{
			ID:          cand.AssetID,
			Text:        search.CandidateText(assetProfile, asset.Name, asset.Category, asset.Language, asset.SearchText, asset.Tags, asset.Source, asset.MediaType),
			VectorScore: float64Ptr(cand.Score),
		})
	}
	if len(requestCandidates) == 0 {
		return candidates, nil
	}

	results, err := b.reranker.Rerank(ctx, q.Text, requestCandidates)
	if err != nil {
		b.warn("semantic backend: enabled reranker failed; search request rejected",
			zap.Error(err),
		)
		return nil, fmt.Errorf("semantic backend: reranker: %w", err)
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("semantic backend: reranker returned no results for %d candidates", len(requestCandidates))
	}

	rawScores := make(map[string]float64, len(results))
	for _, r := range results {
		rawScores[r.ID] = r.RerankScore
	}
	normalized := reranker.NormalizeScores(rawScores)
	weight := b.reranker.Weight()
	if weight <= 0 {
		weight = 0.35
	}
	weight = prof.BlendWeight(weight)

	updated := make([]search.Candidate, len(candidates))
	copy(updated, candidates)
	for i := range updated {
		if rerankScore, ok := normalized[updated[i].AssetID]; ok {
			updated[i].Score = reranker.MixedScore(updated[i].Score, rerankScore, weight)
		}
	}

	// ── Canonical precision recipe ───────────────────────────
	// pgvector top_k → BGE → top 5: after blending, return only the
	// semanticRerankTopResults most relevant assets. The final
	// RankByScore in Search re-sorts; truncating here caps the semantic
	// leg at the 5 best hits (the aggregator still trims the merged
	// page to the requested size).
	updated = search.RankByScore(updated)
	if len(updated) > semanticRerankTopResults {
		updated = updated[:semanticRerankTopResults]
	}

	return updated, nil
}

func float64Ptr(v float64) *float64 {
	return &v
}

// ── Tag matching ──────────────────────────────────────────────────────

// matchesAllTags returns true when every filter tag is present in
// the asset tag list (AND semantics). Case-insensitive comparison.
// An empty filter list always matches (no-op filter).
func isSearchableLifecycleState(state string) bool {
	for _, allowed := range search.SearchableLifecycleStates {
		if state == allowed {
			return true
		}
	}
	return false
}

func matchesAllTags(assetTags, filterTags []string) bool {
	if len(filterTags) == 0 {
		return true
	}
	tagSet := make(map[string]struct{}, len(assetTags))
	for _, t := range assetTags {
		tagSet[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	for _, t := range filterTags {
		if _, ok := tagSet[strings.ToLower(strings.TrimSpace(t))]; !ok {
			return false
		}
	}
	return true
}
