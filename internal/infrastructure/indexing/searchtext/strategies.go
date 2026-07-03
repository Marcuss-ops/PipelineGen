package searchtext

import (
	"strings"

	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/application/indexing/searchtext"
)

// Truncation limits for long text fields. Transcripts and descriptions
// can be hundreds of KB; truncation keeps the search_text column bounded
// while preserving the most relevant prefix.
const (
	maxTranscriptChars  = 2000
	maxDescriptionChars = 1000
	maxPromptChars      = 1500
	maxCaptionChars     = 500
)

// ── Per-source strategies ───────────────────────────────────────────────
//
// Each strategy documents the canonical list of fields it reads from
// SearchTextInput. The output is a single space-joined string suitable
// for storage in media_assets.search_text and later embedding for the
// Qdrant BM25 sparse channel.
//
// Design rule: strategies never error. An empty return value is valid
// (the PayloadMapper drops the BM25 channel when SearchText is empty).
// Strategies only read the subset of fields they document; unknown
// fields are ignored silently.

// youtubeStrategy builds search text from:
//
//	title + transcript + channel + description
//
// Follows the documented YouTube formula from the original plan.
func youtubeStrategy(input appsearchtext.SearchTextInput) string {
	return joinNonEmpty(" ",
		input.Title,
		truncate(input.Transcript, maxTranscriptChars),
		input.Channel,
		truncate(input.Description, maxDescriptionChars),
	)
}

// artlistStrategy builds search text from:
//
//	title + tags + category + description
//
// Tags are joined with spaces. Follows the MergeMetadataSearchText
// precedent from internal/infrastructure/ai/semantic/semantic.go.
func artlistStrategy(input appsearchtext.SearchTextInput) string {
	return joinNonEmpty(" ",
		input.Title,
		joinTags(input.Tags),
		input.Category,
		truncate(input.Description, maxDescriptionChars),
	)
}

// voiceoverStrategy builds search text from:
//
//	title + transcript + language + topic
//
// Transcript for voiceover is the spoken text being synthesised.
func voiceoverStrategy(input appsearchtext.SearchTextInput) string {
	return joinNonEmpty(" ",
		input.Title,
		truncate(input.Transcript, maxTranscriptChars),
		input.Language,
		input.Topic,
	)
}

// imageStrategy builds search text from:
//
//	prompt + caption + detected entities + tags + origin provider
//
// This is the canonical strategy for sourced images (stock, scraped,
// manually uploaded).
func imageStrategy(input appsearchtext.SearchTextInput) string {
	return joinNonEmpty(" ",
		truncate(input.Prompt, maxPromptChars),
		truncate(input.Caption, maxCaptionChars),
		joinTags(input.DetectedEntities),
		joinTags(input.Tags),
		input.OriginProvider,
		input.Category,
	)
}

// generatedImageStrategy builds search text from:
//
//	prompt + caption + detected entities + tags + origin provider
//
// Same formula as imageStrategy: both image source types share the
// same canonical text-building path. The separate strategy entry
// allows future divergence (e.g. generated images may add a model
// identifier or generation seed) without changing the image path.
func generatedImageStrategy(input appsearchtext.SearchTextInput) string {
	return imageStrategy(input)
}

// ── Helpers ─────────────────────────────────────────────────────────────

// joinNonEmpty joins non-empty strings with the given separator.
// Empty strings are silently skipped.
func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

// joinTags joins a tag list into a single space-separated string.
// Empty tags are skipped. Returns "" for nil or empty slices.
func joinTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	var kept []string
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, " ")
}

// truncate returns s truncated to maxLen runes. Returns s unchanged
// when the rune count is <= maxLen or maxLen <= 0. Uses []rune
// conversion so multi-byte UTF-8 characters are never split.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
