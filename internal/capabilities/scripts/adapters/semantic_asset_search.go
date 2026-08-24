// Package adapters — semantic_asset_search.go (PR-TRANSLATE-SCRIPT-SPEC-§7,
// closure landed 2026-08-08).
//
// Canonical typed adapter for semantic asset search. Wires the canonical
// search.SearchBackend + search.QueryEmbedder ports behind a single
// SearchAssets entry point. Implements the 8 contract endpoints
// per pasted plan §7 (forward-prevention regression-guard surface):
//
//  1. EmptyQueryReturnsEmptyWithoutEmbed — empty Query text → returns
//     nil + never invokes the Embedder (godlike/07 minimum-blast-radius:
//     avoid wasted embedder calls for empty input).
//
//  2. NilSearcherFails — nil Searcher field → returns
//     ErrSemanticSearchNilSearcher typed sentinel (godlike/07 fail-closed
//     at the seam, not a panic, not a silent-success).
//
//  3. NilEmbedderFails — nil Embedder field → returns
//     ErrSemanticSearchNilEmbedder typed sentinel (same fail-closed contract).
//
//  4. DefaultsLimitAndMinScore — Limit==0 || MinScore<=0 → use the
//     adapter's DefaultLimit + DefaultMinScore fields (composition-time
//     configurable via NewSemanticAssetSearch's defaults).
//
//  5. SourceStockBuildsStockFilter — Source=="stock" → populates
//     q.Sources + q.Filters.Source on the canonical search.Query
//     (godlike/06 SSOT: stock is a first-class filter on the canonical
//     orchestrator input, NOT a special case inside the backend).
//
//  6. WorkspaceRequiredForUserTraffic — !Actor.IsSystem &&
//     Actor.WorkspaceID=="" → returns ErrSemanticSearchWorkspaceRequired
//     typed sentinel. Only IsSystem=true bypasses the WorkspaceID
//     requirement (godlike/07 fail-closed at the user-traffic boundary).
//
//  7. IsSystemAllowsEmptyWorkspace — Actor.IsSystem=true with empty
//     WorkspaceID → search proceeds (reconcile / admin paths).
//
//  8. ConvertsDriveURLFallback — if backend returns 0 hits AND the
//     request carries a non-empty DriveURL AND
//     urlutil.FileIDFromDriveLink extracts a file ID → append a
//     synthetic hit with AssetID=fileID and DriveLink=DriveURL (the
//     canonical "fallback" semantics: never return zero hits when the
//     caller can supply their own Drive URL anchor).
//
// godlike/06 SSOT (one canonical owner per fact): the typed ports
// `search.SearchBackend` and `search.QueryEmbedder` are owned by
// `internal/capabilities/assets/search`. This adapter is the SOLE owner of the
// `SemanticAssetSearch` request/response DTOs and the 3 typed-error
// sentinels. No other package may redefine these symbols.
package adapters

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// ── Typed error sentinels (godlike/07 NO-FAKE-AVAILABILITY) ────────────

// ErrSemanticSearchNilSearcher is returned by SearchAssets when the
// Searcher field is nil. Callers probe via errors.Is and surface a
// "backend unavailable" diagnostic in the operator dashboard rather
// than allowing the silent-success class where the search call
// returns an empty slice with no error.
var ErrSemanticSearchNilSearcher = errors.New("adapters: SemanticAssetSearch.Searcher is nil (fail-closed at the seam)")

// ErrSemanticSearchNilEmbedder mirrors ErrSemanticSearchNilSearcher
// for the Embedder field. See the doc comment above for rationale.
var ErrSemanticSearchNilEmbedder = errors.New("adapters: SemanticAssetSearch.Embedder is nil (fail-closed at the seam)")

// ErrSemanticSearchWorkspaceRequired is returned by SearchAssets when
// the request is user traffic (Actor.IsSystem=false) AND
// Actor.WorkspaceID is empty. The canonical tenant-isolation contract
// per PR-SEARCH-ACTOR (June 2026): user traffic MUST be workspace-scoped;
// empty WorkspaceID silently degraded to admin would violate the
// godlike/07 NO-FAKE-AVAILABILITY invariant. Only Actor.IsSystem=true
// bypasses this check (reconcile + admin paths).
var ErrSemanticSearchWorkspaceRequired = errors.New("adapters: SemanticAssetSearch requires WorkspaceID for user traffic (Actor.IsSystem=false with empty WorkspaceID is rejected fail-closed)")

