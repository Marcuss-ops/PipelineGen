package adapters

import (
	"context"
	"fmt"
	"strings"

	stockplan "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VidRushYouTubeProvider adapts the canonical YouTube StockService to the
// VidRush provider port. Source hints are bounded to the requesting segment;
// no web search is performed here.
type VidRushYouTubeProvider struct {
	Stock *stockplan.StockService
}

func NewVidRushYouTubeProvider(stock *stockplan.StockService) (*VidRushYouTubeProvider, error) {
	if stock == nil {
		return nil, fmt.Errorf("youtube VidRush provider: stock service is required")
	}
	return &VidRushYouTubeProvider{Stock: stock}, nil
}

func (p *VidRushYouTubeProvider) Name() string { return scriptpkg.VidRushProviderYouTube }

// Acquire is intentionally deferred to MaterializeSelected because YouTube
// selection must remain comparable with other providers before download.
func (p *VidRushYouTubeProvider) Acquire(context.Context, scriptpkg.SegmentAssetCandidate) (scriptports.LocalArtifact, error) {
	return scriptports.LocalArtifact{}, fmt.Errorf("youtube VidRush provider: acquire requires selected-window materialization")
}

func (p *VidRushYouTubeProvider) Verify(context.Context, scriptports.LocalArtifact) (scriptports.VerifiedArtifact, error) {
	return scriptports.VerifiedArtifact{}, fmt.Errorf("youtube VidRush provider: verification is owned by the canonical extractor")
}

// Search plans transcript windows only. Materialization is intentionally
// separate so VidRush can rank YouTube candidates against other providers.
func (p *VidRushYouTubeProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if p == nil || p.Stock == nil {
		return nil, fmt.Errorf("youtube VidRush provider: stock service is unavailable")
	}
	urls := make([]string, 0, len(req.Sources))
	for _, source := range req.Sources {
		if strings.TrimSpace(source.URL) != "" {
			urls = append(urls, strings.TrimSpace(source.URL))
		}
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("youtube VidRush provider: no source hints for segment %q", req.SegmentID)
	}
	planned, err := p.Stock.Plan(ctx, stockplan.YouTubeStockRequest{
		Subject:        req.SceneID,
		YouTubeURLs:    urls,
		Query:          req.Query,
		ClipsPerVideo:  1,
		ClipDurationMs: 10000,
	})
	if err != nil {
		return nil, err
	}
	return selectedSegmentsToCandidates(planned.SelectedSegments, req.Query), nil
}

// MaterializeSelected delegates selected YouTube windows to the canonical
// extractor. The returned candidates carry the persisted asset identity.
func (p *VidRushYouTubeProvider) MaterializeSelected(ctx context.Context, req scriptports.VidRushSearchRequest, selected []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if p == nil || p.Stock == nil {
		return nil, fmt.Errorf("youtube VidRush provider: stock service is unavailable")
	}
	urls := make([]string, 0, len(req.Sources))
	for _, source := range req.Sources {
		if strings.TrimSpace(source.URL) != "" {
			urls = append(urls, strings.TrimSpace(source.URL))
		}
	}
	selected = dedupeYouTubeCandidatesByCacheKey(selected)
	plannedSegments := candidatesToSelectedSegments(selected)
	if allYouTubeCandidatesPersisted(selected) {
		return append([]scriptpkg.SegmentAssetCandidate(nil), selected...), nil
	}
	planned := &stockplan.YouTubeStockResult{SelectedSegments: plannedSegments}
	result, err := p.Stock.Materialize(ctx, stockplan.YouTubeStockRequest{
		Subject:        req.SceneID,
		YouTubeURLs:    urls,
		Query:          req.Query,
		ClipsPerVideo:  1,
		ClipDurationMs: 10000,
	}, planned)
	if err != nil {
		return nil, err
	}
	return selectedSegmentsToCandidates(result.SelectedSegments, req.Query), nil
}

func selectedSegmentsToCandidates(segments []stockplan.SelectedSegment, query string) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(segments))
	for _, segment := range segments {
		out = append(out, scriptpkg.SegmentAssetCandidate{
			AssetID: segment.AssetID, Provider: scriptpkg.VidRushProviderYouTube,
			Query: query, SourceURL: segment.SourceURL,
			SourceStartMs: segment.StartMs, SourceEndMs: segment.EndMs,
			DurationMs: segment.DurationMs,
			DriveLink:  segment.DriveLink, LocalPath: segment.LocalPath,
			Score: segment.RelevanceScore, RelevanceScore: segment.RelevanceScore,
			SelectionReason:    segment.SelectionReason,
			AcquisitionStatus:  acquisitionStatus(segment.Status),
			VerificationStatus: verificationStatus(segment.Status),
			PersistenceStatus:  persistenceStatus(segment.Status, segment.AssetID),
			IndexStatus:        indexStatus(segment.Status),
		})
	}
	return out
}

