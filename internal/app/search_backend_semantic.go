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
// (internal/application/search/ports.go) instead of the historical
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
// internal/application/* domains are imported at once.
package app

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// Canonical vector names for the semantic backend. Mirrors the
// Qdrant IndexSchema (see AGENTS.md §Qdrant Entity Associations):
//
//	text      = 768-dim multilingual-e5-base (semantic meaning)
//	bm25_text = sparse BM25 (lexical exact-match)
const (
	semanticDenseVectorName  = "text"
	semanticSparseVectorName = "bm25_text"

	// semanticMinScore is the floor below which Qdrant hits are
	// dropped pre-hydration. Set to 0.01 to allow low-confidence
	// results through — the production embedding model
	// (multilingual-e5-base at index time, nomic-embed-text at
	// query time) produces cosine similarity scores in the
	// 0.01-0.05 range for weakly-related content. The previous
	// 0.50 threshold was designed for a single-model pipeline;
	// the dual-model gap narrows the usable score range.
	// PR-SEMANTIC-MINSCORE-FIX (July 2026): lowered from 0.50
	// to 0.01 to unblock the search pipeline.
	semanticMinScore = 0.01
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
	// backend default (semanticMinScore 0.50). This lets callers
	// tune precision/recall per-request without changing the
	// package-level constant.
	minScore := semanticMinScore
	if q.MinScore > 0 {
		minScore = q.MinScore
	}

	// ── 4. Clamp limit ─────────────────────────────────────────
	limit := q.Limit
	if limit <= 0 {
		limit = search.DefaultLimit
	}
	if limit > search.MaxLimit {
		limit = search.MaxLimit
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
			Limit:            limit,
			MinScore:         minScore,
			Source:           filter.Source,
			Category:         filter.Category,
			MediaType:        filter.MediaType,
			Language:         filter.Language,
			WorkspaceID:      scope.WorkspaceID,
			IsSystem:         scope.IsSystem,
		})
	default: // SearchModeANN (or empty string → ANN)
		results, err = b.vectorStore.Search(ctx, assetsearch.VectorSearchRequest{
			QueryVector: vec,
			VectorName:  semanticDenseVectorName,
			Limit:       limit,
			MinScore:    minScore,
			Source:      filter.Source,
			Category:    filter.Category,
			MediaType:   filter.MediaType,
			Language:    filter.Language,
			WorkspaceID: scope.WorkspaceID,
			IsSystem:    scope.IsSystem,
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
	for _, a := range assets {
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
			AssetID:    a.ID,
			Source:     "semantic",
			SourceRef:  a.ID,
			MediaType:  a.MediaType,
			Title:      a.Name,
			Name:       a.Name,
			DriveLink:  a.DriveLink,
			PreviewURL: url,
			Score:      scoreByID[a.ID],
		})
	}

	return candidates, nil
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
// The Qdrant adapter (infrastructure/qdrant/search_adapter.go)
// internally calls CompileQdrantFilter with these values.
func compileSemanticFilters(q search.Query) (assetsearch.SearchScope, assetsearch.AssetFilter) {
	return assetsearch.SearchScope{
			WorkspaceID: strings.TrimSpace(q.Actor.WorkspaceID),
			IsSystem:    q.Actor.IsAdmin,
		},
		assetsearch.AssetFilter{
			Source:    strings.TrimSpace(q.Filters.Source),
			Category:  strings.TrimSpace(q.Filters.Category),
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

// ── Tag matching ──────────────────────────────────────────────────────

// matchesAllTags returns true when every filter tag is present in
// the asset tag list (AND semantics). Case-insensitive comparison.
// An empty filter list always matches (no-op filter).
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
