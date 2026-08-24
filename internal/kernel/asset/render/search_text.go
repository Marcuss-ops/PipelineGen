// Package asset — SearchTextComposer port (PR-CANONICAL-SEARCHTEXT-PORT,
// July 2026).
//
// godlike/06 SSOT (one canonical owner per fact): this file is the
// SOLE canonical owner of the SearchTextInput type + SearchTextComposer
// interface + per-source Strategy function type + ComposerRegistry
// dispatcher. Every pipeline (YouTube, Stock, Artlist, Voiceover, Image,
// GeneratedImage) MUST compose search text through this registry instead
// of inline ad-hoc concatenations.
//
// The application-layer port (internal/application/indexing/searchtext)
// references these types; the infrastructure-layer registry
// (internal/infrastructure/indexing/searchtext) delegates to these
// strategies. YouTube and Stock map their pipeline-specific DTOs
// (CanonicalClipMetadata, ClipPlan) to SearchTextInput at the adapter
// boundary.
//
// godlike/07 minimum-blast-radius: this is an additive-only domain
// surface. Existing inline composers (composeYouTubeClipSearchText,
// composeStockChunkSearchText) continue working as-is; callers that
// adopt the registry deprecate their local derivation over time.
package render

import (
	"fmt"
	"strings"
)

// SearchTextInput carries every field that a strategy might use to
// assemble the canonical search text for an asset. Fields that are
// not applicable to a given source are left at their zero values;
// each strategy only reads the subset it documents.
//
// godlike/06 SSOT: this type is the canonical owner-of-fact for the
// search-text composition input shape. The application-layer
// SearchTextInput (internal/application/indexing/searchtext) is a
// type alias to this domain type (or will be migrated to reference
// it in a follow-up PR).
type SearchTextInput struct {
	// AssetID is the canonical media_assets.id. Used for error messages.
	AssetID string

	// Source discriminates the strategy: "youtube", "artlist", "voiceover",
	// "image", "generated_image", "stock". The registry dispatches on this
	// field.
	Source string

	// MediaType is the canonical media type (video, audio, image, document).
	MediaType string

	// ── Freetext fields ──────────────────────────────────────────────

	// Title is the human-readable title. Sources: media_assets.name or
	// metadata_json.title. Used by every strategy.
	Title string

	// Description is a longer prose description. Sources: metadata_json
	// .description, metadata_json.caption, or clip summary.
	Description string

	// Summary is the YouTube clip summary (distinct from Description
	// which is the long-form prose). Used by YouTube Step 9.
	Summary string

	// Transcript is the Whisper-derived transcript text. Used by YouTube,
	// Voiceover.
	Transcript string

	// Prompt is the AI generation prompt. Used by Image, GeneratedImage.
	Prompt string

	// Caption is a short human-written caption. Used by Image.
	Caption string

	// Hook is the YouTube clip hook (attention-grabbing opener). Used
	// by YouTube.
	Hook string

	// ── Structured fields ────────────────────────────────────────────

	// Tags are the canonical tag list (media_assets.tags). Used by every
	// strategy.
	Tags []string

	// Category is the asset category (media_assets.category). Used by
	// Artlist, Image, Stock.
	Category string

	// Language is the BCP-47 language tag. Used by Voiceover.
	Language string

	// Channel is the YouTube channel name. Used by YouTube.
	Channel string

	// Topic is the voiceover topic / script section label. Used by
	// Voiceover.
	Topic string

	// DetectedEntities are visual entities detected by the image analysis
	// pipeline. Used by Image, GeneratedImage.
	DetectedEntities []string

	// OriginProvider is the originating service (e.g. "dall-e", "midjourney",
	// "stable-diffusion"). Used by Image, GeneratedImage.
	OriginProvider string

	// Speakers are the people speaking in the YouTube clip. Used by YouTube.
	Speakers []string

	// MentionedPeople are people mentioned (but not speaking) in the YouTube
	// clip. Used by YouTube.
	MentionedPeople []string

	// Topics are the topic labels for the YouTube clip (distinct from Topic
	// which is the voiceover section label). Used by YouTube.
	Topics []string

	// SourceURL is the original source URL. Used by YouTube, Stock.
	SourceURL string

	// Additional is a free-form map for source-specific extras. Strategies
	// may inspect keys they document; unknown keys are ignored silently.
	// Prefer typed fields above over Additional when possible.
	Additional map[string]string
}

