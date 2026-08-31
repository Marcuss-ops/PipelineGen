// Package searchtext defines the canonical SearchTextBuilder port.
//
// Pattern 0 (Port abstraction layer, June 2026): every asset source type
// must build its search text through a single registry-backed interface
// instead of scattered ad-hoc concatenations. The interface lives in the
// application layer so consumers (indexing pipeline, backfill command,
// clip indexer) depend on the contract, not on concrete strategies.
//
// The concrete registry + per-source strategies live in
// internal/capabilities/indexing/searchtext/. A compile-time assertion
// there locks the port-implementation contract.
package indexing

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// TextTrackEntry carries a single localized text resource for
// inclusion in the search text. Strategies iterate over TextTracks
// to build multilingual embeddings.
type TextTrackEntry struct {
	// LanguageCode is the BCP-47 language tag (e.g. "en", "it", "pt-BR").
	LanguageCode string
	// Text is the localized text content (transcript, description, etc.).
	Text string
	// TextKind classifies the content ("transcript", "description", etc.).
	TextKind string
}

// SearchTextInput carries every field that a strategy might use to
// assemble the canonical search text for an asset. Fields that are
// not applicable to a given source are left at their zero values;
// each strategy only reads the subset it documents.
type SearchTextInput struct {
	// AssetID is the canonical media_assets.id. Used for error messages.
	AssetID string

	// Source discriminates the strategy: "youtube", "artlist", "voiceover",
	// "image", "generated_image". The registry dispatches on this field.
	Source string

	// MediaType is the canonical media type (video, audio, image, document).
	MediaType string

	// ── Freetext fields ──────────────────────────────────────────────

	// Title is the human-readable title. Sources: media_assets.name or
	// metadata_json.title. Used by every strategy.
	Title string

	// Description is a longer prose description. Sources: metadata_json.description,
	// metadata_json.caption, or clip summary. Used by YouTube, Artlist, Image.
	Description string

	// Transcript is the Whisper-derived transcript text. Used by YouTube,
	// Voiceover.
	Transcript string

	// Prompt is the AI generation prompt. Used by Image, GeneratedImage.
	Prompt string

	// Caption is a short human-written caption. Used by Image.
	Caption string

	// ── Structured fields ────────────────────────────────────────────

	// Tags are the canonical tag list (media_assets.tags). Used by every strategy.
	Tags []string

	// Category is the asset category (media_assets.category). Used by Artlist, Image.
	Category string

	// Language is the BCP-47 language tag. Used by Voiceover.
	Language string

	// Channel is the YouTube channel name. Used by YouTube.
	Channel string

	// Topic is the voiceover topic / script section label. Used by Voiceover.
	Topic string

	// DetectedEntities are visual entities detected by the image analysis pipeline.
	// Used by Image, GeneratedImage.
	DetectedEntities []string

	// OriginProvider is the originating service (e.g. "dall-e", "midjourney",
	// "stable-diffusion"). Used by Image, GeneratedImage.
	OriginProvider string

	// TextTracks carries localized text resources (transcripts,
	// descriptions) from asset_text_tracks. Used by YouTube for
	// multilingual embedding construction.
	TextTracks []TextTrackEntry

	// Additional is a free-form map for source-specific extras. Strategies
	// may inspect keys they document; unknown keys are ignored silently.
	Additional map[string]string
}

// SearchTextBuilder builds the canonical search text for an asset. The
// returned string is what gets stored in media_assets.search_text and
// later embedded for Qdrant's BM25 sparse channel.
//
// The implementation is a registry that dispatches on Source to a
// per-source strategy. Unrecognised sources produce a non-empty fallback
// (title + tags) and return nil error so the indexing pipeline never
// blocks on a missing strategy.
type SearchTextBuilder interface {
	// Build assembles the canonical search text for the given input.
	// Returns (text, nil) even for unrecognised sources (fallback strategy).
	// Returns ("", error) only when the input is fundamentally invalid
	// (nil input, empty AssetID).
	Build(ctx context.Context, input SearchTextInput) (string, error)
}

// SearchDocumentBuilder assembles the canonical search text directly
// from an asset's structured fields. It is source-aware but favours
// deterministic, metadata-driven composition over AI-generated text,
// preventing contamination between ProviderTags, VLMTags, and the
// aggregated Tags list.
//
// The returned string is what gets stored in media_assets.search_text.
type SearchDocumentBuilder interface {
	// Build assembles the search text from the asset fields.
	// Returns (text, nil) even when most fields are empty; an empty
	// asset (no AssetID) is the only error condition.
	Build(ctx context.Context, a asset.Asset) (string, error)
}
