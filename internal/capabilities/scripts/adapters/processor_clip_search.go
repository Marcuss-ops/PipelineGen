package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// artlistSegmentCachePayload stores only the Artlist provider delta. Caching
// the entire segment would overwrite newer image/provider results on a hit.
type artlistSegmentCachePayload struct {
	Candidates []scriptpkg.SegmentAssetCandidate
	Matches    []ArtlistClipMatch
}

// ClipSearchProcessor searches Artlist per canonical VidRush segment.
type ClipSearchProcessor struct {
	searcher ArtlistClipSearcher
	metrics  VidRushMetrics
	cache    scriptports.VidRushCachePort
}

func NewClipSearchProcessor(searcher ArtlistClipSearcher, metrics ...VidRushMetrics) *ClipSearchProcessor {
	return NewClipSearchProcessorWithCache(searcher, nil, metrics...)
}

func NewClipSearchProcessorWithCache(searcher ArtlistClipSearcher, cache scriptports.VidRushCachePort, metrics ...VidRushMetrics) *ClipSearchProcessor {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &ClipSearchProcessor{searcher: searcher, metrics: m, cache: cache}
}

func (p *ClipSearchProcessor) Name() ProcessorName { return ProcessorClipSearch }

func (p *ClipSearchProcessor) Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	if vidRushArtlistOnlyPlan(plan) {
		return ProcessorRequired
	}
	return ProcessorBestEffort
}

