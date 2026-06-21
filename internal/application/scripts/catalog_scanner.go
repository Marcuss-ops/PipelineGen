package scripts

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/reranker"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
)

// ── Catalog types ──────────────────────────────────────────────────────

// CatalogCluster is a thematic cluster of clips discovered by CatalogScanner.
type CatalogCluster struct {
	Theme      string   `json:"theme"`
	ClipIDs    []string `json:"clip_ids"`
	ClipCount  int      `json:"clip_count"`
	Role       string   `json:"role"`     // "main", "supporting", "transition", "closing"
	Coverage   string   `json:"coverage"` // "sufficient", "partial", "insufficient"
	AvgQuality float64  `json:"avg_quality,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// CatalogReport is the output of catalog scanning and clustering.
type CatalogReport struct {
	Topic           string           `json:"topic"`
	ClustersFound   int              `json:"clusters_found"`
	ClustersUsed    int              `json:"clusters_used"`
	TotalClipsFound int              `json:"total_clips_found"`
	ClipsSelected   int              `json:"clips_selected"`
	CoverageScore   float64          `json:"coverage_score"`
	Clusters        []CatalogCluster `json:"clusters"`
	Warnings        []string         `json:"warnings,omitempty"`
}

// catalogLLMResponse is the JSON structure expected from the LLM clustering call.
type catalogLLMResponse struct {
	Title    string           `json:"title"`
	Clusters []CatalogCluster `json:"clusters"`
	Warnings []string         `json:"warnings,omitempty"`
}

// ── SelectClipsForTopic ─────────────────────────────────────────────────

// SelectClipsForTopic searches the clip catalog for a topic, clusters results
// via LLM, and returns the selected clip IDs along with a coverage report.
// Only clusters with "sufficient" or "partial" coverage are included.
//
// Search strategy (two-layer):
//  1. LIKE search on tags, name, and metadata (synchronous, always runs)
//  2. Qdrant vector search (optional, runs if vectorSvc is available)
//
// Results from both sources are merged, deduplicated by clip ID.
func (b *ClipSourceBuilder) SelectClipsForTopic(ctx context.Context, topic string, maxClips int) ([]string, *CatalogReport, error) {
	if strings.TrimSpace(topic) == "" {
		return nil, nil, fmt.Errorf("topic is required")
	}
	if maxClips <= 0 {
		maxClips = 10
	}

	b.log.Info("catalog scanning: searching clips for topic",
		zap.String("topic", topic),
		zap.Int("max_clips", maxClips),
	)

	// Track which search method discovered each clip for reporting
	seen := make(map[string]struct{})
	var allClips []*searchClipSummary

	// ── Layer 1: LIKE search (always runs) ─────────────────────────────
	likeResults, likeErr := b.clipsRepo.SearchClips(ctx, "", topic)
	if likeErr == nil && len(likeResults) > 0 {
		for _, clip := range likeResults {
			if _, ok := seen[clip.ID]; ok {
				continue
			}
			summary := b.toSearchSummary(clip)
			if summary != nil {
				seen[clip.ID] = struct{}{}
				allClips = append(allClips, summary)
			}
		}
		b.log.Info("catalog: LIKE search results",
			zap.Int("total", len(likeResults)),
			zap.Int("usable", len(allClips)),
		)
	} else if likeErr != nil {
		b.log.Warn("catalog: LIKE search failed, proceeding with vector search",
			zap.Error(likeErr),
		)
	}

	// ── Layer 2: Qdrant vector search (optional) ───────────────────────
	vdResults, vdErr := b.searchViaQdrant(ctx, topic)
	if vdErr == nil && len(vdResults) > 0 {
		added := 0
		for _, clip := range vdResults {
			if _, ok := seen[clip.ID]; ok {
				continue
			}
			summary := b.toSearchSummary(clip)
			if summary != nil {
				seen[clip.ID] = struct{}{}
				allClips = append(allClips, summary)
				added++
			}
		}
		b.log.Info("catalog: Qdrant search results",
			zap.Int("total", len(vdResults)),
			zap.Int("new", added),
			zap.Int("total_unique", len(allClips)),
		)
	} else if vdErr != nil {
		b.log.Debug("catalog: Qdrant search unavailable or failed, using LIKE only",
			zap.Error(vdErr),
		)
	}

	if len(allClips) == 0 {
		b.log.Warn("catalog scan: no usable clips found", zap.String("topic", topic))
		return nil, &CatalogReport{
			Topic:           topic,
			ClustersFound:   0,
			ClustersUsed:    0,
			TotalClipsFound: 0,
			ClipsSelected:   0,
			CoverageScore:   0,
			Warnings:        []string{"no clips with transcripts found for topic"},
		}, nil
	}

	// ── Step 2: Rerank merged results (optional) ────────────────────────
	allClips = b.rerankClips(ctx, topic, allClips)

	// Build usable slice for clustering
	usable := make([]searchClipSummary, len(allClips))
	for i, c := range allClips {
		usable[i] = *c
	}

	// if few clips, skip LLM clustering
	if len(usable) <= maxClips {
		clipIDs := make([]string, len(usable))
		for i, c := range usable {
			clipIDs[i] = c.ID
		}
		report := &CatalogReport{
			Topic:           topic,
			ClustersFound:   1,
			ClustersUsed:    1,
			TotalClipsFound: len(usable),
			ClipsSelected:   len(clipIDs),
			CoverageScore:   1.0,
			Clusters: []CatalogCluster{{
				Theme:     topic,
				ClipIDs:   clipIDs,
				ClipCount: len(clipIDs),
				Role:      "main",
				Coverage:  "sufficient",
			}},
		}
		return clipIDs, report, nil
	}

	// Step 3: LLM clustering for larger sets
	// If no Ollama client is available, skip clustering and use all clips as fallback.
	if b.ollamaCli == nil {
		b.log.Warn("catalog scan: no Ollama client, skipping LLM clustering, using all clips")
		clipIDs := make([]string, len(usable))
		for i, c := range usable {
			clipIDs[i] = c.ID
		}
		return clipIDs, &CatalogReport{
			Topic:           topic,
			ClustersFound:   1,
			ClustersUsed:    1,
			TotalClipsFound: len(usable),
			ClipsSelected:   len(clipIDs),
			CoverageScore:   0.5,
			Warnings:        []string{"no Ollama client available, using all clips"},
		}, nil
	}

	clusterResult, err := b.clusterClipsViaLLM(ctx, topic, usable, maxClips)
	if err != nil {
		b.log.Warn("catalog scan: LLM clustering failed, using all clips as fallback",
			zap.Error(err),
		)
		clipIDs := make([]string, len(usable))
		for i, c := range usable {
			clipIDs[i] = c.ID
		}
		return clipIDs, &CatalogReport{
			Topic:           topic,
			ClustersFound:   1,
			ClustersUsed:    1,
			TotalClipsFound: len(usable),
			ClipsSelected:   len(clipIDs),
			CoverageScore:   0.5,
			Warnings:        []string{"LLM clustering failed, using all clips"},
		}, nil
	}

	// Step 4: Select clips from sufficient/partial clusters
	var selectedIDs []string
	var usedClusters []CatalogCluster

	for _, cluster := range clusterResult.Clusters {
		if cluster.Coverage == "insufficient" {
			continue
		}
		usedClusters = append(usedClusters, cluster)

		for _, id := range cluster.ClipIDs {
			if len(selectedIDs) >= maxClips {
				break
			}
			selectedIDs = append(selectedIDs, id)
		}
		if len(selectedIDs) >= maxClips {
			break
		}
	}

	// Fill remaining slots
	if len(selectedIDs) < maxClips {
		for _, c := range usable {
			found := false
			for _, sid := range selectedIDs {
				if sid == c.ID {
					found = true
					break
				}
			}
			if !found {
				selectedIDs = append(selectedIDs, c.ID)
				if len(selectedIDs) >= maxClips {
					break
				}
			}
		}
	}

	coverageScore := 0.0
	if len(usable) > 0 {
		coverageScore = float64(len(selectedIDs)) / float64(len(usable))
	}

	report := &CatalogReport{
		Topic:           topic,
		ClustersFound:   len(clusterResult.Clusters),
		ClustersUsed:    len(usedClusters),
		TotalClipsFound: len(usable),
		ClipsSelected:   len(selectedIDs),
		CoverageScore:   coverageScore,
		Clusters:        usedClusters,
		Warnings:        clusterResult.Warnings,
	}

	b.log.Info("catalog scan: completed",
		zap.Int("clusters_found", report.ClustersFound),
		zap.Int("clusters_used", report.ClustersUsed),
		zap.Int("selected", report.ClipsSelected),
		zap.Float64("coverage", report.CoverageScore),
	)

	return selectedIDs, report, nil
}

// searchClipSummary is a compact representation of a search result for LLM clustering.
type searchClipSummary struct {
	ID       string
	Name     string
	Summary  string
	Topics   string // JSON array string or empty
	Quality  float64
	Duration int
}

// ── Helper methods ────────────────────────────────────────────────────

// toSearchSummary converts a MediaAsset to a searchClipSummary, filtering out
// clips without transcripts. Returns nil if the clip is not usable.
func (b *ClipSourceBuilder) toSearchSummary(clip *asset.Asset) *searchClipSummary {
	transcript := clip.GetMetadataString("clean_transcript")
	if transcript == "" {
		transcript = clip.GetMetadataString("transcript")
	}
	if transcript == "" {
		return nil
	}

	summary := clip.GetMetadataString("clip_summary")
	if summary == "" {
		summary = clip.Name
	}

	topicsRaw := clip.GetMetadataString("topics")
	qualityStr := clip.GetMetadataString("quality_score")
	var quality float64
	if qualityStr != "" {
		fmt.Sscanf(qualityStr, "%f", &quality)
	}

	return &searchClipSummary{
		ID:       clip.ID,
		Name:     clip.Name,
		Summary:  summary,
		Topics:   topicsRaw,
		Quality:  quality,
		Duration: int(clip.Duration.Seconds()),
	}
}

// searchViaQdrant searches the vector store for clips semantically matching the topic.
// Uses HybridSearch with BM25 sparse vector (auto-generated from topic text).
// Returns nil, nil if vector store is not configured or unavailable.
func (b *ClipSourceBuilder) searchViaQdrant(ctx context.Context, topic string) ([]*asset.Asset, error) {
	if b.vectorSvc == nil {
		return nil, fmt.Errorf("vector store not configured")
	}

	// Use hybrid search with just QueryText—the service auto-generates
	// a BM25 sparse vector from the text for token-level matching.
	// This catches clips the LIKE search misses: different wording,
	// synonyms, or languages.
	results, err := b.vectorSvc.HybridSearch(ctx, qdrant.HybridSearchRequest{
		QueryText: topic,
		Limit:     30, // Fetch more than needed for dedup with LIKE
		MinScore:  0.1,
	})
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	// Fetch full MediaAsset records for the returned asset IDs from the clips repository.
	// The clipsRepo is used for all sources; vector results may include clips from
	// any indexed source (youtube, artlist, stock).
	var foundAssets []*asset.Asset
	for _, r := range results {
		clip, err := b.clipsRepo.GetClip(ctx, r.AssetID)
		if err != nil || clip == nil {
			b.log.Debug("vector result clip not found in DB, skipping",
				zap.String("asset_id", r.AssetID),
				zap.Error(err),
			)
			continue
		}
		foundAssets = append(foundAssets, clip)
	}

	return foundAssets, nil
}

// rerankClips optionally reorders merged search results using the CrossEncoder reranker
// (BGE-reranker-v2-m3). Clips more semantically relevant to the topic are moved to the
// front, improving the LLM clustering quality that follows.
//
// Graceful degradation: if the reranker is not configured, unavailable, or fails,
// the original order is preserved (LIKE-first, then Qdrant-discovered).
func (b *ClipSourceBuilder) rerankClips(ctx context.Context, topic string, clips []*searchClipSummary) []*searchClipSummary {
	if b.rerankerCli == nil || !b.rerankerCli.IsEnabled() || len(clips) < 2 {
		return clips
	}

	b.log.Debug("reranking catalog results",
		zap.Int("clips", len(clips)),
		zap.String("topic", topic),
	)

	candidates := make([]reranker.Candidate, len(clips))
	for i, clip := range clips {
		// Parse topics JSON string to []string for richer candidate text
		var tags []string
		if clip.Topics != "" && clip.Topics != "[]" {
			json.Unmarshal([]byte(clip.Topics), &tags)
		}

		candidates[i] = reranker.Candidate{
			ID:   clip.ID,
			Text: reranker.BuildCandidateText(clip.Name, clip.Summary, tags, "", "", ""),
		}
	}

	results, err := b.rerankerCli.Rerank(ctx, topic, candidates)
	if err != nil || len(results) == 0 {
		b.log.Debug("reranker failed or returned no results, preserving original order",
			zap.Error(err),
		)
		return clips
	}

	// Build score map from reranker results
	scoreMap := make(map[string]float64, len(results))
	for _, r := range results {
		scoreMap[r.ID] = r.RerankScore
	}

	// Sort: higher rerank score first. Clips not scored by the reranker
	// (shouldn't happen, but be safe) go to the end.
	slices.SortFunc(clips, func(a, b *searchClipSummary) int {
		scoreA := scoreMap[a.ID]
		scoreB := scoreMap[b.ID]
		// Descending: higher score first
		if scoreA > scoreB {
			return -1
		}
		if scoreA < scoreB {
			return 1
		}
		return 0
	})

	b.log.Debug("reranker reordered catalog results",
		zap.Int("clips", len(clips)),
		zap.String("top_id", clips[0].ID),
		zap.Float64("top_score", scoreMap[clips[0].ID]),
	)

	return clips
}