// SearchTextComposer is the canonical port interface for composing
// search text for any asset source type. Implementations dispatch on
// SearchTextInput.Source to a per-source strategy.
//
// godlike/06 SSOT: this interface is the SOLE canonical owner of the
// search-text composition contract. The application-layer
// SearchTextBuilder (internal/application/indexing/searchtext) will
// become a type alias to this domain interface in a follow-up PR.
type SearchTextComposer interface {
	// Compose assembles the canonical search text for the given input.
	// Returns (text, nil) even for unrecognised sources (fallback
	// strategy). Returns ("", error) only when the input is fundamentally
	// invalid (empty Source).
	Compose(input SearchTextInput) (string, error)
}

// SearchTextStrategy is a per-source search-text builder function. It
// receives the full SearchTextInput and returns the assembled text.
// The zero-value string is valid (empty search text → BM25 channel
// is dropped by the payload mapper).
//
// Strategies never error. An empty return value is valid. Strategies
// only read the subset of fields they document; unknown fields are
// ignored silently.
type SearchTextStrategy func(input SearchTextInput) string

// ComposerRegistry dispatches Compose calls to the strategy registered
// for the asset's Source. Unrecognised sources fall back to the
// default strategy (title + tags join).
//
// godlike/06 SSOT: this is the SOLE canonical registry for search-text
// composition. The infrastructure-layer Registry
// (internal/infrastructure/indexing/searchtext) delegates to this
// domain-level registry.
type ComposerRegistry struct {
	strategies map[string]SearchTextStrategy
}

// NewComposerRegistry creates a ComposerRegistry with the six canonical
// strategies pre-registered: youtube, artlist, voiceover, image,
// generated_image, stock.
func NewComposerRegistry() *ComposerRegistry {
	return &ComposerRegistry{
		strategies: map[string]SearchTextStrategy{
			"youtube":         youtubeSearchTextStrategy,
			"artlist":         artlistSearchTextStrategy,
			"voiceover":       voiceoverSearchTextStrategy,
			"image":           imageSearchTextStrategy,
			"generated_image": generatedImageSearchTextStrategy,
			"stock":           stockSearchTextStrategy,
		},
	}
}

// Register adds or replaces a strategy for the given source. The source
// key is compared case-sensitively. Pass nil to remove a source (it will
// then fall back to the default strategy at dispatch time).
func (r *ComposerRegistry) Register(source string, s SearchTextStrategy) {
	if s == nil {
		delete(r.strategies, source)
		return
	}
	r.strategies[source] = s
}

// Compose dispatches to the registered strategy. Unrecognised sources
// use the defaultFallback strategy (title + tags join). The only hard
// error is an empty Source.
func (r *ComposerRegistry) Compose(input SearchTextInput) (string, error) {
	if input.Source == "" {
		return "", fmt.Errorf("asset.ComposerRegistry.Compose: Source must not be empty")
	}
	s, ok := r.strategies[input.Source]
	if !ok {
		s = defaultSearchTextFallback
	}
	return s(input), nil
}

// Compile-time assertion: ComposerRegistry satisfies SearchTextComposer.
var _ SearchTextComposer = (*ComposerRegistry)(nil)

// ── Per-source strategies ───────────────────────────────────────────

// Truncation limits for long text fields. Transcripts and descriptions
// can be hundreds of KB; truncation keeps the search_text column bounded
// while preserving the most relevant prefix.
const (
	MaxTranscriptChars  = 2000
	MaxDescriptionChars = 1000
	MaxPromptChars      = 1500
	MaxCaptionChars     = 500
)

// youtubeSearchTextStrategy builds the canonical YouTube clip search text:
//
//	title + summary + hook + topics + source_url + speakers + mentioned_people
//
// This mirrors composeYouTubeClipSearchText's DoD 10 priority order
// exactly. Transcript and channel are intentionally excluded (deferred
// to the youtube.rebuild_search_text post-enrichment job).
//
// NOTE: this mirrors Step 9 (composeYouTubeClipSearchText at
// internal/application/youtube/usecase/process_segment_helpers.go:123).
// For post-enrichment text including Transcript and Channel, use the
// infra-layer youtubeStrategy at
// internal/infrastructure/indexing/searchtext/strategies.go. The two
// strategies produce DIFFERENT output for the same Source — this is
// intentional (Step 9 vs post-enrichment text).
//
// godlike/06 SSOT: this function is the domain-level canonical owner of
// the YouTube search_text format at Step 9.
func youtubeSearchTextStrategy(input SearchTextInput) string {
	return joinSearchTextNonEmpty(" ",
		input.Title,
		input.Summary,
		input.Hook,
		joinSearchTextTags(input.Topics),
		input.SourceURL,
		joinSearchTextTags(input.Speakers),
		joinSearchTextTags(input.MentionedPeople),
	)
}

// artlistSearchTextStrategy builds search text from:
//
//	title + tags + category + description
func artlistSearchTextStrategy(input SearchTextInput) string {
	return joinSearchTextNonEmpty(" ",
		input.Title,
		joinSearchTextTags(input.Tags),
		input.Category,
		truncateSearchText(input.Description, MaxDescriptionChars),
	)
}

