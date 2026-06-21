package youtube

import (
	tagutil "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/tagutil"
	"regexp"
	"strings"
)

var (
	// Timestamp patterns: 0:00, 1:23:45, 00:00:00, etc.
	timestampRegex = regexp.MustCompile(`(?m)^\s*\d{1,2}:\d{2}(?::\d{2})?\s*$`)

	// URLs (http/https/www)
	urlRegex = regexp.MustCompile(`https?://\S+|www\.\S+`)

	// Social media and common CTA patterns
	socialPatterns = regexp.MustCompile(`(?i)` +
		`(?m)^\s*(subscribe|like|comment|share|follow|join|hit the bell|notification|click the link|check out|sign up|use code|promo code|discount|affiliate|sponsored|ad\b|advertisement|merch|merchandise|store|shop|buy|purchase|deal|offer|coupon)\b.*$`)

	// Common sponsor/promo patterns
	sponsorPatterns = regexp.MustCompile(`(?i)` +
		`(?m)^\s*(use code|promo code|discount code|affiliate|sponsored by|brought to you by|partner with|thanks to|special thanks|shoutout|shout out)\b.*$`)

	// Timestamp lines with context (e.g., "00:00 Intro", "1:23:45 - Chapter Title")
	timestampContextRegex = regexp.MustCompile(`(?m)^\s*\d{1,2}:\d{2}(?::\d{2})?\s*[-–—]?\s*.+$`)

	// Emoji and special characters that add noise
	emojiRegex = regexp.MustCompile(`[\x{1F600}-\x{1F64F}\x{1F300}-\x{1F5FF}\x{1F680}-\x{1F6FF}\x{1F1E0}-\x{1F1FF}\x{2702}-\x{27B0}\x{24C2}-\x{1F251}]`)

	// Excessive whitespace
	excessWhitespaceRegex = regexp.MustCompile(`\n{3,}`)
)



// isSponsorSegment returns true if the text contains obvious sponsor content
func isSponsorSegment(text string) bool {
	if text == "" {
		return false
	}

	sponsorKeywords := []string{
		"use code", "promo code", "discount code", "affiliate",
		"sponsored by", "brought to you by", "partner with",
		"thanks to", "special thanks", "shoutout",
		"check out", "sign up", "click the link",
		"merch", "store", "shop", "buy", "purchase",
		"deal", "offer", "coupon",
		"bluechew", "celsius", "tecovas", "perplexity",
		"expressvpn", "nordvpn", "surfshark", "raidsafe",
		"skillshare", "audible", "helix sleep", "squarespace",
		"freshly", "hello fresh", "factor", "manscaped",
		"warby parker", "third love", "bombas",
	}

	lower := strings.ToLower(text)
	for _, keyword := range sponsorKeywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}

	return false
}

// calculateQualityScore calculates a quality score for a clip based on various factors.
// Returns a score between 0.0 (low quality) and 1.0 (high quality).
func calculateQualityScore(transcript, title, description string, tags []string, duration float64, meta *clipRichMetadata) float64 {
	heuristic := calculateHeuristicQualityScore(transcript, title, description, tags, duration, meta)
	if meta != nil && meta.QualityScore > 0 {
		score := (heuristic * 0.72) + (meta.QualityScore * 0.28)
		if heuristic >= 0.82 && score < 0.78 {
			score = heuristic * 0.90
		}
		if meta.QualityScore >= 0.80 && score < 0.80 {
			score = 0.80
		}
		if heuristic < 0.35 && score > heuristic {
			score = heuristic
		}
		if score < 0 {
			score = 0
		}
		if score > 1.0 {
			score = 1.0
		}
		return score
	}
	return heuristic
}

func calculateHeuristicQualityScore(transcript, title, description string, tags []string, duration float64, meta *clipRichMetadata) float64 {
	score := 0.08

	transcriptLen := len(transcript)
	switch {
	case transcriptLen >= 1200:
		score += 0.18
	case transcriptLen >= 700:
		score += 0.14
	case transcriptLen >= 300:
		score += 0.10
	case transcriptLen >= 120:
		score += 0.06
	case transcriptLen > 0:
		score += 0.02
	}

	if title != "" {
		score += 0.03
		if len(title) > 20 {
			score += 0.03
		}
		if len(title) > 40 {
			score += 0.02
		}
	}

	switch {
	case len(tags) >= 5:
		score += 0.04
	case len(tags) >= 2:
		score += 0.03
	case len(tags) == 1:
		score += 0.01
	}

	switch {
	case duration >= 25 && duration <= 180:
		score += 0.16
	case duration >= 12 && duration <= 300:
		score += 0.08
	case duration >= 8 && duration <= 600:
		score += 0.03
	default:
		score -= 0.10
	}

	if transcriptLen < 200 {
		score -= 0.03
	}
	if duration < 20 {
		score -= 0.10
	}
	if duration < 15 {
		score -= 0.05
	}
	if duration > 240 {
		score -= 0.05
	}

	if meta != nil {
		if meta.ClipSummary != "" {
			score += 0.10
		}
		if meta.Hook != "" {
			score += 0.10
		}
		if meta.CleanTitle != "" && tagutil.NormalizeClipTag(meta.CleanTitle) != tagutil.NormalizeClipTag(title) {
			score += 0.06
		}
		switch {
		case len(meta.Topics) >= 5:
			score += 0.12
		case len(meta.Topics) >= 3:
			score += 0.10
		case len(meta.Topics) >= 2:
			score += 0.07
		case len(meta.Topics) == 1:
			score += 0.03
		}
		switch {
		case len(meta.Speakers) >= 2:
			score += 0.06
		case len(meta.Speakers) == 1:
			score += 0.03
		}
		switch {
		case len(meta.MentionedPeople) >= 2:
			score += 0.05
		case len(meta.MentionedPeople) == 1:
			score += 0.03
		}
		if len(meta.SourceTags) > 0 {
			score += 0.02
		}
		if len(meta.ClipTags) > 0 {
			score += 0.03
		}
		if len(meta.SearchKeywords) > 0 {
			score += 0.03
		}
		if len(meta.CleanTranscript) > 100 {
			score += 0.05
		}
		if len(meta.EmbeddingText) > 300 {
			score += 0.02
		}
		if duration >= 25 && duration <= 180 && meta.ClipSummary != "" && meta.Hook != "" && len(meta.Topics) >= 3 {
			score += 0.18
		}
		if duration >= 45 && duration <= 180 && len(meta.MentionedPeople) >= 1 && len(meta.SearchKeywords) >= 2 {
			score += 0.08
		}
		if duration >= 45 && duration <= 180 && meta.ClipSummary != "" && meta.Hook != "" && len(meta.Topics) >= 3 && len(meta.Speakers) >= 1 && score < 0.72 {
			score = 0.72
		}
	}

	if isSponsorSegment(transcript) {
		score -= 0.18
	}

	if score < 0 {
		score = 0
	}
	if score > 1.0 {
		score = 1.0
	}
	return score
}

// getQualityTier returns a human-readable quality tier based on the score.
func getQualityTier(score float64) string {
	switch {
	case score >= 0.80:
		return "high"
	case score >= 0.55:
		return "medium"
	case score >= 0.30:
		return "low"
	default:
		return "poor"
	}
}
