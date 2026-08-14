package adapters

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Artlist applies a provider-side rate limit independently of the local
// worker budget. Keep one live browser search in flight across all VidRush
// jobs and use bounded retries for 429 responses; otherwise waves of jobs can
// turn the configured worker count into a provider outage.
var vidRushArtlistSearchGate = make(chan struct{}, 1)

func acquireVidRushArtlistSearch(ctx context.Context) error {
	select {
	case vidRushArtlistSearchGate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseVidRushArtlistSearch() { <-vidRushArtlistSearchGate }

func isArtlistRateLimited(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "429")
}

// VidRushRegistryClipSearcher adapts the shared VidRush provider registry to
// the legacy clip-search result shape. It keeps discovery on the same
// provider path as acquisition; the materializer still owns persistence.
type VidRushRegistryClipSearcher struct {
	Registry *VidRushAssetProviderRegistry
}

func (s *VidRushRegistryClipSearcher) SearchClips(ctx context.Context, title string, phrases []string) ([]ArtlistClipMatch, error) {
	if s == nil || s.Registry == nil {
		return nil, scriptports.ErrVidRushProviderNotFound
	}
	type queryResult struct {
		match ArtlistClipMatch
		err   error
	}
	results, mapErr := concurrent.Map(ctx, phrases, 3, func(ctx context.Context, _ int, rawPhrase string) (queryResult, error) {
		phrase := strings.TrimSpace(rawPhrase)
		if phrase == "" {
			return queryResult{}, nil
		}
		if err := acquireVidRushArtlistSearch(ctx); err != nil {
			return queryResult{err: fmt.Errorf("artlist query %q: acquire search slot: %w", phrase, err)}, nil
		}
		defer releaseVidRushArtlistSearch()

		var candidates []scriptpkg.SegmentAssetCandidate
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			queryCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			candidates, err = s.Registry.Search(queryCtx, scriptpkg.VidRushProviderArtlist, scriptports.VidRushSearchRequest{SceneID: title, Text: title, Query: phrase, Limit: 10})
			cancel()
			if !isArtlistRateLimited(err) || attempt == 2 {
				break
			}
			backoff := time.Duration(1<<attempt) * time.Second
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return queryResult{err: fmt.Errorf("artlist query %q: retry canceled: %w", phrase, ctx.Err())}, nil
			}
		}
		if err != nil {
			return queryResult{err: fmt.Errorf("artlist query %q: %w", phrase, err)}, nil
		}
		match := ArtlistClipMatch{Phrase: phrase, Remote: true}
		for _, candidate := range candidates {
			link := strings.TrimSpace(candidate.SourceURL)
			if link == "" {
				link = strings.TrimSpace(candidate.PreviewURL)
			}
			if link == "" {
				continue
			}
			match.ClipNames = append(match.ClipNames, candidate.AssetID)
			match.ClipDriveLinks = append(match.ClipDriveLinks, link)
			if match.FolderLink == "" {
				match.FolderLink = candidate.SourcePageURL
			}
		}
		return queryResult{match: match}, nil
	})
	if mapErr != nil {
		return nil, mapErr
	}
	out := make([]ArtlistClipMatch, 0, len(results))
	var firstErr error
	for _, result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		if len(result.match.ClipDriveLinks) > 0 {
			out = append(out, result.match)
		}
	}
	return out, firstErr
}

// VidRushRegistryImageSearcher adapts the shared registry to the image
// discovery port used by InternetImagesProcessor.
type VidRushRegistryImageSearcher struct {
	Registry *VidRushAssetProviderRegistry
}

func (s *VidRushRegistryImageSearcher) SearchImages(ctx context.Context, req InternetImageSearchRequest) ([]scriptpkg.SegmentAssetCandidate, error) {
	if s == nil || s.Registry == nil {
		return nil, scriptports.ErrVidRushProviderNotFound
	}
	return s.Registry.Search(ctx, scriptpkg.VidRushProviderInternetImages, scriptports.VidRushSearchRequest{
		SegmentID: req.SegmentID, TextHash: req.TextHash, Text: req.Query, Query: req.Query, Limit: req.Limit,
	})
}

// VidRushProviderFanout resolves a single enriched segment's visual providers
// in parallel through the shared searcher ports (which dispatch the canonical
// provider registry). It owns only the concurrency and the candidate merge; the
// provider-specific search, rate limiting and retry stay in the searchers, so
// no provider orchestration is duplicated for the incremental path.
type VidRushProviderFanout struct {
	artlist ArtlistClipSearcher
	images  InternetImageSearcher
	metrics VidRushMetrics
}

func NewVidRushProviderFanout(artlist ArtlistClipSearcher, images InternetImageSearcher, metrics ...VidRushMetrics) *VidRushProviderFanout {
	var m VidRushMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &VidRushProviderFanout{artlist: artlist, images: images, metrics: m}
}

