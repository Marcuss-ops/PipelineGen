// Package app — search_backend_semantic.go is the Fase 6 (July 2026)
// semantic SearchBackend that connects the canonical search.Aggregator
// to Qdrant for real ANN and Hybrid retrieval. It replaces the
// historical PR-SEARCH-LEGACY-MEDIASEARCH-BACKEND-REMOVAL stub
// (which wrapped *mediasearch.Service in a single-port shape) with
// the canonical two-port architecture:
//
//	EmbeddingChannelRegistry (multi-channel embedding)
//	VectorStorePort          (locator-free ANN/hybrid retrieval)
//
// Plus SQLite hydration (MediaReadRepository) and signed delivery
// URLs (AssetDeliveryService). Per AGENTS.md Pattern 0, every
// external dependency flows through a typed port so tests can swap
// in stubs without touching Qdrant or SQLite.
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
package wiring

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	search "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	searchprofile "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ai/reranker"
)

// Canonical vector names for the semantic backend. Mirrors the
// Qdrant IndexSchema (see AGENTS.md §Qdrant Entity Associations):
//
//	text      = 768-dim intfloat/multilingual-e5-base (semantic meaning)
//	bm25_text = sparse BM25 (lexical exact-match)
const (
	semanticDenseVectorName  = "text"
	semanticSparseVectorName = "bm25_text"

	// semanticMinScore is the ANN (cosine-similarity) floor below which
	// Qdrant hits are dropped pre-hydration. Kept deliberately low: the
	// sampler owns acceptance after hydration, and this backend must not
	// compensate for incompatible embedding spaces with a score hack.
	semanticMinScore = 0.01

	// semanticHybridMinScore is the hybrid (RRF) floor. Qdrant's RRF
	// fusion (rank constant k=1) scores a point that appears in a single
	// prefetch list as 1/(1+rank): rank 9 → 0.1. Raising the floor above
	// 0.1 rejects the rank-9+ single-list tail so irrelevant points (the
	// multi-term "0.1 collapse" noise) never surface pre-hydration.
	// PR-MINSCORE-HYBRID (August 2026).
	semanticHybridMinScore = 0.11

	// semanticRerankTopResults is the final precision limit of the
	// enabled reranker recipe (Qdrant top_k window → BGE re-score →
	// top 5 results). After blending, the semantic backend returns only
	// the semanticRerankTopResults most relevant assets; the aggregator
	// still trims the merged page to the requested size.
	semanticRerankTopResults = 5
)

