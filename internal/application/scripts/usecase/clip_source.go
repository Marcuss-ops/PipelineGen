// Package usecase — clip source search helpers.
//
// clip_source.go owns the clip-search pipeline: SearchScriptAssets,
// BuildPhraseClipSuggestions, SearchIntroClips, and their query-building
// helpers (extractSearchKeywords, extractTopicKeywords, contextualQuery).
// Extracted from flow_helpers.go (July 2026, LONG-FILES-SPLIT-2026-07-06).
package usecase

import (
	"context"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ── SearchScriptAssets ──────────────────────────────────────────────────────

// SearchScriptAssets searches for assets across multiple query-target pairs
// and returns the top suggestions. Falls back to auto-harvest when empty.
func SearchScriptAssets(ctx context.Context, svc ClipServices, queries []string, targets []AssetSearchTarget, limit int) []ScriptAssetSuggestion {
	if svc.RealtimeSvc == nil || len(queries) == 0 || len(targets) == 0 {
		return nil
	}

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
			assets, err := svc.RealtimeSvc.SearchClips(ctx, query, target.Source, target.MediaType, limit, 0.7)
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

func filterSearchAssets(matches []RealtimeMatchAsset, topicKeywords string, seen map[string]struct{}, limit int) []ScriptAssetSuggestion {
	out := make([]ScriptAssetSuggestion, 0, minInt(limit, len(matches)))
	for _, asset := range matches {
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

// ── BuildPhraseClipSuggestions + SearchIntroClips ───────────────────────────

// BuildPhraseClipSuggestions searches for clips matching each important phrase.
func BuildPhraseClipSuggestions(ctx context.Context, svc ClipServices, title string, insights ScriptInsights, targets []AssetSearchTarget) []ScriptPhraseClipSuggestion {
	if svc.RealtimeSvc == nil || len(targets) == 0 {
		return nil
	}

	phrases := sliceutil.UniqueLimitedStrings(insights.ImportantPhrases, 5)
	out := make([]ScriptPhraseClipSuggestion, 0, len(phrases))
	for _, phrase := range phrases {
		localQuery := extractSearchKeywords(phrase, title, insights.SpecialNames)
		if localQuery == "" {
			continue
		}
		topicQuery := contextualQuery(title, localQuery)
		queries := []string{topicQuery, localQuery}
		queries = sliceutil.UniqueLimitedStrings(queries, 2)
		clips := SearchScriptAssets(ctx, svc, queries, targets, 1)
		if len(clips) == 0 {
			continue
		}
		out = append(out, ScriptPhraseClipSuggestion{
			Phrase: phrase,
			Clips:  clips,
		})
		if len(out) >= 5 {
			break
		}
	}
	return out
}

// SearchIntroClips searches for intro clip candidates matching the topic.
func SearchIntroClips(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights, targets []AssetSearchTarget) []ScriptAssetSuggestion {
	if svc.RealtimeSvc == nil || len(targets) == 0 {
		return nil
	}

	queries := make([]string, 0, 6)
	if t := strings.TrimSpace(title); t != "" {
		queries = append(queries, t)
	}
	sentences := textutil.SplitScriptSentences(script)
	if len(sentences) > 0 {
		queries = append(queries, sentences[:sliceutil.MinInt(2, len(sentences))]...)
	}
	if len(insights.SpecialNames) > 0 {
		queries = append(queries, insights.SpecialNames[:sliceutil.MinInt(3, len(insights.SpecialNames))]...)
	}
	queries = sliceutil.UniqueLimitedStrings(queries, 6)
	if len(queries) == 0 {
		return nil
	}
	return SearchScriptAssets(ctx, svc, queries, targets, 2)
}

// ── Query helpers ────────────────────────────────────────────────────────────

func extractSearchKeywords(phrase, title string, specialNames []string) string {
	var keywords []string
	for _, name := range specialNames {
		if textutil.ContainsCI(phrase, name) {
			keywords = append(keywords, name)
		}
	}
	if title != "" {
		for _, w := range strings.Fields(title) {
			if len(w) < 3 {
				continue
			}
			if textutil.ContainsCI(phrase, w) {
				keywords = append(keywords, w)
			}
		}
	}
	if len(keywords) < 3 {
		for _, w := range strings.Fields(phrase) {
			clean := strings.Trim(strings.ToLower(w), ".,;:!?\"'")
			if len(clean) < 3 || linguistics.IsStopWord(clean) {
				continue
			}
			keywords = append(keywords, clean)
		}
	}
	keywords = sliceutil.UniqueLimitedStrings(keywords, 4)
	return strings.Join(keywords, " ")
}

func extractTopicKeywords(title string) string {
	if title == "" {
		return ""
	}
	words := strings.Fields(title)
	var kept []string
	for _, w := range words {
		clean := strings.Trim(strings.ToLower(w), ".,;:!?\"'()")
		if len(clean) < 3 || linguistics.IsStopWord(clean) {
			continue
		}
		kept = append(kept, clean)
	}
	if len(kept) > 7 {
		kept = kept[:7]
	}
	return strings.Join(kept, " ")
}

func contextualQuery(title, phrase string) string {
	keywords := extractTopicKeywords(title)
	if keywords == "" {
		return phrase
	}
	return keywords + " " + phrase
}