// voiceoverSearchTextStrategy builds search text from:
//
//	title + transcript + language + topic
func voiceoverSearchTextStrategy(input SearchTextInput) string {
	return joinSearchTextNonEmpty(" ",
		input.Title,
		truncateSearchText(input.Transcript, MaxTranscriptChars),
		input.Language,
		input.Topic,
	)
}

// imageSearchTextStrategy builds search text from:
//
//	prompt + caption + detected entities + tags + origin provider
func imageSearchTextStrategy(input SearchTextInput) string {
	return joinSearchTextNonEmpty(" ",
		truncateSearchText(input.Prompt, MaxPromptChars),
		truncateSearchText(input.Caption, MaxCaptionChars),
		joinSearchTextTags(input.DetectedEntities),
		joinSearchTextTags(input.Tags),
		input.OriginProvider,
		input.Category,
	)
}

// generatedImageSearchTextStrategy builds search text from:
//
//	prompt + caption + detected entities + tags + origin provider
//
// Same formula as imageStrategy; the separate entry allows future
// divergence without changing the image path.
func generatedImageSearchTextStrategy(input SearchTextInput) string {
	return imageSearchTextStrategy(input)
}

// stockSearchTextStrategy builds search text for a stock-pipeline chunk.
// Format:
//
//	"Stock video from {event} round {N}. {subject} {action}.
//	 Segment {start}s to {end}s. Tags: {tags}."
//
// Stock-specific fields (event, round, subject, action, source_url,
// start_sec, end_sec) are read from Additional map keys. Title, Tags
// and Category come from the typed top-level fields.
//
// godlike/07 minimum-blast-radius: each labelled segment is dropped
// silently when its inputs are empty.
func stockSearchTextStrategy(input SearchTextInput) string {
	add := input.Additional

	event := strings.TrimSpace(add["event"])
	roundN := strings.TrimSpace(add["round"])
	subject := strings.TrimSpace(add["subject"])
	action := strings.TrimSpace(add["action"])
	sourceURL := strings.TrimSpace(add["source_url"])
	startSec := strings.TrimSpace(add["start_sec"])
	endSec := strings.TrimSpace(add["end_sec"])

	// Mandatory prefix: title + category
	prefix := joinSearchTextNonEmpty(" ", input.Title, input.Category)

	// Segment 1: "Stock video from {event} round {N}"
	seg1 := composeStockSearchHeader(event, roundN)

	// Segment 2: "{subject} {action}"
	seg2 := joinSearchTextNonEmpty(" ", subject, action)

	// Segment 3: "Segment {start}s to {end}s"
	// godlike/07 NO-FAKE-AVAILABILITY: emit only when BOTH endpoints
	// are present.
	var seg3 string
	if startSec != "" && endSec != "" {
		seg3 = fmt.Sprintf("Segment %ss to %ss", startSec, endSec)
	}

	// Segment 4: "Tags: {tags}"
	var seg4 string
	if len(input.Tags) > 0 {
		seg4 = "Tags: " + joinSearchTextTags(input.Tags)
	}

	// Segment 5: "Source: {source_url}"
	var seg5 string
	if sourceURL != "" {
		seg5 = "Source: " + sourceURL
	}

	return joinSearchTextNonEmpty(" ", prefix, seg1, seg2, seg3, seg4, seg5)
}

// composeStockSearchHeader renders "Stock video from {event} round {N}".
// Drops leading "from" or trailing "round N" cleanly when either input
// is empty. The "round-only" branch uses a comma separator.
func composeStockSearchHeader(event, roundN string) string {
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

// defaultSearchTextFallback joins title + tags, intended as the safe
// floor for unrecognised or future source types.
func defaultSearchTextFallback(input SearchTextInput) string {
	return joinSearchTextNonEmpty(" ", input.Title, joinSearchTextTags(input.Tags))
}

// ── Helpers ─────────────────────────────────────────────────────────

// joinSearchTextNonEmpty joins non-empty strings with the given separator.
// Empty strings are silently skipped.
func joinSearchTextNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

// joinSearchTextTags joins a tag list into a single space-separated string.
// Empty tags are skipped. Returns "" for nil or empty slices.
func joinSearchTextTags(tags []string) string {
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

// NOTE: joinSearchTextTags is used for both tags and string lists
// (speakers, topics). The function is generic over []string; callers
// pass the appropriate slice.

// truncateSearchText returns s truncated to maxLen runes. Returns s
// unchanged when the rune count is <= maxLen or maxLen <= 0. Uses
// []rune conversion so multi-byte UTF-8 characters are never split.
func truncateSearchText(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
