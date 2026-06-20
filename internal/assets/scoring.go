package assets

import (
	"sort"
	"strings"
)

// scoreClips scores and sorts clips based on keyword match quality, quality score,
// duplicate penalty, sponsor penalty, and search visibility.
func scoreClips(clips []*Asset, keywords []string) []*Asset {
	type scoredClip struct {
		clip  *Asset
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
		if qs := metadataFloat(clip.Metadata, "quality_score"); qs > 0 {
			score += qs * 0.2
		}
		if visibilityBonus := searchVisibilityBonus(clip.Metadata); visibilityBonus != 0 {
			score += visibilityBonus
		}

		// Structured metadata boosts
		if clip.Metadata != nil {
			if v := strings.ToLower(strings.TrimSpace(metadataString(clip.Metadata, "clean_title"))); v != "" {
				for _, kw := range keywordsLower {
					if strings.Contains(v, kw) {
						score += 0.12
						break
					}
				}
			}
			for _, field := range []string{"clip_summary", "hook", "embedding_text"} {
				if v := strings.ToLower(metadataString(clip.Metadata, field)); v != "" {
					for _, kw := range keywordsLower {
						if strings.Contains(v, kw) {
							score += 0.06
							break
						}
					}
				}
			}
			score += float64(len(metadataStringSlice(clip.Metadata, "topics"))) * 0.015
			score += float64(len(metadataStringSlice(clip.Metadata, "speakers"))) * 0.012
			score += float64(len(metadataStringSlice(clip.Metadata, "mentioned_people"))) * 0.012
		}

		// Sponsor penalty (-0.2)
		if isSponsorSegment(clip) {
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
	result := make([]*Asset, len(scored))
	for i, s := range scored {
		result[i] = s.clip
	}

	return result
}

// isSponsorSegment checks if a clip is a sponsor segment based on metadata.
func isSponsorSegment(clip *Asset) bool {
	if clip.Metadata == nil {
		return false
	}
	if isSponsor, ok := clip.Metadata["is_sponsor_segment"].(bool); ok {
		return isSponsor
	}
	return false
}

func searchableClipText(clip *Asset) string {
	if clip == nil {
		return ""
	}
	parts := []string{
		clip.Name,
		clip.SearchText,
		metadataString(clip.Metadata, "clean_title"),
		metadataString(clip.Metadata, "clip_summary"),
		metadataString(clip.Metadata, "hook"),
		metadataStringSliceText(clip.Metadata, "topics"),
		metadataStringSliceText(clip.Metadata, "speakers"),
		metadataStringSliceText(clip.Metadata, "mentioned_people"),
		metadataStringSliceText(clip.Metadata, "people"),
		metadataStringSliceText(clip.Metadata, "clip_tags"),
		metadataStringSliceText(clip.Metadata, "search_keywords"),
		metadataStringSliceText(clip.Metadata, "source_tags"),
		metadataString(clip.Metadata, "embedding_text"),
	}
	return strings.Join(parts, "\n")
}

func isDuplicateClip(clip *Asset) bool {
	if clip == nil || clip.Metadata == nil {
		return false
	}
	return metadataBool(clip.Metadata, "is_duplicate") || metadataString(clip.Metadata, "duplicate_of") != ""
}

func isBestVersionClip(clip *Asset) bool {
	if clip == nil || clip.Metadata == nil {
		return false
	}
	return metadataBool(clip.Metadata, "is_best_version")
}

func searchVisibilityBonus(meta map[string]any) float64 {
	switch strings.ToLower(strings.TrimSpace(metadataString(meta, "search_visibility"))) {
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