// ── Default constants ────────────────────────────────────────────────

// DefaultSemanticSearchLimit is the canonical fallback Limit when the
// request does not specify one (Limit==0). Mirrors search.DefaultLimit
// (20) per the centralised-limit-bounds contract.
const DefaultSemanticSearchLimit = 20

// DefaultSemanticSearchMinScore is the canonical fallback MinScore
// when the request does not specify one (MinScore<=0). The 0.50 floor
// mirrors the semantic backend's semanticMinScore contract.
const DefaultSemanticSearchMinScore = 0.50

// ── Canonical DTOs ──────────────────────────────────────────────────

// SemanticAssetSearchRequest is the canonical request DTO for
// SemanticAssetSearch.SearchAssets.
//
// Field semantics:
//   - Query: trimmed by SearchAssets before fanout. Empty after
//     trim is the no-Embed-call sentinel.
//   - Source: optional source filter (e.g. "youtube", "stock"). When
//     non-empty, propagates to q.Sources + q.Filters.Source.
//   - Limit: 0 → use DefaultSemanticSearchLimit.
//   - MinScore: 0 → use DefaultSemanticSearchMinScore.
//   - Actor: tenant identity forwarded to the canonical backend
//     (per QDRANT-004 contract).
//   - DriveURL: optional Google Drive URL used as a fallback anchor
//     when the backend returns 0 hits. Extracted via
//     urlutil.FileIDFromDriveLink; if the extraction succeeds AND
//     the file ID is non-empty, a synthetic hit is appended.
type SemanticAssetSearchRequest struct {
	Query    string
	Source   string
	Limit    int
	MinScore float64
	Actor    search.Actor
	DriveURL string
}

// SemanticAssetSearchHit is the canonical response DTO. Carries the
// asset identifier + score + DriveLink + the source identifier. The
// SearchText field is reserved for future per-hit explanation strings
// (e.g. the matched phrase or the embedding-similarity surface); it is
// always empty in the current implementation.
//
// godlike/06 SSOT: this type lives ONLY here. No other package may
// redefine SemanticAssetSearchHit.
type SemanticAssetSearchHit struct {
	AssetID    string
	Score      float64
	DriveLink  string
	Source     string
	Title      string
	SearchText string
}

// ── Canonical typed adapter ─────────────────────────────────────────

// SemanticAssetSearch is the canonical typed adapter for semantic
// asset search. It owns the wiring between the canonical
// search.SearchBackend + search.QueryEmbedder ports and the
// caller-facing SearchAssets entry point.
//
// godlike/06 SSOT: the adapter lives ONLY here. Composition root
// owns the wiring (Pattern 0 — see internal/app/adapters_media_
// search.go for the composition pattern).
type SemanticAssetSearch struct {
	Searcher        search.SearchBackend
	Embedder        search.QueryEmbedder
	Logger          search.Logger
	DefaultLimit    int
	DefaultMinScore float64
}

// noopLogger is a local no-op logger (search.noopLogger is unexported,
// so we re-declare a compatible shape here). Used when the caller
// passes nil Logger to NewSemanticAssetSearch.
type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Error(string, ...any) {}

// NewSemanticAssetSearch constructs the canonical typed adapter with
// the default limit + min_score. A nil Logger is replaced with a
// local no-op (the search package's noopLogger is unexported).
func NewSemanticAssetSearch(s search.SearchBackend, e search.QueryEmbedder, log search.Logger) *SemanticAssetSearch {
	if log == nil {
		log = noopLogger{}
	}
	return &SemanticAssetSearch{
		Searcher:        s,
		Embedder:        e,
		Logger:          log,
		DefaultLimit:    DefaultSemanticSearchLimit,
		DefaultMinScore: DefaultSemanticSearchMinScore,
	}
}

