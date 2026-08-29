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
// VidRush provider port. Caller-supplied source hints win; when none are
// supplied the provider falls back to autonomous discovery through the
// VideoSourceDiscovery port (no URLs are invented here — discovery returns
// candidates, StockService plans windows, VidRush ranks winners).
type VidRushYouTubeProvider struct {
	Stock     *stockplan.StockService
	Discovery scriptports.VideoSourceDiscovery
}

func NewVidRushYouTubeProvider(stock *stockplan.StockService, discovery scriptports.VideoSourceDiscovery) (*VidRushYouTubeProvider, error) {
	if stock == nil {
		return nil, fmt.Errorf("youtube VidRush provider: stock service is required")
	}
	// discovery is optional: nil keeps the legacy source-hints-only behavior
	// (Search fails with ErrNoYouTubeCandidates when no hints are supplied).
	return &VidRushYouTubeProvider{Stock: stock, Discovery: discovery}, nil
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
//
// Source semantics:
//   - required hint: that exact video must be used; failure is terminal
//     (a broken required URL never silently falls back to another video)
//   - suggested hints: tried first; if planning yields nothing usable the
//     provider falls back to autonomous discovery
//   - no hints: fully autonomous discovery from the segment query
func (p *VidRushYouTubeProvider) Search(ctx context.Context, req scriptports.VidRushSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if p == nil || p.Stock == nil {
		return nil, fmt.Errorf("youtube VidRush provider: stock service is unavailable")
	}
	hints := requiredFirst(req.Sources)
	if hasRequiredHint(hints) {
		// Required path: only required URLs are eligible. Any planning error,
		// empty result, malformed URL or unavailable transcript is terminal;
		// discovery and other source hints must never mask the failure.
		return p.planRequiredWindows(ctx, req, requiredHintURLs(hints))
	}
	suggested := optionalHintURLs(hints)
	if len(suggested) > 0 {
		candidates, err := p.planWindows(ctx, req, suggested)
		if err == nil && len(candidates) > 0 {
			return candidates, nil
		}
		// Suggested URLs produced nothing usable: fall through to discovery.
	}
	urls, err := p.discoverURLs(ctx, req)
	if err != nil {
		if len(suggested) > 0 {
			return nil, fmt.Errorf("youtube VidRush provider: suggested sources failed and discovery failed for segment %q: %w", req.SegmentID, err)
		}
		return nil, err
	}
	if len(urls) == 0 {
		return nil, fmt.Errorf("youtube VidRush provider: no youtube candidates for segment %q: %w", req.SegmentID, scriptports.ErrNoDiscoveryCandidates)
	}
	// Discovered URLs are intentionally sent through Plan only: metadata,
	// transcripts and semantic windows are evaluated without downloading or
	// persisting media. MaterializeSelected remains the sole download path.
	return p.planWindows(ctx, req, urls)
}

// planWindows runs the transcript-first StockService.Plan over the given
// URLs and maps the selected windows to VidRush candidates.
func (p *VidRushYouTubeProvider) planWindows(ctx context.Context, req scriptports.VidRushSearchRequest, urls []string) ([]scriptpkg.SegmentAssetCandidate, error) {
	planned, err := p.Stock.Plan(ctx, stockplan.YouTubeStockRequest{
		Subject:        req.SceneID,
		YouTubeURLs:    urls,
		Query:          req.Query,
		ClipsPerVideo:  1,
		ClipDurationMs: normalizeClipDurationMs(req),
	})
	if err != nil {
		return nil, err
	}
	candidates := selectedSegmentsToCandidates(planned.SelectedSegments, req.Query)
	for i := range candidates {
		candidates[i].SemanticStatus = "planned_transcript"
	}
	return preRankYouTubeCandidates(candidates, req.Query, req.SceneID, youtubeCandidateLimit(req)), nil
}

// discoverURLs runs autonomous discovery and returns canonical watch URLs.
func (p *VidRushYouTubeProvider) discoverURLs(ctx context.Context, req scriptports.VidRushSearchRequest) ([]string, error) {
	if p.Discovery == nil {
		return nil, fmt.Errorf("youtube VidRush provider: no source hints for segment %q and no discovery configured", req.SegmentID)
	}
	maxVideos := 12
	candidates, err := p.Discovery.Discover(ctx, scriptports.VideoSourceDiscoveryRequest{
		SegmentID:          req.SegmentID,
		Queries:            buildYouTubeQueryPlan(req).Phrases(),
		Language:           "en",
		MaxVideos:          maxVideos,
		MinVideoDurationMs: normalizeClipDurationMs(req),
		ExcludeLive:        true,
	})
	if err != nil {
		return nil, err
	}
	urls := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			continue
		}
		url := strings.TrimSpace(candidate.URL)
		if url == "" {
			continue
		}
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		urls = append(urls, url)
		if len(urls) >= maxVideos {
			break
		}
	}
	return urls, nil
}

