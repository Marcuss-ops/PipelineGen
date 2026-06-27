// Package mediasearch (api) — cutover.go holds the translation helpers
// that bridge the canonical search.Aggregator output to the legacy
// MediaSearchResponse envelope.
//
// Wave 21 PR 10 (June 2026): the canonical search.Result is the SSOT.
// The Aggregator has a NARROWER Candidate shape than the legacy
// SearchHit (only AssetID, Score, Name, Source, MediaType, DeliveryURL),
// so the translation is intentionally lossy on the legacy fields
// (Tags, Language, DurationMs, Width, Height, MatchedChannels, Reason,
// RequestID). See architecture/deprecations.yaml PR-SEARCH-LEGACY-* records.
//
// Architectural placement note (Wave 19):
//
//   This file lives in internal/api/mediasearch/, NOT internal/app/.
//   Internal/api is thin transport only (AGENTS.md Pattern 8) and
//   MUST NOT depend on the composition root (internal/app/) — the
//   composition root imports api to wire routes, so the reverse
//   direction would create an import cycle. The helpers are
//   package-local so the handler can call them as bare names.
package mediasearch

import (
	"strings"

	mediasearchapp "github.com/Marcuss-ops/PipelineGen/internal/application/mediasearch"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// resultToMediaSearchResponse converts the canonical search.Result
// into the legacy MediaSearchResponse envelope for migration-window
// byte-equivalence.
//
// Result.Partial flips OK=false ONLY when items is empty AND any
// backend errored; this is the "no fake availability" version of the
// legacy hard-coded OK=true. Callers see a degraded envelope rather
// than a confident empty success.
func resultToMediaSearchResponse(r *search.Result, req mediasearchapp.MediaSearchRequest) *mediasearchapp.MediaSearchResponse {
	ok := true
	var hits []mediasearchapp.SearchHit
	if r != nil {
		if r.Partial && len(r.Items) == 0 {
			ok = false
		}
		hits = make([]mediasearchapp.SearchHit, 0, len(r.Items))
		for _, c := range r.Items {
			hits = append(hits, mediasearchapp.SearchHit{
				AssetID:     c.AssetID,
				Score:       c.Score,
				Name:        c.Title,
				Source:      c.Source,
				MediaType:   c.MediaType,
				DeliveryURL: c.PreviewURL,
			})
		}
	}
	return &mediasearchapp.MediaSearchResponse{
		OK: ok,
		Query: mediasearchapp.QueryEcho{
			Normalized:   strings.TrimSpace(req.Query),
			ChannelsUsed: nil, // dropped in PR 10 (Candidate's narrower shape)
			Mode:         string(req.Mode),
		},
		Count:        len(hits),
		Hits:         hits,
		IndexVersion: "v1-search-api",
	}
}

// searchQueryFromRequest builds a search.Query from the API request DTO.
// Kept as a package-local helper so the handler stays focused on auth +
// transport; the request-DTO → canonical-Query mapping is its own concern.
func searchQueryFromRequest(req searchRequest, mode mediasearchapp.SearchMode, limit int) search.Query {
	return search.Query{
		Text:  strings.TrimSpace(req.Query),
		Mode:  mode,
		Limit: limit,
		Filters: search.Filters{
			Source:        strings.TrimSpace(req.Filters.Source),
			MediaType:     strings.TrimSpace(req.Filters.MediaType),
			Category:      strings.TrimSpace(req.Filters.Category),
			Language:      strings.TrimSpace(req.Filters.Language),
			Tags:          req.Filters.Tags,
			DurationMsMin: req.Filters.DurationMsMin,
		},
	}
}
