// Package mediasearch — service.go is the QDRANT-004 orchestrator.
//
// Pipeline (single-tenant, single-workspace, hybrid-vector):
//
//	1. Authorisation: workspace context must be present and non-default.
//	2. Embedding:      generate dense vector for the query text.
//	3. Hybrid search:  call the existing assets/search.VectorStorePort
//	                   (real dense + sparse fusion via fuseSearchResults
//	                   in internal/infrastructure/qdrant/service.go).
//	4. Score filter:   drop hits below MinScore pre-hydration so the
//	                   SQL read sequence is shorter (N+1 paranoia).
//	5. Hydration:      batched SQLite read via MediaReadRepository.
//	6. Joins:         the hydrated rows win on metadata; the Qdrant
//	                  payload's score is canonical.
//	7. Delivery URL:  each surviving asset is granted a short-TTL
//	                  signed URL via AssetDeliveryService.
//	8. Response:    hits are returned in the same order as the vector
//	                  order — search relevance is the contract, not
//	                  "stable sort by name".
//
// QDRANT-004 BLOCKER NOTE: cross-tenant isolation at the SQL level
// requires QDRANT-001 (media_assets.workspace_id column not yet
// present). Until that lands, isolation is auth-context only and
// documented as such. The service keeps the WorkspaceContext in
// every signature so the SQL filter can be added later without
// signature drift.
package mediasearch

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
)

// Logger is a minimal logging port the service uses for debug +
// audit-friendly observability. Mirrors application/assets/search
// for consistency, but is local so callers don't need to re-import
// pkg defaults.
type Logger interface {
	Info(msg string, keysAndValues ...any)
	Warn(msg string, keysAndValues ...any)
	Debug(msg string, keysAndValues ...any)
}

// Config groups tuning knobs the service respects.
type Config struct {
	// DefaultLimit / MaxLimit are mirrored from types.go. Held in
	// Config so the operator can tighten them at runtime via the
	// admin port without a rebuild. Zero values fall back to the
	// constants in types.go.
	DefaultLimit int
	MaxLimit     int

	// MinScoreFloor is the score below which a hit is dropped even
	// if the caller-supplied MinScore is 0. Acts as a circuit-breaker
	// against embedding regressions. Zero disables the floor.
	MinScoreFloor float64

	// HybridWeights / HybridThreshold let the operator tune the
	// dense vs. transcript vs. sparse scoring. Phase 6 follow-up
	// (QDRANT-005 territory): the values are wired through the
	// request but not enforced at app-level yet — Qdrant's RRF
	// fusion still gates on the score_threshold param.
	HybridWeights HybridWeights
}

// HybridWeights is a closed set of channel weights. The orchestrator
// doesn't enforce them yet (QDRANT-005); storing them on Config
// keeps the call site future-proof.
type HybridWeights struct {
	Dense       float64
	Transcript  float64
	Sparse      float64
}

// Service is the QDRANT-004 MediaSearchService. It does not own any
// state; all collaborators are passed through the constructor and
// read on each call (so the admin port can hot-reload Config).
type Service struct {
	vector  VectorSearchPort
	read    MediaReadRepository
	deliver AssetDeliveryService
	cfg     Config
	log     Logger
}

// NewService constructs the orchestrator. All dependencies are
// required except Logger (gets a noop default if nil).
func NewService(
	vector VectorSearchPort,
	read MediaReadRepository,
	deliver AssetDeliveryService,
	cfg Config,
	log Logger,
) *Service {
	if log == nil {
		log = noopLogger{}
	}
	return &Service{
		vector:  vector,
		read:    read,
		deliver: deliver,
		cfg:     cfg,
		log:     log,
	}
}