// ResolveProviders runs Artlist and internet-image discovery concurrently for
// one segment and merges the winning candidates back into an immutable result.
// Required providers fail closed; best-effort providers never turn an
// unavailable backend into a silent successful empty result (the searchers
// already return typed errors, and their absence is reflected in the result).
func (f *VidRushProviderFanout) ResolveProviders(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, segment scriptpkg.VidRushSegmentResult) (scriptpkg.VidRushSegmentResult, error) {
	updated := cloneVidRushSegmentResult(segment)
	if plan == nil {
		return updated, nil
	}
	perQueryLimit := 10
	if plan.MediaPlan.Planner.CandidateLimit > 0 {
		perQueryLimit = plan.MediaPlan.Planner.CandidateLimit
	}
	if perQueryLimit > 50 {
		perQueryLimit = 50
	}

	type providerOutcome struct {
		provider   string
		candidates []scriptpkg.SegmentAssetCandidate
		primary    *scriptpkg.SegmentAssetCandidate
		err        error
	}

	artlistEnabled := plan.MediaPlan.ProviderPolicy.Artlist.AsBool() && f.artlist != nil && len(updated.Insights.ArtlistQueries) > 0
	imagesEnabled := plan.MediaPlan.ProviderPolicy.InternetImages.AsBool() && f.images != nil && len(updated.Insights.ImageQueries) > 0

	outcomes := make(chan providerOutcome, 2)
	var wg sync.WaitGroup
	if artlistEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if f.metrics != nil {
				f.metrics.IncProviderRequest("artlist")
			}
			matches, err := f.artlist.SearchClips(ctx, plan.Title, updated.Insights.ArtlistQueries)
			if err != nil {
				if f.metrics != nil {
					f.metrics.IncProviderFailure("artlist")
				}
				outcomes <- providerOutcome{provider: "artlist", err: err}
				return
			}
			candidates := artlistMatchesToCandidates(updated, dedupeArtlistMatches(matches))
			var primary *scriptpkg.SegmentAssetCandidate
			if len(candidates) > 0 && readyVidRushCandidate(candidates[0]) {
				selected := candidates[0]
				primary = &selected
			}
			outcomes <- providerOutcome{provider: "artlist", candidates: candidates, primary: primary}
		}()
	}
	if imagesEnabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			firstEntity := ""
			if len(updated.Insights.Entities) > 0 {
				firstEntity = strings.TrimSpace(updated.Insights.Entities[0].Value)
			}
			candidates := make([]scriptpkg.SegmentAssetCandidate, 0)
			seen := make(map[string]struct{})
			for _, query := range updated.Insights.ImageQueries {
				if f.metrics != nil {
					f.metrics.IncProviderRequest("internet_images")
				}
				results, err := f.images.SearchImages(ctx, InternetImageSearchRequest{
					SegmentID: updated.SegmentID, Query: query, Entity: firstEntity,
					TextHash: updated.TextHash, Language: plan.Language, Limit: perQueryLimit,
					Provider: "internet_images",
				})
				if err != nil {
					if f.metrics != nil {
						f.metrics.IncProviderFailure("internet_images")
					}
					outcomes <- providerOutcome{provider: "internet_images", err: err}
					return
				}
				for _, cand := range results {
					if strings.TrimSpace(cand.Provider) == "" {
						cand.Provider = "internet_images"
					}
					if strings.TrimSpace(cand.Query) == "" {
						cand.Query = query
					}
					if strings.ToLower(strings.TrimSpace(cand.Provider)) != "internet_images" {
						continue
					}
					key := vidRushCandidateIdentity(cand)
					if key == "" {
						continue
					}
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					candidates = append(candidates, cand)
				}
			}
			outcomes <- providerOutcome{provider: "internet_images", candidates: candidates}
		}()
	}
	go func() { wg.Wait(); close(outcomes) }()

	for outcome := range outcomes {
		if outcome.err != nil {
			if outcome.provider == scriptpkg.VidRushProviderArtlist && vidRushArtlistOnlyPlan(plan) {
				return updated, fmt.Errorf("vidrush provider fanout: required artlist search failed for segment %s: %w", updated.SegmentID, outcome.err)
			}
			continue
		}
		updated.Assets.Candidates = appendProviderCandidatesUnique(updated.Assets.Candidates, outcome.candidates)
		switch outcome.provider {
		case scriptpkg.VidRushProviderArtlist:
			updated.Assets.PrimaryVideo = outcome.primary
			updated.Cache.Artlist = "MISS"
		case scriptpkg.VidRushProviderInternetImages:
			updated.Assets.SecondaryImages = appendProviderCandidatesUnique(updated.Assets.SecondaryImages, outcome.candidates)
			updated.Cache.InternetImages = "MISS"
		}
	}
	return updated, nil
}
