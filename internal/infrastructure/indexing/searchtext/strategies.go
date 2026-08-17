package searchtext

import (
	"fmt"
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

// youtubeStrategy builds the canonical YouTube clip search text from
// the full set of available SearchTextInput fields:
//
//	title + transcript + channel + description + tags + detected_entities +
//	hook + speakers + mentioned_people
//
// Fields that are not applicable (zero-value) are silently dropped.
// The strategy reads Additional keys for YouTube-specific metadata_json
// fields that don't have a dedicated SearchTextInput slot:
//
//	hook, speakers, mentioned_people
//
// godlike/06 SSOT: this function is the SOLE canonical owner of the
// YouTube search_text format for the Qdrant BM25 indexing path.
func youtubeStrategy(input appsearchtext.SearchTextInput) string {
	add := input.Additional

	// Additional fields from metadata_json (populated by PayloadMapper).
	hook := strings.TrimSpace(add["hook"])
	speakers := strings.TrimSpace(add["speakers"])
	mentionedPeople := strings.TrimSpace(add["mentioned_people"])

	// Multilingual transcripts: append translations from TextTracks.
	// The original transcript is always included first; translations
	// from configured index_languages follow so the E5 multilingual
	// embedding model can match queries in any supported language.
	// When IndexLanguages is set in Additional, only tracks matching
	// those languages are included (prevents embedding pollution from
	// languages the operator didn't configure).
	indexLangs := parseIndexLanguages(add["index_languages"])
	var transcriptParts []string
	if input.Transcript != "" {
		transcriptParts = append(transcriptParts, truncate(input.Transcript, maxTranscriptChars))
	}
	for _, tt := range input.TextTracks {
		if tt.TextKind != "transcript" || tt.Text == "" || tt.Text == input.Transcript {
			continue
		}
		if len(indexLangs) > 0 && !indexLangs[strings.ToLower(tt.LanguageCode)] {
			continue
		}
		transcriptParts = append(transcriptParts, truncate(tt.Text, maxTranscriptChars))
	}

	return joinNonEmpty(" ",
		input.Title,
		joinNonEmpty(" ", transcriptParts...),
		input.Channel,
		truncate(input.Description, maxDescriptionChars),
		joinTags(input.Tags),
		joinTags(input.DetectedEntities),
		hook,
		speakers,
		mentionedPeople,
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

// stockChunkStrategy builds search text for a stock-pipeline chunk
// (per-chunk metadata from the stock finalizer). It composes the
// canonical sentence pattern:
//
//	"Stock video from {event} round {N}. {subject} {action}.
//	 Segment {start}s to {end}s. Tags: {tags}."
//
// Stock-specific fields (event, round, subject, action, start_sec,
// end_sec) are read from SearchTextInput.Additional under
// the keys listed in stockChunkStrategyAdditionalKeys. Title, Tags
// and Category come from the typed top-level fields so the consumer
// (e.g. the finalizer in
// internal/application/assets/providers/stock/stockpipeline) only
// needs to populate the standard DTO fields + Additional.
//
// godlike/07 minimum-blast-radius: each labelled segment is dropped
// silently when its inputs are empty — never emit "Stock video from
//
//	round ." or "Tags: " fragments. godlike/06 SSOT: this function
//
// is the SOLE canonical owner of the stock-chunk text format; other
// callers MUST NOT re-derive the same composition.
func stockChunkStrategy(input appsearchtext.SearchTextInput) string {
	add := input.Additional

	event := strings.TrimSpace(add["event"])
	roundN := strings.TrimSpace(add["round"])
	subject := strings.TrimSpace(add["subject"])
	action := strings.TrimSpace(add["action"])
	startSec := strings.TrimSpace(add["start_sec"])
	endSec := strings.TrimSpace(add["end_sec"])

	// ── Mandatory prefix: title + category ──────────────────────
	prefix := joinNonEmpty(" ", input.Title, input.Category)

	// ── Segment 1: "Stock video from {event} round {N}" ─────────
	seg1 := composeStockHeader(event, roundN)

	// ── Segment 2: "{subject} {action}" ──────────────────────────
	seg2 := joinNonEmpty(" ", subject, action)

	// ── Segment 3: "Segment {start}s to {end}s" ──────────────────
	// godlike/07 NO-FAKE-AVAILABILITY: emit only when BOTH endpoints
	// are present — a one-sided "Segment 10.0s to s" or "Segment s
	// to 20.0s" would be a malformed sentence that pollutes the
	// Qdrant BM25 channel. Drop the segment cleanly when either
	// endpoint is missing (mirrors seg1/seg4/seg5 graceful-drop).
	var seg3 string
	if startSec != "" && endSec != "" {
		seg3 = fmt.Sprintf("Segment %ss to %ss", startSec, endSec)
	}

	// ── Segment 4: "Tags: {tags}" ────────────────────────────────
	var seg4 string
	if len(input.Tags) > 0 {
		seg4 = "Tags: " + joinTags(input.Tags)
	}

	return joinNonEmpty(" ", prefix, seg1, seg2, seg3, seg4)
}

// composeStockHeader renders "Stock video from {event} round {N}".
// godlike/07 minimum-blast-radius: drops the leading "from" or
// trailing "round N" cleanly when either input is empty so we
// never emit "Stock video from  round ." fragments. The
// "round-only" branch uses a comma separator to keep the phrase
// grammatically clean even when the event is unknown.
func composeStockHeader(event, roundN string) string {
	switch {
	case event != "" && roundN != "":
		return "Stock video from " + event + " round " + roundN
	case event != "":
		return "Stock video from " + event
	case roundN != "":
		return "Stock video, round " + roundN
	default:
		return ""
	}
}

// ── Helpers ─────────────────────────────────────────────────────────────

// parseIndexLanguages parses a comma-separated list of language codes
// into a lookup map. Empty input yields an empty map. Whitespace is
// trimmed from each code.
func parseIndexLanguages(s string) map[string]bool {
	langs := make(map[string]bool)
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			langs[p] = true
		}
	}
	return langs
}

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