func (p *ClipSearchProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
	cacheOnly := plan.MediaPlan.Mode == mediadomain.MediaPlanModeCacheOnly
	if !plan.MediaPlan.ProviderPolicy.Artlist.AsBool() {
		segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
		for _, segment := range input.VidRushSegments {
			cloned := cloneVidRushSegmentResult(segment)
			cloned.Cache.Artlist = "BYPASSED"
			segments = append(segments, cloned)
		}
		if len(segments) == 0 {
			return &PostProcessResult{}, nil
		}
		return &PostProcessResult{VidRushSegments: segments, Changed: true}, nil
	}
	if p.searcher == nil && !cacheOnly {
		if vidRushArtlistOnlyPlan(plan) {
			return nil, fmt.Errorf("clip_search: Artlist is required but the searcher is unavailable")
		}
		return &PostProcessResult{
			Changed:  true,
			Warnings: []string{"clip_search: ArtlistClipSearcher not configured"},
		}, nil
	}
	if len(input.VidRushSegments) == 0 {
		return &PostProcessResult{}, nil
	}
	segments := make([]scriptpkg.VidRushSegmentResult, 0, len(input.VidRushSegments))
	aggregated := make([]ArtlistClipMatch, 0)
	var warnings []string

	for _, seg := range input.VidRushSegments {
		updated := cloneVidRushSegmentResult(seg)
		if len(updated.Insights.ArtlistQueries) == 0 {
			updated.Cache.Artlist = "BYPASSED"
			segments = append(segments, updated)
			continue
		}

		cacheKey := artlistSegmentCacheKey(
			// Provider discovery is keyed by the canonical scene identity.
			// Explicit Artlist intent is stable across generated-prose retries;
			// untagged scenes retain their text-hash identity.
			updated.SegmentID,
			updated.TextHash,
			updated.Insights.ArtlistIntentHash,
			plan.Language,
			plan.Model,
			plan.PromptVersion,
		)
		// cache_only is an absolute no-provider contract. It must read a
		// previously materialized segment result even when a caller left a
		// force-refresh flag enabled; a miss is reported below and must never
		// fall through to Artlist.
		if cacheOnly || !plan.MediaPlan.ForceRefreshAssets {
			if cached, ok := cacheLoad(&vidrushArtlistCache, cacheKey); ok {
				if payload, ok := cached.(artlistSegmentCachePayload); ok {
					payload = cloneArtlistSegmentCachePayload(payload)
					updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, payload.Candidates)
					if len(payload.Candidates) > 0 && readyVidRushCandidate(payload.Candidates[0]) {
						primary := payload.Candidates[0]
						updated.Assets.PrimaryVideo = &primary
					}
					updated.Cache.Artlist = "HIT_EXACT"
					segments = append(segments, updated)
					aggregated = append(aggregated, payload.Matches...)
					if p.metrics != nil {
						p.metrics.IncAssetCache("artlist", true)
					}
					continue
				}
			}
			var persisted artlistSegmentCachePayload
			if hit, cacheErr := loadVidRushPersistentJSON(ctx, p.cache, "artlist", cacheKey, &persisted); cacheErr != nil {
				return nil, cacheErr
			} else if hit {
				persisted = cloneArtlistSegmentCachePayload(persisted)
				updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, persisted.Candidates)
				updated.Cache.Artlist = "HIT_EXACT"
				cacheStore(&vidrushArtlistCache, cacheKey, persisted)
				segments = append(segments, updated)
				aggregated = append(aggregated, persisted.Matches...)
				if p.metrics != nil {
					p.metrics.IncAssetCache("artlist", true)
				}
				continue
			}
		}
		if cacheOnly {
			updated.Cache.Artlist = "CACHE_MISS"
			warnings = append(warnings, fmt.Sprintf("clip_search: cache-only Artlist miss for segment %s", updated.SegmentID))
			segments = append(segments, updated)
			continue
		}

		if p.metrics != nil {
			p.metrics.IncAssetCache("artlist", false)
			p.metrics.IncProviderRequest("artlist")
		}

		segmentMatches := make([]ArtlistClipMatch, 0)
		// The planner may retain up to five ranked queries for diagnostics and
		// cache identity, but Artlist search is a serialized browser operation.
		// Use the two highest-priority queries for the live fan-out so one slow
		// tail query cannot consume the whole postprocessor budget.
		searchQueries := updated.Insights.ArtlistQueries
		if _, liveRegistrySearcher := p.searcher.(*VidRushRegistryClipSearcher); liveRegistrySearcher {
			// A provider-specific query can legitimately return no clips even
			// when the scene is searchable. Prefer one compact source-text query
			// first: the browser search is serialized and a slow narrative query
			// must not consume the entire provider timeout before the useful
			// fallback gets a chance to run.
			if fallback := compactLiveFallbackQuery(updated.Text); fallback != "" {
				ordered := []string{fallback}
				for _, query := range searchQueries {
					if !strings.EqualFold(strings.TrimSpace(query), fallback) {
						ordered = append(ordered, query)
					}
					if len(ordered) == 2 {
						break
					}
				}
				searchQueries = ordered
			} else if len(searchQueries) > 2 {
				searchQueries = searchQueries[:2]
			}
		}
		var matches []ArtlistClipMatch
		err := measureVidRushProvider(ctx, p.metrics, kernobs.OperationInfo{
			Stage: kernobs.StageAcquire, Component: "vidrush", Operation: "search", Provider: "artlist",
		}, func(callCtx context.Context) error {
			var searchErr error
			matches, searchErr = p.searcher.SearchClips(callCtx, plan.Title, searchQueries)
			return searchErr
		})
		segmentMatches = append(segmentMatches, matches...)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("clip_search: Artlist provider search failed for segment %s: %v", updated.SegmentID, err))
		}
		segmentMatches = dedupeArtlistMatches(segmentMatches)
		candidates := artlistMatchesToCandidates(updated, segmentMatches)

		if len(candidates) == 0 {
			updated.Cache.Artlist = "MISS"
			if plan.MediaPlan.ForceRefreshAssets {
				updated.Cache.Artlist = "REFRESHED"
			}
			segments = append(segments, updated)
			warnings = append(warnings, fmt.Sprintf("clip_search: no matching Artlist clips found for segment %s", updated.SegmentID))
			if vidRushArtlistOnlyPlan(plan) {
				return nil, fmt.Errorf("clip_search: required Artlist candidates missing for segment %s", updated.SegmentID)
			}
			// Do not cache provider misses without a TTL: a transient empty
			// result must remain discoverable on the next request.
			continue
		}

		updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
		if readyVidRushCandidate(candidates[0]) {
			primary := candidates[0]
			updated.Assets.PrimaryVideo = &primary
		}
		updated.Cache.Artlist = "MISS"
		if plan.MediaPlan.ForceRefreshAssets {
			updated.Cache.Artlist = "REFRESHED"
		}
		segments = append(segments, updated)
		aggregated = append(aggregated, segmentMatches...)
		cacheStore(&vidrushArtlistCache, cacheKey, artlistSegmentCachePayload{
			Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...),
			Matches:    cloneArtlistMatches(segmentMatches),
		})
		if cacheErr := storeVidRushPersistentJSON(ctx, p.cache, "artlist", cacheKey, artlistSegmentCachePayload{
			Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), candidates...),
			Matches:    cloneArtlistMatches(segmentMatches),
		}); cacheErr != nil {
			return nil, cacheErr
		}
	}

	return &PostProcessResult{
		VidRushSegments:        segments,
		ArtlistClipSuggestions: dedupeArtlistMatches(aggregated),
		Warnings:               warnings,
		Changed:                len(segments) > 0,
	}, nil
}

func compactLiveFallbackQuery(text string) string {
	text = strings.TrimSpace(text)
	if end := strings.IndexAny(text, ".!?\n"); end > 0 {
		text = text[:end]
	}
	tokens := textutil.TokenizeWithStopWords(text, linguistics.DefaultStopWords())
	if len(tokens) > 6 {
		tokens = tokens[:6]
	}
	if len(tokens) < 2 {
		return ""
	}
	return strings.Join(tokens, " ")
}

