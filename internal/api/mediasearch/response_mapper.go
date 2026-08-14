package mediasearch

import (
	"strings"

	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
)

// resultToResponse converts the canonical search.Result into the
// handler's public response DTO (searchResponse). Public items retain
// searchable metadata and signed delivery URLs while intentionally
// omitting internal SQLite/Drive locators. OK flips to false only when Partial &&
// zero items (no fake availability).
// PR-AGENTE2-TRUTHFUL (Agente 2, Azione 3): Degraded is true when
// Partial && len(items) > 0 (at least one backend returned results
// but others failed). BackendErrors is the SANITIZED per-backend
// failure map (goes through SanitizeProviderErrors so the public
// wire never leaks stack traces / internal URLs / secrets).
// godlike/07 fail-closed: provider_errors is always populated
// whenever Partial=true (the aggregator always populates).
//
// Commit 2 BACKFILL/CUTOVER: IndexVersion is now sourced from the
// IndexVersionSource port (parameter `indexVer`), replacing the
// is rendered as JSON omitempty so callers do not get a stale
// static string.
func resultToResponse(r *search.Result, query string, mode search.SearchMode, universe search.SearchUniverse, indexVer string) *searchResponse {
	ok := true
	items := make([]searchResultItem, 0)
	var degraded bool
	var backendErrors map[string]string
	if r != nil {
		if r.Partial && len(r.Items) == 0 {
			ok = false
		}
		if r.Partial && len(r.Items) > 0 {
			degraded = true
		}
		if len(r.ProviderErrors) > 0 {
			backendErrors = SanitizeProviderErrors(r.ProviderErrors)
		}
		for _, c := range r.Items {
			items = append(items, searchResultItem{
				AssetID:   c.AssetID,
				Score:     c.Score,
				Title:     c.Title,
				Source:    c.Source,
				MediaType: c.MediaType,
				// PreviewURL passes through UNCHANGED:
				// search.Candidate.PreviewURL is the canonical
				// signed delivery URL produced by the only
				// legitimate source (delivery.Publisher.BuildAuthorizedURL).
				// Sanitising it would break the signed-URL
				// contract QDRANT-004 requires; the safety
				// layer lives at construction time (the signer
				// never mints raw Drive paths), not at
				// projection time. godlike/07 fail-closed
				// applies to error strings, not to valid
				// response URLs.
				PreviewURL: c.PreviewURL,
			})
		}
	}
	nextCursor := ""
	if r != nil {
		nextCursor = r.NextCursor
	}
	partial := false
	if r != nil {
		partial = r.Partial
	}
	var channelsUsed []string
	if r != nil && len(r.ChannelsUsed) > 0 {
		channelsUsed = r.ChannelsUsed
	}
	return &searchResponse{
		OK:            ok,
		Query:         strings.TrimSpace(query),
		Mode:          string(mode),
		Universe:      string(universe),
		Count:         len(items),
		Items:         items,
		Partial:       partial,
		Degraded:      degraded,
		BackendErrors: backendErrors,
		ChannelsUsed:  channelsUsed,
		NextCursor:    nextCursor,
		IndexVersion:  indexVer,
	}
}

// searchQueryFromRequest builds a search.Query from the API request DTO.
// Workspace identity is taken from the canonical search.Actor
// (Commit 2 BACKFILL/CUTOVER migration target of the historical
// WorkspaceContext fed into Query.Actor).
// PR-1 (Agente 2, Azione 1): workspace is propagated into Query.Actor
// so every backend receives the real tenant identity.
// PR-AGENTE2-MEDIATYPE (Agente 2, Azione 2): when the request filter
// carries media_type, it is also forwarded as Query.MediaTypes so
// the BackendRegistry can select capability-compatible backends.
func searchQueryFromRequest(req searchRequest, mode search.SearchMode, limit int, actor search.Actor) search.Query {
	mediaType := strings.TrimSpace(req.Filters.MediaType)
	var mediaTypes []string
	if mediaType != "" {
		mediaTypes = []string{mediaType}
	}
	source := strings.TrimSpace(req.Filters.Source)
	// Propagate the source filter into Query.Sources (plural) so
	// BackendRegistry.Eligible can route to the correct backend.
	// Without this, source=artlist/youtube/stock silently routes to
	// ALL backends (local/semantic included) and the Aggregator
	// returns "no eligible backends" when those non-provider
	// backends fail on the source-filtered query.
	var sources []string
	if source != "" {
		sources = []string{source}
	}
	return search.Query{
		Text:       strings.TrimSpace(req.Query),
		Mode:       mode,
		Universe:   search.ParseUniverse(req.Universe),
		Limit:      limit,
		Sources:    sources,
		MediaTypes: mediaTypes,
		Actor:      actor,
		Filters: search.Filters{
			Source:        source,
			MediaType:     mediaType,
			Category:      strings.TrimSpace(req.Filters.Category),
			Language:      strings.TrimSpace(req.Filters.Language),
			Tags:          req.Filters.Tags,
			DurationMsMin: req.Filters.DurationMsMin,
		},
	}
}
