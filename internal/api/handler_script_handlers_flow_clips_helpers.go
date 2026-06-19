package api

import (
	"context"
	"strings"

	sliceutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
	textutil "github.com/Marcuss-ops/PipelineGen/internal/platform"
)

type assetSearchTarget struct {
	source    string
	mediaType string
}

// ── BuildPhraseClipSuggestions ───────────────────────────────────────────────

// BuildPhraseClipSuggestions searches for clips matching each important phrase.
func BuildPhraseClipSuggestions(ctx context.Context, svc ClipServices, title string, insights ScriptInsights, targets []assetSearchTarget) []ScriptPhraseClipSuggestion {
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

// ── SearchIntroClips ─────────────────────────────────────────────────────────

// SearchIntroClips searches for intro clip candidates matching the topic.
func SearchIntroClips(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights, targets []assetSearchTarget) []ScriptAssetSuggestion {
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

// ── Query Helpers ────────────────────────────────────────────────────────────

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
			if len(clean) < 3 || textutil.IsStopWord(clean) {
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
		if len(clean) < 3 || textutil.IsStopWord(clean) {
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