// artlistMatchesToCandidates expands every clip in every match. The previous
// implementation kept only the first name/link and silently dropped the rest.
func artlistMatchesToCandidates(seg scriptpkg.VidRushSegmentResult, matches []ArtlistClipMatch) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0)
	seen := make(map[string]struct{})
	rank := 0
	for _, match := range matches {
		count := len(match.ClipNames)
		if len(match.ClipDriveLinks) > count {
			count = len(match.ClipDriveLinks)
		}
		for i := 0; i < count; i++ {
			name := ""
			if i < len(match.ClipNames) {
				name = strings.TrimSpace(match.ClipNames[i])
			}
			link := ""
			if i < len(match.ClipDriveLinks) {
				link = strings.TrimSpace(match.ClipDriveLinks[i])
			}
			if link == "" {
				continue
			}
			identity := strings.ToLower(link)
			if _, ok := seen[identity]; ok {
				continue
			}
			seen[identity] = struct{}{}

			score := 1.0 - float64(rank)*0.02
			if score < 0.1 {
				score = 0.1
			}
			rank++
			assetID := segmentCacheKey(seg.SegmentID, match.Phrase, name, link)
			candidate := scriptpkg.SegmentAssetCandidate{
				AssetID:         "artlist-" + assetID[:12],
				Provider:        "artlist",
				Query:           strings.TrimSpace(match.Phrase),
				Entity:          name,
				Score:           score,
				SourceURL:       link,
				SourcePageURL:   strings.TrimSpace(match.FolderLink),
				PreviewURL:      link,
				RightsStatus:    "unknown",
				SelectionReason: "ranked Artlist clip matching a segment visual query",
			}
			if match.Remote {
				candidate.AcquisitionStatus = scriptpkg.VidRushStatusCandidateFound
				candidate.VerificationStatus = "pending"
				candidate.PersistenceStatus = "pending"
				candidate.IndexStatus = "pending"
			} else {
				// A remote discovery URL is not a durable Drive location.
				// Populate DriveLink only for legacy results that already
				// represent a persisted location.
				candidate.DriveLink = link
			}
			out = append(out, candidate)
		}
	}
	return out
}

func appendProviderCandidatesUnique(base, additions []scriptpkg.SegmentAssetCandidate) []scriptpkg.SegmentAssetCandidate {
	out := append([]scriptpkg.SegmentAssetCandidate(nil), base...)
	seen := make(map[string]struct{}, len(out)+len(additions))
	for _, candidate := range out {
		if key := vidRushCandidateIdentity(candidate); key != "" {
			seen[key] = struct{}{}
		}
	}
	for _, candidate := range additions {
		key := vidRushCandidateIdentity(candidate)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func vidRushCandidateIdentity(candidate scriptpkg.SegmentAssetCandidate) string {
	provider := strings.ToLower(strings.TrimSpace(candidate.Provider))
	for _, value := range []string{candidate.DriveLink, candidate.SourceURL, candidate.PreviewURL, candidate.AssetID} {
		if value = strings.TrimSpace(value); value != "" {
			return provider + "\x00" + strings.ToLower(value)
		}
	}
	return ""
}

func dedupeArtlistMatches(matches []ArtlistClipMatch) []ArtlistClipMatch {
	seen := make(map[string]struct{}, len(matches))
	out := make([]ArtlistClipMatch, 0, len(matches))
	for _, match := range matches {
		key := strings.Join([]string{
			strings.ToLower(strings.TrimSpace(match.Phrase)),
			strings.ToLower(strings.TrimSpace(match.FolderID)),
			strings.Join(match.ClipDriveLinks, "\x00"),
		}, "\x01")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cloneArtlistMatch(match))
	}
	return out
}

func cloneArtlistSegmentCachePayload(in artlistSegmentCachePayload) artlistSegmentCachePayload {
	return artlistSegmentCachePayload{
		Candidates: append([]scriptpkg.SegmentAssetCandidate(nil), in.Candidates...),
		Matches:    cloneArtlistMatches(in.Matches),
	}
}

func cloneArtlistMatches(in []ArtlistClipMatch) []ArtlistClipMatch {
	out := make([]ArtlistClipMatch, 0, len(in))
	for _, match := range in {
		out = append(out, cloneArtlistMatch(match))
	}
	return out
}

func cloneArtlistMatch(in ArtlistClipMatch) ArtlistClipMatch {
	out := in
	out.ClipNames = append([]string(nil), in.ClipNames...)
	out.ClipDriveLinks = append([]string(nil), in.ClipDriveLinks...)
	return out
}