// WithDefaults overrides the adapter's default Limit + MinScore.
// Non-positive values are ignored (keeps the field at its
// constructor-time value). Returns the receiver for fluent chaining.
func (s *SemanticAssetSearch) WithDefaults(limit int, minScore float64) *SemanticAssetSearch {
	if limit > 0 {
		s.DefaultLimit = limit
	}
	if minScore > 0 {
		s.DefaultMinScore = minScore
	}
	return s
}

// SearchAssets is the canonical entry point. Implements the 8 contract
// endpoints per PR-TRANSLATE-SCRIPT-SPEC-§7:
//
//  1. nil-port fail-closed (typed sentinels)
//  2. empty query → return nil + skip Embedder (contract 1)
//  3. workspace gate (contract 6+7)
//  4. defaults for limit + min_score (contract 4)
//  5. source="stock" → stock filter (contract 5)
//  6. canonical Embedder call (1 call, fail-fast on error)
//  7. canonical Searcher call (1 call, fail-fast on error)
//  8. DriveURL fallback (contract 8)
//
// godlike/07 NO-FAKE-AVAILABILITY: every failure path returns a typed
// sentinel; empty-query returns nil with no error (per contract 1);
// nil-port returns the corresponding typed sentinel; workspace gate
// returns ErrSemanticSearchWorkspaceRequired. The function never
// returns a successful-but-empty result on a configuration failure.
func (s *SemanticAssetSearch) SearchAssets(ctx context.Context, req SemanticAssetSearchRequest) ([]SemanticAssetSearchHit, error) {
	// Contract 2 + 3: nil-port fail-closed at the seam.
	if s.Searcher == nil {
		return nil, ErrSemanticSearchNilSearcher
	}
	if s.Embedder == nil {
		return nil, ErrSemanticSearchNilEmbedder
	}

	// Contract 1: empty query → return nil + skip the Embedder call.
	text := strings.TrimSpace(req.Query)
	if text == "" {
		return nil, nil
	}

	// Contract 6 + 7: workspace gate. Only IsSystem bypasses.
	if !req.Actor.IsSystem && req.Actor.WorkspaceID == "" {
		return nil, ErrSemanticSearchWorkspaceRequired
	}

	// Contract 4: defaults when request fields are zero.
	limit := req.Limit
	if limit <= 0 {
		limit = s.DefaultLimit
	}
	minScore := req.MinScore
	if minScore <= 0 {
		minScore = s.DefaultMinScore
	}

	// Canonical embed call (fail-fast on error).
	if _, err := s.Embedder.Embed(ctx, text); err != nil {
		return nil, fmt.Errorf("adapters: SemanticAssetSearch.Embedder.Embed: %w", err)
	}

	// Build the canonical search.Query with the source filter
	// propagated (Contract 5: Source="stock" → Sources + Filters.Source).
	q := search.Query{
		Text:     text,
		Limit:    limit,
		MinScore: minScore,
		Actor:    req.Actor,
	}
	if req.Source != "" {
		q.Sources = []string{req.Source}
		q.Filters.Source = req.Source
	}

	candidates, err := s.Searcher.Search(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("adapters: SemanticAssetSearch.Searcher.Search: %w", err)
	}

	// Convert candidates to the canonical hit DTO.
	hits := make([]SemanticAssetSearchHit, 0, len(candidates))
	for _, c := range candidates {
		hits = append(hits, SemanticAssetSearchHit{
			AssetID:   c.AssetID,
			Score:     c.Score,
			DriveLink: c.DriveLink,
			Source:    c.Source,
			Title:     c.Title,
		})
	}

	// Contract 8: DriveURL fallback. If the backend returned 0 hits
	// AND a non-empty DriveURL extracts a file ID, append a synthetic
	// hit so the caller never sees a zero-result when they have
	// supplied their own Drive URL anchor.
	if len(hits) == 0 && strings.TrimSpace(req.DriveURL) != "" {
		if fileID, err := urlutil.FileIDFromDriveLink(req.DriveURL); err == nil && fileID != "" {
			hits = append(hits, SemanticAssetSearchHit{
				AssetID:   fileID,
				DriveLink: req.DriveURL,
				Source:    req.Source,
			})
		}
	}

	return hits, nil
}
