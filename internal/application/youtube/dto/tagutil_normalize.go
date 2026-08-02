package dto

import (
	"strings"

	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// NormalizeClipTagList normalizes, filters generic tags, and deduplicates a
// tag list in first-seen order.
func NormalizeClipTagList(tags []string) []string {
	return sliceutil.NormalizeAndDedupe(tags, NormalizeClipTag, IsGenericClipTag)
}

// MergeTagLists normalizes, filters generic tags, and merges multiple tag
// lists into a single deduplicated slice.
func MergeTagLists(lists ...[]string) []string {
	return sliceutil.MergeNormalizedListsVariadic(NormalizeClipTag, IsGenericClipTag, lists...)
}

// NormalizeClipTag normalizes a single tag: lowercases, replaces separators,
// collapses whitespace.
func NormalizeClipTag(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	tag = strings.ReplaceAll(tag, "_", " ")
	tag = strings.ReplaceAll(tag, "-", " ")
	tag = strings.Join(strings.Fields(tag), " ")
	return tag
}

// ContainsNormalized checks whether a normalized target string appears in a
// list after normalization.
func ContainsNormalized(list []string, target string) bool {
	target = NormalizeClipTag(target)
	for _, item := range list {
		if NormalizeClipTag(item) == target {
			return true
		}
	}
	return false
}

// IsGenericClipTag returns true for tags that are too generic to be useful
// (e.g. "video", "podcast", "youtube").
func IsGenericClipTag(tag string) bool {
	switch tag {
	case "", "video", "clip", "clips", "youtube", "yt", "http", "https", "www", "nbsp", "subscribe":
		return true
	}
	for _, fragment := range []string{"youtube", "clip", "official video", "shorts", "highlights"} {
		if strings.Contains(tag, fragment) {
			return true
		}
	}
	return false
}

// IsGenericPersonPhrase is retained for API compatibility. Person, venue,
// show and sponsor decisions belong to channel configuration, not this
// global normalizer.
func IsGenericPersonPhrase(tag string) bool {
	_ = tag
	return false
}

// NormalizeSemanticText lowercases, strips punctuation/HTML, filters short
// words and generic tokens, and returns a cleaned token string.
func NormalizeSemanticText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.NewReplacer(
		"&gt;", " ", "&nbsp;", " ", "https://", " ", "http://", " ",
		",", " ", ".", " ", "!", " ", "?", " ", ";", " ", ":", " ",
		"(", " ", ")", " ", "[", " ", "]", " ", "-", " ", "_", " ",
		"\"", " ", "'", " ", "/", " ", "\\", " ", "|", " ", "#", " ",
	).Replace(text)
	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}
	filtered := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 3 || IsGenericToken(w) {
			continue
		}
		filtered = append(filtered, w)
	}
	return strings.Join(filtered, " ")
}

// IsGenericToken delegates linguistic data to the configured lexicon and
// keeps only provider-neutral URL/HTML boilerplate locally.
func IsGenericToken(token string) bool {
	switch token {
	case "http", "https", "www", "nbsp", "subscribe":
		return true
	default:
		return textutil.IsStopWord(token)
	}
}
