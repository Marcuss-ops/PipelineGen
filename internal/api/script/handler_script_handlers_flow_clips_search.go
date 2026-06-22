package script

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/realtime"
	"go.uber.org/zap"
)

// ── SearchScriptAssets ───────────────────────────────────────────────────────

// SearchScriptAssets searches for assets across multiple query-target pairs
// and returns the top suggestions. Falls back to auto-harvest when empty.
func SearchScriptAssets(ctx context.Context, svc ClipServices, queries []string, targets []assetSearchTarget, limit int) []ScriptAssetSuggestion {
	if svc.RealtimeSvc == nil || len(queries) == 0 || len(targets) == 0 {
		return nil
	}

	// Extract clean topic keywords (stop words removed) from the longest
	// query for post-filtering.
	topicKeywords := ""
	for _, q := range queries {
		cleaned := extractTopicKeywords(q)
		if len(strings.Fields(cleaned)) > len(strings.Fields(topicKeywords)) {
			topicKeywords = cleaned
		}
	}

	seen := make(map[string]struct{})
	out := make([]ScriptAssetSuggestion, 0, limit)

	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		for _, target := range targets {
			assets, err := svc.RealtimeSvc.SearchClips(ctx, query, target.source, target.mediaType, limit, 0.7)
			if err != nil {
				continue
			}
			remaining := limit - len(out)
			if remaining <= 0 {
				return out
			}
			results := filterSearchAssets(assets, topicKeywords, seen, remaining)
			out = append(out, results...)
			if len(out) >= limit {
				return out
			}
		}
	}

	// Auto-harvest: if no clips found and harvest service is available,
	// enqueue background download jobs for the search terms.
	if len(out) == 0 && svc.HarvestSvc != nil && len(queries) > 0 {
		for _, q := range queries {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			if svc.Logger != nil {
				svc.Logger.Info("auto-harvest triggered: no clips found for query",
					zap.String("query", q))
			}
			svc.HarvestSvc.EnqueueHarvest(ctx, q, 3, "youtube_1080p_7s")
		}
	}

	return out
}

// ── Standalone Search Helpers ────────────────────────────────────────────────

func filterSearchAssets(assets []realtime.MatchAsset, topicKeywords string, seen map[string]struct{}, limit int) []ScriptAssetSuggestion {
	out := make([]ScriptAssetSuggestion, 0, min(limit, len(assets)))
	for _, asset := range assets {
		if len(out) >= limit {
			break
		}
		if _, ok := seen[asset.ID]; ok {
			continue
		}
		if asset.Source != "artlist" && !topicRelevant(asset.Name, topicKeywords) {
			continue
		}
		seen[asset.ID] = struct{}{}
		out = append(out, ScriptAssetSuggestion{
			ID:        asset.ID,
			Name:      asset.Name,
			Source:    asset.Source,
			Score:     asset.Score,
			DriveLink: asset.DriveLink,
		})
	}
	return out
}

func topicRelevant(assetName, topicKeywords string) bool {
	if topicKeywords == "" {
		return true
	}
	nameLower := strings.ToLower(assetName)
	topicWords := strings.Fields(topicKeywords)
	for _, w := range topicWords {
		if len(w) < 4 {
			continue
		}
		if strings.Contains(nameLower, w) {
			return true
		}
		if len(w) >= 4 {
			for _, nw := range strings.Fields(nameLower) {
				if len(nw) < 4 {
					continue
				}
				if w[:3] == nw[:3] {
					return true
				}
			}
		}
	}
	return false
}
