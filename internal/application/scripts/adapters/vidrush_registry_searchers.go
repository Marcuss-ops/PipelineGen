package adapters

import (
	"context"
	"fmt"
	"strings"
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
