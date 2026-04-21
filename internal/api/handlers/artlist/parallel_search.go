package artlistpipeline

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// ParallelSearcher searches Artlist with multiple queries in parallel and deduplicates results.
type ParallelSearcher struct {
	artlistSrc *clip.ArtlistSource
	artlistDB  *artlistdb.ArtlistDB
	maxResults int
}

// NewParallelSearcher creates a new parallel searcher.
func NewParallelSearcher(artlistSrc *clip.ArtlistSource, artlistDB *artlistdb.ArtlistDB, maxResults int) *ParallelSearcher {
	return &ParallelSearcher{
		artlistSrc: artlistSrc,
		artlistDB:  artlistDB,
		maxResults: maxResults,
	}
}

// SearchWithExpandedQueries runs multiple Artlist searches in parallel and deduplicates results.
// Returns top N clips ranked by relevance.
func (ps *ParallelSearcher) SearchWithExpandedQueries(ctx context.Context, queries []string, topN int) ([]artlistdb.ArtlistClip, error) {
	if len(queries) == 0 {
		return nil, fmt.Errorf("no queries provided")
	}

	var (
		allClips []artlistdb.ArtlistClip
		mu       sync.Mutex
		wg       sync.WaitGroup
		errs     []error
	)

	// Run all queries in parallel
	for _, query := range queries {
		wg.Add(1)
		go func(q string) {
			defer wg.Done()

			clips, err := ps.searchSingleQuery(q)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("query '%s': %w", q, err))
				mu.Unlock()
				return
			}

			mu.Lock()
			allClips = append(allClips, clips...)
			mu.Unlock()
		}(query)
	}

	wg.Wait()

	// Deduplicate clips
	deduped := ps.deduplicateClips(allClips)

	// Sort by relevance (longer duration = usually higher quality)
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Duration > deduped[j].Duration
	})

	// Return top N
	if len(deduped) > topN {
		deduped = deduped[:topN]
	}

	logger.Info("Parallel search completed",
		zap.Int("queries", len(queries)),
		zap.Int("total_results", len(allClips)),
		zap.Int("deduped", len(deduped)),
		zap.Int("errors", len(errs)))

	return deduped, nil
}

// searchSingleQuery searches Artlist for a single query.
func (ps *ParallelSearcher) searchSingleQuery(query string) ([]artlistdb.ArtlistClip, error) {
	if ps.artlistSrc == nil {
		return nil, fmt.Errorf("Artlist source not available")
	}

	indexedClips, err := ps.artlistSrc.SearchClips(query, ps.maxResults)
	if err != nil {
		return nil, err
	}

	// Convert to ArtlistClip format
	var clips []artlistdb.ArtlistClip
	for _, ic := range indexedClips {
		clips = append(clips, artlistdb.ArtlistClip{
			ID:          ic.ID,
			VideoID:     ic.Filename,
			Title:       ic.Name,
			OriginalURL: ic.DownloadLink,
			URL:         ic.DownloadLink,
			Duration:    int(ic.Duration),
			Width:       ic.Width,
			Height:      ic.Height,
			Category:    ic.FolderPath,
			Tags:        ic.Tags,
		})
	}

	return clips, nil
}

// deduplicateClips removes duplicate clips by ID/URL.
func (ps *ParallelSearcher) deduplicateClips(clips []artlistdb.ArtlistClip) []artlistdb.ArtlistClip {
	seen := make(map[string]bool)
	var deduped []artlistdb.ArtlistClip

	for _, clip := range clips {
		key := clip.ID
		if key == "" {
			key = clip.URL
		}
		if key == "" {
			key = clip.OriginalURL
		}

		if key == "" {
			continue
		}

		normalizedKey := strings.ToLower(key)
		if !seen[normalizedKey] {
			seen[normalizedKey] = true
			deduped = append(deduped, clip)
		}
	}

	return deduped
}