func dedupeYouTubeCandidatesByCacheKey(candidates []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		key := candidate.Provider + "\x00" + candidate.SourceURL + "\x00" + fmt.Sprint(candidate.SourceStartMs) + "\x00" + fmt.Sprint(candidate.SourceEndMs)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func allYouTubeCandidatesPersisted(candidates []scriptpkg.SegmentAssetCandidate) bool {
	if len(candidates) == 0 {
		return false
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.AssetID) == "" || strings.TrimSpace(candidate.DriveLink) == "" || candidate.PersistenceStatus != "persisted" {
			return false
		}
	}
	return true
}

func candidatesToSelectedSegments(candidates []scriptpkg.SegmentAssetCandidate) []stockplan.SelectedSegment {
	out := make([]stockplan.SelectedSegment, 0, len(candidates))
	for _, candidate := range candidates {
		video, err := stockplan.ParseYouTubeURL(candidate.SourceURL)
		if err != nil {
			continue
		}
		cacheKey := candidateCacheKey(candidate, video.ID)
		status := "SEGMENTS_PLANNED"
		if strings.TrimSpace(candidate.AssetID) != "" && strings.TrimSpace(candidate.DriveLink) != "" && candidate.PersistenceStatus == "persisted" {
			status = "persisted"
		}
		out = append(out, stockplan.SelectedSegment{
			YouTubeVideoID: video.ID, SourceURL: video.URL,
			StartMs: candidate.SourceStartMs, EndMs: candidate.SourceEndMs,
			DurationMs: candidate.DurationMs, RelevanceScore: candidate.RelevanceScore,
			SelectionReason: candidate.SelectionReason, SelectionBasis: "transcript",
			CacheKey: cacheKey, LocalPath: candidate.LocalPath, AssetID: candidate.AssetID,
			LegacyFileMD5: candidate.LegacyFileMD5, DriveLink: candidate.DriveLink, Status: status,
		})
	}
	return out
}

func candidateCacheKey(candidate scriptpkg.SegmentAssetCandidate, videoID string) string {
	return stockplan.PartialDownloadPlan{
		VideoID: videoID, StartMs: candidate.SourceStartMs, EndMs: candidate.SourceEndMs,
		DurationMs: candidate.DurationMs, ProfileVersion: "youtube-stock-v1",
	}.CacheKey()
}

func statusOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func acquisitionStatus(status string) string {
	if strings.EqualFold(status, "SEGMENTS_PLANNED") || strings.TrimSpace(status) == "" {
		return "planned"
	}
	return "acquired"
}

func verificationStatus(status string) string {
	if strings.Contains(strings.ToLower(status), "verif") || strings.EqualFold(status, "processed") || strings.EqualFold(status, "persisted") {
		return "verified"
	}
	return "pending"
}

func persistenceStatus(status, assetID string) string {
	if strings.TrimSpace(assetID) != "" || strings.Contains(strings.ToLower(status), "persist") || strings.EqualFold(status, "processed") {
		return "persisted"
	}
	return "pending"
}

func indexStatus(status string) string {
	if strings.Contains(strings.ToLower(status), "index") {
		return "indexed"
	}
	return "pending"
}

var _ scriptports.VidRushAssetProvider = (*VidRushYouTubeProvider)(nil)