// semanticSearchBackend is the Fase 6 + PR-EMBEDDING-CHANNEL-REGISTRY
// Qdrant-backed SearchBackend. It fans out a search.Query through the
// canonical two-port architecture:
//
//  1. embeddings.EmbedQuery(ctx, ChannelText, q.Text) → dense vector
//     (delegated to EmbeddingChannelRegistry so new channel encoders
//     plug in at composition root without backend changes)
//  2. vectorStore.Search or .HybridSearch      → Qdrant results
//  3. mediaReader.GetMany                      → SQLite hydration (canonical metadata)
//  4. delivery.BuildAuthorizedURL              → signed delivery URL per hit
//
// Workspace isolation, lifecycle ACTIVE, and equality filters
// (source, category, media_type, language) are applied at the
// Qdrant layer via the VectorStorePort adapter. Tags (AND
// semantics) and DurationMsMin are enforced post-hydration.
//
// PR-EMBEDDING-CHANNEL-REGISTRY (July 2026): Embeddings is the
// canonical multi-channel embedding port (Pattern 0); the legacy
// `embedder search.QueryEmbedder` field is gone — the registry
// owns text-channel encoding now and future cross-modal channels
// plug in here.
type semanticSearchBackend struct {
	embeddings  search.EmbeddingChannelRegistry
	vectorStore assetsearch.VectorStorePort
	mediaReader search.MediaReadRepository
	delivery    search.AssetDeliveryService
	log         *zap.Logger
	reranker    rerankerClient
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
// can return. Qdrant indexes all four, so the backend is
// eligible for any Query.MediaTypes ∩ these four.
func (b *semanticSearchBackend) Capabilities() []search.Capability {
	return []search.Capability{
		search.CapVideo,
		search.CapImage,
		search.CapAudio,
		search.CapMusic,
	}
}

// Universe reports SearchCatalog: the semantic backend searches Qdrant
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

	// ── 1. Text embedding via canonical EmbeddingChannelRegistry ──────
	// PR-EMBEDDING-CHANNEL-REGISTRY (July 2026): we delegate the
	// text-channel encoding to the registry instead of calling
	// QueryEmbedder.Embed directly. Today's registry wires text+transcript
	// both to the qdrant.TextEmbedder; future SigLIP-text/CLAP-text
	// encoders plug in to the visual/audio channels at composition
	// root without this backend knowing. The ChannelText constant is
	// the canonical SSOT channel name (godlike/06) and matches the
	// Qdrant vector name "text" (semanticDenseVectorName) 1:1.
	vec, err := b.embeddings.EmbedQuery(ctx, search.ChannelText, q.Text)
	if err != nil {
		return nil, fmt.Errorf("semantic backend: embed channel %q: %w", search.ChannelText, err)
	}
	if len(vec) == 0 {
		b.warn("semantic backend: embedder returned zero-length vector",
			zap.String("channel", search.ChannelText),
			zap.String("text", q.Text))
		return nil, nil
	}

	// ── 2. Compile filters ─────────────────────────────────────
	scope, filter := compileSemanticFilters(q)

	// ── 3. Resolve score floor ───────────────────────────────
	// PR-MINSCORE-QUERY (July 2026): q.MinScore > 0 overrides the
	// backend defaults. ANN (cosine) and hybrid (RRF) use different
	// score scales, so each path resolves its own floor; a caller-set
	// q.MinScore overrides BOTH so per-request tuning stays uniform.
	annMinScore := semanticMinScore
	hybridMinScore := semanticHybridMinScore
	if q.MinScore > 0 {
		annMinScore = q.MinScore
		hybridMinScore = q.MinScore
	}

	// ── 4. Clamp limit ─────────────────────────────────────────
	limit := q.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}
	if limit > search.MaxLimit {
		limit = search.MaxLimit
	}

	// ── 4b. Rerank fetch window ───────────────────────────────
	// Canonical recipe (Qdrant top_k → BGE → top 5): when the reranker
	// is enabled, Qdrant must return a wider candidate window than the
	// final page so BGE can genuinely re-score the top_k window and the
	// backend can return the top semanticRerankTopResults. The window
	// is capped at MaxLimit so a pathological top_k cannot explode the
	// fetch.
	fetchLimit := limit
	if b.reranker != nil && b.reranker.IsEnabled() {
		if w := b.reranker.TopK(); w > fetchLimit {
			fetchLimit = w
		}
		if fetchLimit > search.MaxLimit {
			fetchLimit = search.MaxLimit
		}
	}

	// ── 5. Execute vector search ───────────────────────────────
	var results []assetsearch.VectorSearchResult
	switch q.Mode {
	case search.SearchModeHybrid:
		// TranscriptVector is intentionally absent: the
		// orchestrator does NOT pass a transcript-channel
		// vector today (passing the same dense vector would
		// silently inflate Qdrant RRF fusion). A dedicated
		// transcript embedder is QDRANT-005 territory.
		// See mediasearch/ports.go::ChannelTranscript comment.
		results, err = b.vectorStore.HybridSearch(ctx, assetsearch.HybridSearchRequest{
			DenseVector:      vec,
			DenseVectorName:  semanticDenseVectorName,
			SparseText:       q.Text,
			SparseVectorName: semanticSparseVectorName,
			Limit:            fetchLimit,
			MinScore:         hybridMinScore,
			Source:           filter.Source,
			Category:         filter.Category,
			MediaType:        filter.MediaType,
			Language:         filter.Language,
			LifecycleState:   filter.LifecycleState,
			WorkspaceID:      scope.WorkspaceID,
			IsSystem:         scope.IsSystem,
		})
	default: // SearchModeANN (or empty string → ANN)
		results, err = b.vectorStore.Search(ctx, assetsearch.VectorSearchRequest{
			QueryVector:    vec,
			VectorName:     semanticDenseVectorName,
			Limit:          fetchLimit,
			MinScore:       annMinScore,
			Source:         filter.Source,
			Category:       filter.Category,
			MediaType:      filter.MediaType,
			Language:       filter.Language,
			LifecycleState: filter.LifecycleState,
			WorkspaceID:    scope.WorkspaceID,
			IsSystem:       scope.IsSystem,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("semantic backend: vector search: %w", err)
	}

	// ── 6. No Qdrant results → done ────────────────────────────
	if len(results) == 0 {
		return nil, nil
	}

	// ── 7. Extract asset IDs + scores ──────────────────────────
	assetIDs := make([]string, 0, len(results))
	scoreByID := make(map[string]float64, len(results))
	for _, r := range results {
		if r.AssetID == "" {
			continue
		}
		// Deduplicate within the same result set: keep the
		// highest score per asset (Qdrant RRF fusion can
		// return the same point via multiple vector channels).
		if existing, ok := scoreByID[r.AssetID]; ok {
			if r.Score > existing {
				scoreByID[r.AssetID] = r.Score
			}
			continue
		}
		assetIDs = append(assetIDs, r.AssetID)
		scoreByID[r.AssetID] = r.Score
	}

	if len(assetIDs) == 0 {
		return nil, nil
	}

	// ── 8. SQLite hydration ────────────────────────────────────
	ws := search.Actor{WorkspaceID: scope.WorkspaceID}
	// SEARCH-T07-LIFECYCLE-DEL (P0, 2026-07-15): the canonical ACTIVE-only
	// filter is hardcoded at the MediaReadRepository impl. Caller no
	// longer threads the allowStates parameter through the interface.
	assets, err := b.mediaReader.GetMany(ctx, ws, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("semantic backend: hydrate: %w", err)
	}

	// ── 9. Post-hydration filters + signed URLs ────────────────
	candidates := make([]search.Candidate, 0, len(assets))
	assetsByID := make(map[string]search.MediaAsset, len(assets))
	for _, a := range assets {
		// Defense in depth against Qdrant/SQLite replication lag or a
		// non-canonical MediaReadRepository implementation. Only the
		// search capability's canonical searchable states may reach the
		// API response; unavailable assets remain diagnosable in SQLite
		// but never become candidates.
		if !isSearchableLifecycleState(a.LifecycleState) {
			continue
		}

		// Tag filter: AND semantics (every filter tag must
		// be present on the asset).
		if !matchesAllTags(a.Tags, q.Filters.Tags) {
			continue
		}

		// Duration floor: enforced post-hydration because
		// canonical duration lives in SQLite, not in the
		// Qdrant payload.
		if q.Filters.DurationMsMin > 0 && a.DurationMs < q.Filters.DurationMsMin {
			continue
		}
		assetsByID[a.ID] = a

		// Signed delivery URL. Admin users access files directly
		// and don't need signed URLs — the signer requires a
		// non-empty workspace which admins don't have.
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
			AssetID:   a.ID,
			Source:    "semantic",
			SourceRef: a.ID,
			MediaType: a.MediaType,
			Title:     a.Name,
			Name:      a.Name,
			// Raw Drive links are intentionally not copied into public
			// semantic-search candidates. SQLite is canonical, but this
			// adapter has no per-request Drive verification capability;
			// exposing the field here could leak a stale URL after
			// reconciliation lag. PreviewURL is the signed delivery
			// surface and remains the only client-facing locator.
			PreviewURL: url,
			Score:      scoreByID[a.ID],
		})
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	adjusted, err := b.rerankCandidates(ctx, q, candidates, assetsByID)
	if err != nil {
		return nil, err
	}
	candidates = adjusted

	return search.RankByScore(candidates), nil
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
//     drop out of the Qdrant filter (no zero-value must-clauses).
//
// The Qdrant adapter (platform/qdrant/search_adapter.go)
// internally calls CompileQdrantFilter with these values.
func compileSemanticFilters(q search.Query) (assetsearch.SearchScope, assetsearch.AssetFilter) {
	category := strings.TrimSpace(q.Filters.Category)
	if category == "" {
		category = search.InferCategoryFromQuery(q.Text)
	}
	return assetsearch.SearchScope{
			WorkspaceID: strings.TrimSpace(q.Actor.WorkspaceID),
			IsSystem:    q.Actor.IsSystem || q.Actor.IsAdmin,
		},
		assetsearch.AssetFilter{
			Source:    strings.TrimSpace(q.Filters.Source),
			Category:  category,
			MediaType: strings.TrimSpace(q.Filters.MediaType),
			Language:  strings.TrimSpace(q.Filters.Language),
			// LifecycleState: include both ACTIVE and PUBLISHED
			// because the stock pipeline indexes assets with
			// lifecycle_state=PUBLISHED (not ACTIVE). The Qdrant
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
	// canonical Qdrant ranking untouched. Once enabled, however, every
	// reranker failure is returned to the caller: silently returning a
	// successful but degraded search would misrepresent availability.
	if b.reranker == nil || !b.reranker.IsEnabled() || len(candidates) == 0 {
		return candidates, nil
	}

	prof := searchprofile.Resolve(q.Filters.Source)
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
		assetProfile := searchprofile.Resolve(asset.Source)
		requestCandidates = append(requestCandidates, reranker.Candidate{
			ID:          cand.AssetID,
			Text:        searchprofile.CandidateText(assetProfile, asset.Name, asset.Category, asset.Language, asset.SearchText, asset.Tags, asset.Source, asset.MediaType),
			QdrantScore: float64Ptr(cand.Score),
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
	// Qdrant top_k → BGE → top 5: after blending, return only the
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