// buildYouTubeQueryPlan translates one segment semantic profile into the
// per-provider YouTube query plan: 3-5 focused phrases with intent and
// weight, deduplicated, most specific first. Deterministic translation
// only — the small model already produced the understanding; this function
// never invents content and never ranks videos.
//
// Intent ladder (most to least specific):
//
//	exact_subject       — caller query / important phrases / entity-led phrase
//	historical_context  — temporal + place context layered onto the subject
//	visual_fallback     — profile visual terms, for when subject queries miss
func buildYouTubeQueryPlan(req scriptports.VidRushSearchRequest) scriptports.ProviderQueryPlan {
	profile := req.SemanticProfile
	if profile != nil {
		queries := youtubeProfileQueries(*profile, req.Query)
		if len(queries) > 0 {
			plan := scriptports.ProviderQueryPlan{Provider: scriptpkg.VidRushProviderYouTube}
			for i, query := range queries {
				weight := 1.0 - float64(i)*0.1
				if weight < 0.1 {
					weight = 0.1
				}
				intent := scriptports.QueryIntentExactSubject
				if i > 0 {
					intent = scriptports.QueryIntentVisualFallback
				}
				plan.Queries = append(plan.Queries, scriptports.ProviderQuery{Query: query, Intent: intent, Weight: weight})
			}
			return plan
		}
	}
	exact := make([]string, 0, 2)
	contextual := make([]string, 0, 2)
	visual := make([]string, 0, 3)

	if trimmed := strings.TrimSpace(req.Query); trimmed != "" {
		exact = append(exact, trimmed)
	}
	if profile != nil {
		// Entity-led exact phrases: PERSON/PLACE + subject beats one long
		// concatenated query on YouTube search.
		entities := make([]string, 0, len(profile.Entities))
		for _, entity := range profile.Entities {
			if value := strings.TrimSpace(entity.Value); value != "" {
				entities = append(entities, value)
			}
		}
		if len(exact) == 0 && len(entities) > 0 {
			exact = append(exact, strings.Join(entities[:min(len(entities), 2)], " "))
		}
		for _, phrase := range profile.ImportantPhrases {
			if trimmed := strings.TrimSpace(phrase); trimmed != "" {
				contextual = append(contextual, trimmed)
			}
		}
		// Temporal/place context phrase: entity values joined with the topic.
		if profile.Topic != "" && len(entities) > 0 {
			contextual = append(contextual, strings.Join(entities, " ")+" "+profile.Topic)
		}
		for _, term := range profile.VisualTerms {
			if trimmed := strings.TrimSpace(term.Value); trimmed != "" {
				visual = append(visual, trimmed)
			}
		}
	}
	if len(exact)+len(contextual)+len(visual) == 0 && strings.TrimSpace(req.Text) != "" {
		exact = append(exact, strings.TrimSpace(req.Text))
	}

	// Deterministic weights by intent rung, decayed within each rung by
	// emission order. Non-increasing order by construction.
	plan := scriptports.ProviderQueryPlan{Provider: scriptpkg.VidRushProviderYouTube}
	appendRung(&plan.Queries, scriptports.QueryIntentExactSubject, exact, 1.0)
	appendRung(&plan.Queries, scriptports.QueryIntentHistoricalContext, contextual, 0.9)
	appendRung(&plan.Queries, scriptports.QueryIntentVisualFallback, visual, 0.72)
	plan.Queries = dedupeLimitedQueries(plan.Queries, 5)
	return plan
}

// appendRung adds one intent rung's phrases with decaying weights.
func youtubeProfileQueries(profile scriptpkg.SegmentSemanticProfile, explicit string) []string {
	values := make([]string, 0, 1+len(profile.VisualTerms))
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		values = append(values, explicit)
	}
	for _, term := range profile.VisualTerms {
		values = append(values, strings.TrimSpace(term.Value))
	}
	return normalizedProviderQueries(values, 8)
}

func appendRung(out *[]scriptports.ProviderQuery, intent scriptports.QueryIntent, phrases []string, top float64) {
	for i, phrase := range phrases {
		weight := top - float64(i)*0.1
		if weight <= 0 {
			weight = 0.1
		}
		*out = append(*out, scriptports.ProviderQuery{Query: phrase, Intent: intent, Weight: weight})
	}
}