// Search orchestrates the full pipeline described at the package
// header. Errors are returned as sentinel values where it makes
// sense for the handler to map them to status codes.
func (s *Service) Search(ctx context.Context, req MediaSearchRequest) (*MediaSearchResponse, error) {
	// Auth/tenant guard (QDRANT-004 §workspace enforcement).
	//
	// req.Workspace is a value type (struct), so a nil-check at this
	// level is not possible; we instead detect the zero-value via
	// WorkspaceID - the same field the rejection below checks. The
	// Debug log makes the silent filter-disable path (qdrant layer
	// drops the must-filter when WorkspaceID is empty) auditable
	// even though the request is rejected afterwards.
	//
	// Returning ErrMissingWorkspace makes the rejection loud; the
	// accompanying debug log makes it traceable for the
	// workspace_id_empty dashboard metric.
	if strings.TrimSpace(req.Workspace.WorkspaceID) == "" {
		s.log.Debug("mediasearch.Search: WorkspaceID empty would silently disable qdrant-layer tenant filter; rejecting via ErrMissingWorkspace")
		return nil, ErrMissingWorkspace
	}
	if req.Workspace.WorkspaceID == "default" {
		s.log.Debug("mediasearch.Search: WorkspaceID is the reserved sentinel \"default\"; refusing to fan out across tenant spaces")
		return nil, ErrMissingWorkspace
	}
	q := strings.TrimSpace(req.Query)
	if q == "" {
		return nil, fmt.Errorf("mediasearch: query is required")
	}

	mode := req.Mode
	if mode == "" {
		mode = SearchModeHybrid // spec default: real hybrid search
	}

	limit := req.Limit
	if limit <= 0 {
		limit = effectiveDefaultLimit(s.cfg)
	}
	if max := effectiveMaxLimit(s.cfg); max > 0 && limit > max {
		limit = max
	}

	minScore := req.MinScore
	if minScore <= 0 && s.cfg.MinScoreFloor > 0 {
		minScore = s.cfg.MinScoreFloor
	}

	s.log.Debug("mediasearch.Search",
		"workspace", req.Workspace.WorkspaceID,
		"query", q,
		"mode", string(mode),
		"limit", limit,
		"min_score", minScore,
	)

	// ── 2. Embedding ────────────────────────────────────────────────
	if s.vector == nil {
		return nil, fmt.Errorf("mediasearch: vector port not configured")
	}
	denseVector, err := s.vector.EmbedTextForVector(ctx, q, "text")
	if err != nil {
		return nil, fmt.Errorf("mediasearch: embed query: %w", err)
	}

	// ── 3. Hybrid search ────────────────────────────────────────────
	vectorCfg := search.VectorConfig{
		TextVectorName: "text",
	}
	// ConfigPort is optional: some vector backends expose named
	// vector dimensions; if the concrete doesn't implement it we
	// fall back to the zero value above (all-lowercase names).
	if cfgPort, ok := s.vector.(search.ConfigPort); ok {
		vectorCfg = cfgPort.VectorConfig()
	}
	// Defensive: a buggy ConfigPort impl that returns zero-value
	// VectorConfig would silently produce an empty DenseVectorName
	// and silently fail at Qdrant. Re-pin the default.
	if vectorCfg.TextVectorName == "" {
		vectorCfg.TextVectorName = "text"
	}

	// QDRANT-004 §hybrid: "dense+sparse realmente implementati".
	//
	// The transcript channel is intentionally NOT populated today:
	// feeding the same dense vector to it would silently inflate
	// qdrant.fuseSearchResults (the two channels would rank identical
	// points the same way → spurious boost). A dedicated transcript
	// embedder is the right fix; that belongs to a follow-up (likely
	// QDRANT-003+ aliasing work). Until then, hybrid = dense + sparse.
	var rawHits []search.VectorSearchResult
	wsID := req.Workspace.WorkspaceID
	if mode == SearchModeHybrid {
		rawHits, err = s.vector.VectorStore().HybridSearch(ctx, search.HybridSearchRequest{
			QueryText:       q,
			DenseVector:     denseVector,
			DenseVectorName: vectorCfg.TextVectorName,
			Limit:           limit * 2, // over-fetch: hydration may drop a few rows
			MinScore:        minScore,
			Source:          req.Filters.Source,
			Category:        req.Filters.Category,
			MediaType:       req.Filters.MediaType,
			Language:        req.Filters.Language,
			WorkspaceID:     wsID,
		})
	} else {
		rawHits, err = s.vector.VectorStore().Search(ctx, search.VectorSearchRequest{
			QueryVector: denseVector,
			VectorName:  vectorCfg.TextVectorName,
			Limit:       limit * 2,
			MinScore:    minScore,
			Source:      req.Filters.Source,
			Category:    req.Filters.Category,
			MediaType:   req.Filters.MediaType,
			Language:    req.Filters.Language,
			WorkspaceID: wsID,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("mediasearch: vector store: %w", err)
	}

	// ── 4. Score floor + tag filter ──────────────────────────────────
	candidates := make([]search.VectorSearchResult, 0, len(rawHits))
	for _, h := range rawHits {
		if minScore > 0 && h.Score < minScore {
			continue
		}
		if len(req.Filters.Tags) > 0 && !hasAllTags(h.Tags, req.Filters.Tags) {
			continue
		}
		candidates = append(candidates, h)
	}

	// ── 5. Hydration (batched) ──────────────────────────────────────
	if s.read == nil {
		return nil, fmt.Errorf("mediasearch: media read repository not configured")
	}
	assetIDs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if c.AssetID != "" {
			assetIDs = append(assetIDs, c.AssetID)
		}
	}
	assets, err := s.read.GetMany(ctx, req.Workspace, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("mediasearch: hydrate: %w", err)
	}
	assetsByID := indexAssetsByID(assets)

	// ── 6-7. Join + sign ────────────────────────────────────────────
	channelsUsed := channelsForMode(mode)
	out := make([]SearchHit, 0, len(candidates))
	for _, c := range candidates {
		asset, ok := assetsByID[c.AssetID]
		if !ok {
			// Hydration dropped it: skip — don't expose Qdrant metadata directly.
			s.log.Debug("mediasearch: drop unhydrated hit",
				"asset_id", c.AssetID, "score", c.Score)
			continue
		}

		// QDRANT-004 §advanced filters: enforce DurationMsMin
		// post-hydration. The vector payload doesn't always carry
		// duration_ms; canonical comes from SQLite so the check has
		// to run after GetMany returns. duration_ms == 0 means
		// "unknown" — we let those through so non-video rows aren't
		// spuriously dropped (the filter is video-relevant per spec).
		if req.Filters.DurationMsMin > 0 && asset.DurationMs > 0 && asset.DurationMs < req.Filters.DurationMsMin {
			s.log.Debug("mediasearch: drop below DurationMsMin",
				"asset_id", asset.ID,
				"duration_ms", asset.DurationMs,
				"min_ms", req.Filters.DurationMsMin)
			continue
		}

		var deliveryURL string
		if s.deliver != nil {
			url, derr := s.deliver.BuildAuthorizedURL(ctx, req.Workspace, asset.ID)
			if derr != nil {
				// Don't fail the whole search — Surface a malformed entry
				// without a delivery_url so the client can retry just for it.
				s.log.Warn("mediasearch: sign delivery URL failed",
					"asset_id", asset.ID, "error", derr)
			} else {
				deliveryURL = url
			}
		}

		hit := SearchHit{
			AssetID:         asset.ID,
			Score:           c.Score,
			MatchedChannels: deriveMatchedChannels(c, mode),
			Reason:          buildReason(c.Score, asset, req.Query),
			Name:            asset.Name,
			Source:          asset.Source,
			MediaType:       asset.MediaType,
			Category:        asset.Category,
			Tags:            asset.Tags,
			Language:        asset.Language,
			DurationMs:      asset.DurationMs,
			Width:           asset.Width,
			Height:          asset.Height,
			DeliveryURL:     deliveryURL,
		}
		out = append(out, hit)
	}

	// ── 8. Trim to limit (over-fetch then hydrate then trim) ───────
	if len(out) > limit {
		out = out[:limit]
	}

	s.log.Info("mediasearch.Search completed",
		"workspace", req.Workspace.WorkspaceID,
		"query_len", len(q),
		"raw_hits", len(rawHits),
		"hydrated", len(out),
	)

	return &MediaSearchResponse{
		OK:           true,
		Query:        QueryEcho{Normalized: q, ChannelsUsed: channelsUsed, Mode: string(mode)},
		Count:        len(out),
		Hits:         out,
		IndexVersion: indexVersionLabel(),
	}, nil
}

// ── Helpers ────────────────────────────────────────────────────────────

func hasAllTags(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	haveSet := make(map[string]struct{}, len(have))
	for _, t := range have {
		haveSet[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	for _, w := range want {
		if _, ok := haveSet[strings.ToLower(strings.TrimSpace(w))]; !ok {
			return false
		}
	}
	return true
}

func channelsForMode(mode SearchMode) []string {
	// hybrid today means dense + sparse; transcript channel is
	// intentionally omitted pending a dedicated transcript embedder
	// (see HybridSearch call site for rationale).
	if mode == SearchModeHybrid {
		return []string{ChannelDense, ChannelSparseBM25}
	}
	return []string{ChannelDense}
}

func deriveMatchedChannels(h search.VectorSearchResult, mode SearchMode) []string {
	out := make([]string, 0, 2)
	if mode == SearchModeHybrid {
		// Sparse participation is observable via a populated SearchText
		// payload from Qdrant (qdrant/service.go::searchResultFromPoint
		// surfaces it). Without it we conservatively say "dense only".
		if h.SearchText != "" {
			out = append(out, ChannelDense, ChannelSparseBM25)
			return out
		}
		out = append(out, ChannelDense)
		return out
	}
	out = append(out, ChannelDense)
	return out
}

func buildReason(score float64, asset MediaAsset, query string) string {
	parts := []string{}
	switch {
	case score >= 0.85:
		parts = append(parts, "very high semantic similarity")
	case score >= 0.70:
		parts = append(parts, "high semantic similarity")
	case score >= 0.55:
		parts = append(parts, "good thematic match")
	default:
		parts = append(parts, "partial semantic match")
	}
	if asset.Source != "" {
		parts = append(parts, "source: "+asset.Source)
	}
	if asset.Language != "" {
		parts = append(parts, "lang: "+asset.Language)
	}
	if n := countNameMatches(asset.Name, query); n > 0 {
		parts = append(parts, fmt.Sprintf("name matches %d query term(s)", n))
	}
	return strings.Join(parts, " | ")
}

func countNameMatches(name, query string) int {
	if name == "" || query == "" {
		return 0
	}
	nameLower := strings.ToLower(name)
	tokens := strings.Fields(strings.ToLower(query))
	count := 0
	for _, t := range tokens {
		if len(t) >= 3 && strings.Contains(nameLower, t) {
			count++
		}
	}
	return count
}

func indexAssetsByID(assets []MediaAsset) map[string]MediaAsset {
	out := make(map[string]MediaAsset, len(assets))
	for _, a := range assets {
		if a.ID != "" {
			out[a.ID] = a
		}
	}
	return out
}

func indexVersionLabel() string {
	// Pin to a const so callers can grep for "v1-search-api" to find
	// all handlers that need bumping when we cross major versions.
	return "v1-search-api"
}

func effectiveDefaultLimit(cfg Config) int {
	if cfg.DefaultLimit > 0 {
		return cfg.DefaultLimit
	}
	return DefaultLimit
}

func effectiveMaxLimit(cfg Config) int {
	if cfg.MaxLimit > 0 {
		return cfg.MaxLimit
	}
	return MaxLimit
}

// noopLogger swallows everything; used when callers don't pass one.
type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}
