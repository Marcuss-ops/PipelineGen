package detail

import (
	"sort"
	"strings"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// ScoreClips scores and sorts clips based on keyword match quality,
// quality score, duplicate penalty, sponsor penalty, and search
// visibility.
//
// Wave C (Blocco 1 asset.Asset SSOT, June 2026): this variant is the
// canonical public export. Cross-package callers reach the algorithm
// through this name (see `internal/platform/sqlite/assets
// /search_queries.go::SearchClips`). The package-internal impl
// `scoreClipsInternal` carries the actual logic; keeping both names
// exports a stable public surface while leaving the package free to
// refactor internals (e.g. extract scoring-config knobs) without
// breaking callers that depend on the canonical name.
//
// Returns a fresh slice — input is not mutated.
func ScoreClips(clips []*asset.Asset, keywords []string) []*asset.Asset {
	return scoreClipsInternal(clips, keywords)
}

// scoreClipsInternal is the package-internal impl; ScoreClips is the
// canonical public export so callers outside the asset package (e.g.
// the infra Wave C `assets` package — search_queries.go) can reach
// it without inverting the dependency. The logic is identical; only
// the export name changed.
//
// Wave C (Blocco 1 asset.Asset SSOT, June 2026): promotion to public
// surface to support the SQL receiver migration. The two-name
// distinction is documented here for grep-ability and to keep
// future contributors from re-introducing an unexported duplicate.
func scoreClipsInternal(clips []*asset.Asset, keywords []string) []*asset.Asset {
	type scoredClip struct {
		clip  *asset.Asset
		score float64
	}

	scored := make([]scoredClip, 0, len(clips))
	keywordsLower := make([]string, len(keywords))
	for i, k := range keywords {
		keywordsLower[i] = strings.ToLower(k)
	}

	for _, clip := range clips {
		score := 0.0

		// Keyword match scoring (0.0 - 0.65)
		nameLower := strings.ToLower(clip.Name)
		tagsLower := strings.ToLower(strings.Join(clip.Tags, " "))
		searchTextLower := strings.ToLower(clip.SearchText)
		metaTextLower := strings.ToLower(searchableClipText(clip))

		for _, kw := range keywordsLower {
			// Title match (highest weight)
			if strings.Contains(nameLower, kw) {
				score += 0.18
			}
			// Tag match (medium weight)
			if strings.Contains(tagsLower, kw) {
				score += 0.12
			}
			// Search text and semantic metadata match
			if strings.Contains(searchTextLower, kw) {
				score += 0.08
			}
			if strings.Contains(metaTextLower, kw) {
				score += 0.16
			}
		}

		// Cap keyword score at 0.65
		if score > 0.65 {
			score = 0.65
		}

		// Quality score bonus (0.0 - 0.2)
		if qs := asset.MetadataFloat(clip.Metadata, "quality_score"); qs > 0 {
			score += qs * 0.2
		}
		if visibilityBonus := searchVisibilityBonus(clip.Metadata); visibilityBonus != 0 {
			score += visibilityBonus
		}

		// Structured metadata boosts
		if clip.Metadata != nil {
			if v := strings.ToLower(strings.TrimSpace(asset.MetadataString(clip.Metadata, "clean_title"))); v != "" {
				for _, kw := range keywordsLower {
					if strings.Contains(v, kw) {
						score += 0.12
						break
					}
				}
			}
			for _, field := range []string{"clip_summary", "hook", "embedding_text"} {
				if v := strings.ToLower(asset.MetadataString(clip.Metadata, field)); v != "" {
					for _, kw := range keywordsLower {
						if strings.Contains(v, kw) {
							score += 0.06
							break
						}
					}
				}
			}
			score += float64(len(asset.MetadataStringSlice(clip.Metadata, "topics"))) * 0.015
			score += float64(len(asset.MetadataStringSlice(clip.Metadata, "speakers"))) * 0.012
			score += float64(len(asset.MetadataStringSlice(clip.Metadata, "mentioned_people"))) * 0.012
		}

		// Sponsor penalty (-0.2). Reads the pre-computed metadata flag
		// that was set by metadata.IsSponsorSegment during enrichment.
		if isSponsorFlagged(clip) {
			score -= 0.2
		}
		if isDuplicateClip(clip) {
			if isBestVersionClip(clip) {
				score -= 0.03
			} else {
				score -= 0.28
			}
		}

		// Ensure score is not negative
		if score < 0 {
			score = 0
		}

		scored = append(scored, scoredClip{clip: clip, score: score})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Extract sorted clips
	result := make([]*asset.Asset, len(scored))
	for i, s := range scored {
		result[i] = s.clip
	}

	return result
}

// isSponsorFlagged checks whether a clip was flagged as a sponsor segment
// during enrichment. The flag is set by metadata.IsSponsorSegment (regex
// scanning) in the enrichment pipeline and persisted in
// Metadata["is_sponsor_segment"]. This function is the consumer side —
// it reads the pre-computed flag rather than re-scanning transcript text.
func isSponsorFlagged(clip *asset.Asset) bool {
	if clip.Metadata == nil {
		return false
	}
	if isSponsor, ok := clip.Metadata["is_sponsor_segment"].(bool); ok {
		return isSponsor
	}
	return false
}

func searchableClipText(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	parts := []string{
		clip.Name,
		clip.SearchText,
		asset.MetadataString(clip.Metadata, "clean_title"),
		asset.MetadataString(clip.Metadata, "clip_summary"),
		asset.MetadataString(clip.Metadata, "hook"),
		asset.MetadataStringSliceText(clip.Metadata, "topics"),
		asset.MetadataStringSliceText(clip.Metadata, "speakers"),
		asset.MetadataStringSliceText(clip.Metadata, "mentioned_people"),
		asset.MetadataStringSliceText(clip.Metadata, "people"),
		asset.MetadataStringSliceText(clip.Metadata, "clip_tags"),
		asset.MetadataStringSliceText(clip.Metadata, "search_keywords"),
		asset.MetadataStringSliceText(clip.Metadata, "source_tags"),
		asset.MetadataString(clip.Metadata, "embedding_text"),
	}
	return strings.Join(parts, "\n")
}

func isDuplicateClip(clip *asset.Asset) bool {
	if clip == nil || clip.Metadata == nil {
		return false
	}
	return asset.MetadataBool(clip.Metadata, "is_duplicate") || asset.MetadataString(clip.Metadata, "duplicate_of") != ""
}

func isBestVersionClip(clip *asset.Asset) bool {
	if clip == nil || clip.Metadata == nil {
		return false
	}
	return asset.MetadataBool(clip.Metadata, "is_best_version")
}

func searchVisibilityBonus(meta map[string]any) float64 {
	switch strings.ToLower(strings.TrimSpace(asset.MetadataString(meta, "search_visibility"))) {
	case "high":
		return 0.08
	case "normal":
		return 0.03
	case "low":
		return -0.02
	case "poor":
		return -0.08
	default:
		return 0
	}
}

type Params struct {
	Query       string
	QueryTokens []string
	Topic       string
	Name        string
	Path        string
	Tags        []string
}

type Result struct {
	Score        int
	TopicMatched bool
}

func TokenScore(queryTokens, targetTokens []string) int {
	if len(queryTokens) == 0 || len(targetTokens) == 0 {
		return 0
	}

	targetMap := make(map[string]bool)
	for _, t := range targetTokens {
		targetMap[t] = true
	}

	matches := 0
	for _, q := range queryTokens {
		if targetMap[q] {
			matches++
		}
	}

	if matches == 0 {
		return 0
	}

	score := (matches * 100) / len(queryTokens)

	queryJoined := strings.Join(queryTokens, " ")
	targetJoined := strings.Join(targetTokens, " ")
	if strings.Contains(queryJoined, targetJoined) {
		score += 30
	}

	if score > 100 {
		score = 100
	}

	return score
}

// ScoreText counts how many terms appear as substrings in the candidate text.
func ScoreText(candidate string, terms []string) int {
	candidate = strings.ToLower(candidate)
	score := 0
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(candidate, term) {
			score++
		}
	}
	return score
}

func Calculate(p Params) Result {
	queryTokens := p.QueryTokens
	if len(queryTokens) == 0 && p.Query != "" {
		queryTokens = textutil.Tokenize(p.Query)
	}

	if len(queryTokens) == 0 {
		return Result{}
	}

	topic := strings.TrimSpace(strings.ToLower(p.Topic))
	name := strings.ToLower(p.Name)
	tagsStr := strings.ToLower(strings.Join(p.Tags, " "))

	targetText := name + " " + tagsStr
	targetTokens := textutil.Tokenize(targetText)

	tokenScore := TokenScore(queryTokens, targetTokens)

	targetMap := make(map[string]bool)
	for _, t := range targetTokens {
		targetMap[t] = true
	}

	topicMatched := false
	if topic != "" {
		topicTokens := textutil.Tokenize(topic)
		for _, tt := range topicTokens {
			if len(tt) <= 3 {
				continue
			}
			if strings.Contains(name, tt) || strings.Contains(tagsStr, tt) {
				topicMatched = true
				break
			}
		}
		if !topicMatched && (strings.Contains(name, topic) || strings.Contains(tagsStr, topic)) {
			topicMatched = true
		}
	}

	score := tokenScore

	if len(p.Query) > 3 && (textutil.ContainsCI(p.Name, p.Query) || textutil.ContainsCI(p.Path, p.Query)) {
		score += 15
	}

	if topicMatched {
		score += 40
		if strings.Contains(name, topic) || strings.Contains(tagsStr, topic) {
			score += 50
		}
	}

	if topic != "" && !topicMatched && score > 40 {
		score = 40
	}

	if !topicMatched {
		unmatchedCount := 0
		uniqueTokens := make(map[string]bool)
		for _, ct := range targetTokens {
			if len(ct) <= 3 {
				continue
			}
			if !uniqueTokens[ct] {
				uniqueTokens[ct] = true
				found := false
				for _, q := range queryTokens {
					if q == ct {
						found = true
						break
					}
				}
				if !found {
					unmatchedCount++
				}
			}
		}

		if len(uniqueTokens) > 0 {
			noiseRatio := float64(unmatchedCount) / float64(len(uniqueTokens))
			if noiseRatio > 0.6 {
				score -= int(noiseRatio * 50)
			}
		}
	}

	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}

	return Result{Score: score, TopicMatched: topicMatched}
}