// dedupeLimitedQueries keeps the first occurrence of each phrase (trimmed),
// caps the plan at limit entries, and preserves the non-increasing weight
// order the rungs established.
func dedupeLimitedQueries(queries []scriptports.ProviderQuery, limit int) []scriptports.ProviderQuery {
	out := make([]scriptports.ProviderQuery, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, query := range queries {
		key := strings.TrimSpace(query.Query)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, query)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// requiredFirst orders source hints so required ones lead.
func requiredFirst(sources []scriptports.VidRushSourceHint) []scriptports.VidRushSourceHint {
	out := make([]scriptports.VidRushSourceHint, 0, len(sources))
	for _, source := range sources {
		if source.Required {
			out = append(out, source)
		}
	}
	for _, source := range sources {
		if !source.Required {
			out = append(out, source)
		}
	}
	return out
}

func requiredHintURLs(sources []scriptports.VidRushSourceHint) []string {
	urls := make([]string, 0, len(sources))
	for _, source := range sources {
		if source.Required {
			if url := strings.TrimSpace(source.URL); url != "" {
				urls = append(urls, url)
			}
		}
	}
	return urls
}

func optionalHintURLs(sources []scriptports.VidRushSourceHint) []string {
	urls := make([]string, 0, len(sources))
	for _, source := range sources {
		if !source.Required {
			if url := strings.TrimSpace(source.URL); url != "" {
				urls = append(urls, url)
			}
		}
	}
	return urls
}

func (p *VidRushYouTubeProvider) planRequiredWindows(ctx context.Context, req scriptports.VidRushSearchRequest, urls []string) ([]scriptpkg.SegmentAssetCandidate, error) {
	if len(urls) == 0 {
		return nil, fmt.Errorf("youtube VidRush provider: required source URL is missing")
	}
	candidates, err := p.planWindows(ctx, req, urls)
	if err != nil {
		return nil, fmt.Errorf("youtube VidRush provider: required source planning failed: %w", err)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("youtube VidRush provider: required source produced no usable window")
	}
	return candidates, nil
}

func hasRequiredHint(sources []scriptports.VidRushSourceHint) bool {
	for _, source := range sources {
		if source.Required && strings.TrimSpace(source.URL) != "" {
			return true
		}
	}
	return false
}

func candidateURLs(candidates []scriptpkg.SegmentAssetCandidate) []string {
	urls := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if url := strings.TrimSpace(candidate.SourceURL); url != "" {
			urls = append(urls, url)
		}
	}
	return urls
}

func hintURLs(sources []scriptports.VidRushSourceHint) []string {
	urls := make([]string, 0, len(sources))
	for _, source := range sources {
		if trimmed := strings.TrimSpace(source.URL); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	return urls
}

// MaterializeSelected delegates selected YouTube windows to the canonical
// extractor. The returned candidates carry the persisted asset identity.
func (p *VidRushYouTubeProvider) MaterializeSelected(ctx context.Context, req scriptports.VidRushSearchRequest, selected []scriptpkg.SegmentAssetCandidate) ([]scriptpkg.SegmentAssetCandidate, error) {
	if p == nil || p.Stock == nil {
		return nil, fmt.Errorf("youtube VidRush provider: stock service is unavailable")
	}
	// Materialize exactly the selected winner. Callers may pass diagnostic or
	// ranked alternatives, but only the first candidate is eligible for the
	// canonical extractor. A persisted winner is returned immediately and is
	// never downloaded again.
	if len(selected) == 0 {
		return []scriptpkg.SegmentAssetCandidate{}, nil
	}
	// The caller must provide candidates already ordered by the deterministic
	// winner selector; only the first candidate is ever materialized.
	selected = dedupeYouTubeCandidatesByCacheKey(selected[:1])
	urls := hintURLs(req.Sources)
	if len(urls) == 0 {
		urls = candidateURLs(selected)
	}
	if allYouTubeCandidatesPersisted(selected) {
		return append([]scriptpkg.SegmentAssetCandidate(nil), selected...), nil
	}
	plannedSegments := candidatesToSelectedSegments(selected)
	if len(plannedSegments) == 0 {
		return nil, fmt.Errorf("youtube VidRush provider: selected winner has no valid source window")
	}
	planned := &stockplan.YouTubeStockResult{SelectedSegments: plannedSegments}
	result, err := p.Stock.Materialize(ctx, stockplan.YouTubeStockRequest{
		Subject:        req.SceneID,
		YouTubeURLs:    urls,
		Query:          req.Query,
		ClipsPerVideo:  1,
		ClipDurationMs: normalizeClipDurationMs(req),
	}, planned)
	if err != nil {
		return nil, err
	}
	materialized := selectedSegmentsToCandidates(result.SelectedSegments, req.Query)
	if len(materialized) > 1 {
		materialized = materialized[:1]
	}
	return materialized, nil
}

// normalizeClipDurationMs resolves the clip length from the request's
// timing budget instead of a magic number at each call site. The request
// budget wins; Min/Max bound it; defaultClipMs applies only when the
// caller sent no budget.
const defaultClipMs = 8000

func normalizeClipDurationMs(req scriptports.VidRushSearchRequest) int64 {
	// Timing precedence is explicit: voiceover-derived TargetDurationMs,
	// then scene timing, then an estimated segment budget, and finally the
	// provider safety default for legacy callers.
	duration := req.TargetDurationMs
	if duration <= 0 {
		duration = req.SceneDurationMs
	}
	if duration <= 0 {
		duration = req.EstimatedDurationMs
	}
	if duration <= 0 {
		duration = defaultClipMs
	}
	if req.MinDurationMs > 0 && duration < req.MinDurationMs {
		duration = req.MinDurationMs
	}
	if req.MaxDurationMs > 0 && duration > req.MaxDurationMs {
		duration = req.MaxDurationMs
	}
	return duration
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
