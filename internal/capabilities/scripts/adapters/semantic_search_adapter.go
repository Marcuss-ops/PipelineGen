package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

// semanticProductionProvider is the provenance stamped on assets retrieved by
// the semantic production plan. It is distinct from the per-segment
// `internet_images` path so a report can prove which queries were executed by
// the planner.
const semanticProductionProvider = "semantic_production_plan"

// SemanticAssetSearch adapts the canonical InternetImageSearcher to the
// semantic batch planner's narrow port. It performs no search of its own and
// owns no cache: every call is the provider path the rest of the pipeline uses,
// so the measured hit rate and latency describe the real retrieval surface.
type SemanticAssetSearch struct {
	searcher InternetImageSearcher
}

// NewSemanticAssetSearch builds the adapter. A nil searcher yields a nil
// adapter, which the wiring treats as "planner unavailable" instead of
// registering a port that can only fail.
func NewSemanticAssetSearch(searcher InternetImageSearcher) *SemanticAssetSearch {
	if searcher == nil {
		return nil
	}
	return &SemanticAssetSearch{searcher: searcher}
}

var _ scriptgen.SemanticAssetSearchPort = (*SemanticAssetSearch)(nil)

// Search executes one exact-source query.
func (s *SemanticAssetSearch) Search(ctx context.Context, req scriptgen.SemanticAssetSearchRequest) (scriptgen.SemanticAssetSearchResponse, error) {
	if s == nil || s.searcher == nil {
		return scriptgen.SemanticAssetSearchResponse{}, fmt.Errorf("semantic asset search: searcher is not configured")
	}
	entity := strings.TrimSpace(req.Entity)
	if entity == "" {
		entity = strings.TrimSpace(req.Query)
	}
	candidates, err := s.searcher.SearchImages(ctx, InternetImageSearchRequest{
		SegmentID:  strings.TrimSpace(req.CandidateID),
		Query:      req.Query,
		Entity:     entity,
		QueryClass: req.QueryClass,
		Language:   req.Language,
		Limit:      req.Limit,
		Provider:   semanticProductionProvider,
	})
	if err != nil {
		return scriptgen.SemanticAssetSearchResponse{}, err
	}
	response := scriptgen.SemanticAssetSearchResponse{
		Hits:     make([]scriptgen.SemanticAssetHit, 0, len(candidates)),
		Provider: semanticProductionProvider,
	}
	for _, candidate := range candidates {
		assetID := strings.TrimSpace(candidate.AssetID)
		if assetID == "" {
			assetID = strings.TrimSpace(candidate.ContentSHA256)
		}
		if assetID == "" {
			assetID = strings.TrimSpace(candidate.PreviewURL)
		}
		url := strings.TrimSpace(candidate.PreviewURL)
		if url == "" {
			url = strings.TrimSpace(candidate.SourceURL)
		}
		if assetID == "" && url == "" {
			continue
		}
		// The upstream provider is the provenance the plan cache invalidates
		// by. When a cascade returns several providers, the answer is attributed
		// to the first one that produced a hit, not to whichever candidate
		// happened to be appended last.
		provider := strings.TrimSpace(candidate.Provider)
		if provider != "" && response.Provider == semanticProductionProvider {
			response.Provider = provider
		}
		response.Hits = append(response.Hits, scriptgen.SemanticAssetHit{
			AssetID: assetID, URL: url, SHA256: strings.TrimSpace(candidate.ContentSHA256),
			DriveLink: strings.TrimSpace(candidate.DriveLink), MediaType: strings.TrimSpace(candidate.MIMEType),
			Provider: provider, Entity: strings.TrimSpace(candidate.Entity),
			Width: candidate.Width, Height: candidate.Height,
		})
	}
	return response, nil
}
