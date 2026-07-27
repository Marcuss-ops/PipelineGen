package adapters

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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
}

func NewClipSearchProcessor(searcher ArtlistClipSearcher, metrics ...VidRushMetrics) *ClipSearchProcessor {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &ClipSearchProcessor{searcher: searcher, metrics: m}
}

func (p *ClipSearchProcessor) Name() ProcessorName { return ProcessorClipSearch }

func (p *ClipSearchProcessor) Policy(_ *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	return ProcessorBestEffort
}

func (p *ClipSearchProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil {
		return &PostProcessResult{}, nil
	}
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
	if p.searcher == nil {
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

		cacheKey := segmentCacheKey(
			"artlist-assets-v2",
			updated.SegmentID,
			updated.TextHash,
			plan.Language,
			plan.Model,
			plan.PromptVersion,
			strings.Join(updated.Insights.ArtlistQueries, "\u0000"),
		)
		if !plan.MediaPlan.ForceRefreshAssets {
			if cached, ok := cacheLoad(&vidrushArtlistCache, cacheKey); ok {
				if payload, ok := cached.(artlistSegmentCachePayload); ok {
					payload = cloneArtlistSegmentCachePayload(payload)
					updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, payload.Candidates)
					if len(payload.Candidates) > 0 {
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
		}

		if p.metrics != nil {
			p.metrics.IncAssetCache("artlist", false)
			p.metrics.IncProviderRequest("artlist")
		}

		segmentMatches := make([]ArtlistClipMatch, 0)
		for _, query := range updated.Insights.ArtlistQueries {
			segmentMatches = append(segmentMatches, p.searcher.SearchClips(ctx, plan.Title, []string{query})...)
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
			cacheStore(&vidrushArtlistCache, cacheKey, artlistSegmentCachePayload{Matches: cloneArtlistMatches(segmentMatches)})
			continue
		}

		updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, candidates)
		primary := candidates[0]
		updated.Assets.PrimaryVideo = &primary
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
	}

	return &PostProcessResult{
		VidRushSegments:        segments,
		ArtlistClipSuggestions: dedupeArtlistMatches(aggregated),
		Warnings:               warnings,
		Changed:                len(segments) > 0,
	}, nil
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
			out = append(out, scriptpkg.SegmentAssetCandidate{
				AssetID:         "artlist-" + assetID[:12],
				Provider:        "artlist",
				Query:           strings.TrimSpace(match.Phrase),
				Entity:          name,
				Score:           score,
				SourceURL:       link,
				SourcePageURL:   strings.TrimSpace(match.FolderLink),
				PreviewURL:      link,
				DriveLink:       link,
				RightsStatus:    "unknown",
				SelectionReason: "ranked Artlist clip matching a segment visual query",
			})
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
	for _, value := range []string{candidate.AssetID, candidate.DriveLink, candidate.SourceURL, candidate.PreviewURL} {
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
