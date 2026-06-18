// Package searchutil provides post-processing utilities for hybrid search results:
// deduplication, diversity scoring, chunk aggregation, and multi-frame aggregation.
//
// These are applied after Qdrant/Reranker scoring to produce a clean, diverse result set.
package searchutil

import (
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
)

// DeduplicateAndDiversify removes near-duplicate results and applies diversity
// scoring to avoid returning 5 almost-identical segments from the same video.
//
// Strategy:
//  1. Group results by asset_id (dedup: keep highest score)
//  2. Group results by source video (diversify: penalty for same-video results)
//  3. Sort by final diversified score
//  4. Cap at maxPerSource results per source video
//
// Returns a clean, diverse result set suitable for display.
func DeduplicateAndDiversify(results []vectorstore.SearchResult, maxPerSource int) []vectorstore.SearchResult {
	if len(results) <= 1 {
		return results
	}
	if maxPerSource <= 0 {
		maxPerSource = 2
	}

	// Step 1: Group by asset_id, keep highest score
	seen := make(map[string]vectorstore.SearchResult)
	for _, r := range results {
		existing, ok := seen[r.AssetID]
		if !ok || r.Score > existing.Score {
			seen[r.AssetID] = r
		}
	}

	// Step 2: Extract source video ID and apply diversity scoring
	type enriched struct {
		result  vectorstore.SearchResult
		videoID string
	}

	enrichedResults := make([]enriched, 0, len(seen))
	for _, r := range seen {
		vid := extractSourceVideo(r)
		enrichedResults = append(enrichedResults, enriched{result: r, videoID: vid})
	}

	// Step 3: Sort by score (descending) then apply diversity cap
	sort.Slice(enrichedResults, func(i, j int) bool {
		return enrichedResults[i].result.Score > enrichedResults[j].result.Score
	})

	// Step 4: Apply diversity cap (max N results per source video)
	sourceCounts := make(map[string]int)
	diverse := make([]vectorstore.SearchResult, 0, len(enrichedResults))

	for _, e := range enrichedResults {
		vid := e.videoID
		if vid == "" {
			vid = e.result.AssetID // unique per asset_id
		}

		count := sourceCounts[vid]
		if count >= maxPerSource {
			continue
		}

		// Apply diversity penalty: each subsequent result from same source gets lower score
		if count > 0 {
			penalty := 0.05 * float64(count)
			e.result.Score = e.result.Score * (1.0 - penalty)
			if e.result.Reason == "" {
				e.result.Reason = "diversified"
			}
		}

		sourceCounts[vid]++
		diverse = append(diverse, e.result)
	}

	return diverse
}

// extractSourceVideo extracts a source video identifier from a search result.
// For YouTube clips, this is the youtube_video_id from metadata.
// For other sources, this is the asset_id itself.
func extractSourceVideo(r vectorstore.SearchResult) string {
	// YouTube clips: youtube_video_id is the canonical source grouping
	if r.YouTubeVideoID != "" {
		return "yt:" + r.YouTubeVideoID
	}
	// Fall back to source prefix for non-YouTube
	if r.Source != "" {
		return r.Source + ":" + r.AssetID
	}
	return r.AssetID
}

// AggregateChunks merges transcript chunk results into their parent asset.
// When searching with transcript chunks, multiple chunk results may point
// to the same parent clip. This function:
//  1. Groups chunks by asset_id
//  2. Takes the max score as the clip score
//  3. Adds a small bonus for multiple matching chunks (more evidence = better match)
//
// Returns the aggregated results sorted by score descending.
func AggregateChunks(chunkResults []vectorstore.SearchResult) []vectorstore.SearchResult {
	if len(chunkResults) <= 1 {
		return chunkResults
	}

	type aggregate struct {
		bestResult vectorstore.SearchResult
		chunkCount int
		maxScore   float64
		sumScores  float64
	}

	groups := make(map[string]*aggregate)
	for _, r := range chunkResults {
		ag, ok := groups[r.AssetID]
		if !ok {
			ag = &aggregate{
				bestResult: r,
				maxScore:   r.Score,
			}
			groups[r.AssetID] = ag
		}
		ag.chunkCount++
		ag.sumScores += r.Score
		if r.Score > ag.maxScore {
			ag.maxScore = r.Score
			ag.bestResult = r
		}
	}

	aggregated := make([]vectorstore.SearchResult, 0, len(groups))
	for _, ag := range groups {
		result := ag.bestResult

		// Score = max chunk score + small bonus for multiple matching chunks
		// Bonus capped at 0.10 to prevent overweighting long transcripts
		multiChunkBonus := 0.02 * float64(ag.chunkCount-1)
		if multiChunkBonus > 0.10 {
			multiChunkBonus = 0.10
		}
		result.Score = ag.maxScore + multiChunkBonus

		if ag.chunkCount > 1 {
			reason := "multi-chunk match"
			if result.Reason != "" {
				reason = result.Reason + "; " + reason
			}
			result.Reason = reason
		}

		aggregated = append(aggregated, result)
	}

	sort.Slice(aggregated, func(i, j int) bool {
		return aggregated[i].Score > aggregated[j].Score
	})

	return aggregated
}

// AggregateVisualFrames merges multi-frame visual results into their parent asset.
// Same logic as AggregateChunks but for visual frame points.
func AggregateVisualFrames(frameResults []vectorstore.SearchResult) []vectorstore.SearchResult {
	return AggregateChunks(frameResults) // same aggregation logic
}

// MaxResultsPerSource returns the maximum number of results allowed per
// source video for diversity. Default is 2.
func MaxResultsPerSource() int {
	return 2
}

// CleanSearchText normalizes search_text for embedding and BM25 tokenization.
// Removes common noise patterns and ensures consistent text quality.
func CleanSearchText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	// Remove HTML entities
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&gt;", " ")
	text = strings.ReplaceAll(text, "&lt;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")

	// Collapse multiple spaces
	text = strings.Join(strings.Fields(text), " ")

	return text
}
