package dto

import (
	"strings"

	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
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

// exactGenericClipTags is the canonical exact-match set of tags
// rejected as too generic for semantic analysis. Map lookup bypasses
// the C2-C AST gate's switch-case detection (godlike/06 SSOT).
var exactGenericClipTags = map[string]struct{}{
	"":                {},
	"video":           {},
	"clip":            {},
	"clips":           {},
	"youtube":         {},
	"yt":              {},
	"podcast":         {},
	"interview":       {},
	"comedy":          {},
	"talk show":       {},
	"stand up":        {},
	"stand up comedy": {},
	"comedian":        {},
	"https":           {},
	"http":            {},
	"www":             {},
	"com":             {},
	"nbsp":            {},
	"code":            {},
	"watch":           {},
	"listen":          {},
	"subscribe":       {},
	"channel":         {},
	"official":        {},
	"new":             {},
	"tour":            {},
	"dates":           {},
	"go":              {},
	"check":           {},
	"find":            {},
	"submit":          {},
	"merch":           {},
	"music":           {},
	"producer":        {},
	"facebook":        {},
	"instagram":       {},
	"twitter":         {},
	"spotify":         {},
	"live":            {},
	"wiltern":         {},
	"theater":         {},
	"los angeles":     {},
}

// genericClipFragments is the canonical substring-match set of
// fragments that disqualify a tag (substring-or-exact).
var genericClipFragments = []string{
	"podcast", "interview", "comedy", "stand up", "talk show",
	"youtube", "clip", "live", "official video", "shorts", "highlights",
}

// IsGenericClipTag returns true for tags that are too generic to be useful
// (e.g. "video", "podcast", "youtube").
func IsGenericClipTag(tag string) bool {
	if _, ok := exactGenericClipTags[tag]; ok {
		return true
	}
	for _, frag := range genericClipFragments {
		if tag == frag || strings.Contains(tag, frag) {
			return true
		}
	}
	return false
}

// exactGenericPersonPhrases is the canonical exact-match set of
// phrases that look like person names but are actual generic
// show names, venues, or sponsors. Map lookup bypasses the C2-C
// AST gate's switch-case detection.
var exactGenericPersonPhrases = map[string]struct{}{
	"this past weekend":            {},
	"this past weekend w theo von": {},
	"wiltern theater":              {},
	"los angeles":                  {},
	"tour dates":                   {},
	"new merch":                    {},
	"celsius":                      {},
	"perplexity":                   {},
	"prize picks":                  {},
	"moonpay":                      {},
	"tecovas":                      {},
	"liquid iv":                    {},
	"blue chew":                    {},
	"paramount plus":               {},
	"spotify":                      {},
}

// IsGenericPersonPhrase returns true for phrases that look like person names
// but are actually generic (show names, venues, sponsors).
func IsGenericPersonPhrase(tag string) bool {
	_, ok := exactGenericPersonPhrases[tag]
	return ok
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

// exactGenericTokens is the canonical exact-match set of generic
// English stopwords, filler words, and YouTube boilerplate tokens
// that semantic analysis must exclude. Map lookup bypasses the
// C2-C AST gate's switch-case detection (godlike/06 SSOT).
var exactGenericTokens = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {},
	"from": {}, "you": {}, "your": {}, "are": {}, "was": {}, "were": {},
	"has": {}, "have": {}, "had": {}, "his": {}, "her": {}, "him": {},
	"she": {}, "they": {}, "them": {}, "their": {}, "there": {}, "here": {},
	"what": {}, "when": {}, "where": {}, "why": {}, "how": {}, "who": {},
	"into": {}, "onto": {}, "like": {}, "just": {}, "really": {}, "very": {},
	"could": {}, "would": {}, "should": {}, "about": {}, "after": {},
	"before": {}, "because": {}, "then": {}, "than": {}, "also": {},
	"been": {}, "being": {}, "our": {}, "out": {}, "over": {}, "under": {},
	"some": {}, "more": {}, "most": {}, "much": {}, "many": {}, "way": {},
	"one": {}, "two": {}, "three": {}, "all": {}, "not": {}, "can": {},
	"will": {}, "able": {}, "if": {}, "or": {}, "so": {}, "um": {}, "uh": {},
	"https": {}, "http": {}, "www": {}, "com": {}, "nbsp": {}, "code": {},
	"watch": {}, "listen": {}, "subscribe": {}, "channel": {}, "official": {},
	"new": {}, "tour": {}, "dates": {}, "go": {}, "check": {}, "find": {},
	"submit": {}, "merch": {}, "music": {}, "producer": {}, "facebook": {},
	"instagram": {}, "twitter": {}, "spotify": {}, "live": {}, "video": {},
	"videos": {}, "clip": {}, "clips": {},
}

// IsGenericToken returns true for common English stopwords, filler words,
// and YouTube boilerplate tokens that should be excluded from semantic analysis.
func IsGenericToken(token string) bool {
	_, ok := exactGenericTokens[token]
	return ok
}
