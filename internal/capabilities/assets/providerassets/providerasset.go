// Package providerassets defines the canonical, provider-agnostic model
// for a media asset discovered through an external catalog (Artlist,
// Storyblocks, Pexels, Pixabay, etc.).
//
// The types here are intentionally plain structs with no infrastructure
// dependencies so they can be shared across application and infrastructure
// packages without import cycles.
package assets

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ProviderRendition describes a single technical variant of an asset.
type ProviderRendition struct {
	// Kind is the semantic role of this rendition, e.g. "master",
	// "mezzanine", "proxy", "thumbnail", "storyboard".
	Kind string

	// Container is the file container, e.g. "mp4", "mov", "webm".
	Container string

	// VideoCodec is the video codec, e.g. "h264", "h265", "vp9".
	VideoCodec string

	// AudioCodec is the audio codec, e.g. "aac", "mp3", "opus".
	AudioCodec string

	// Width and Height are the pixel dimensions.
	Width  int
	Height int

	// FPS is the frame rate as a floating-point value when the provider
	// reports it directly. When set, it takes precedence over the rational
	// FPSNumerator/FPSDenominator pair.
	FPS float64

	// FPSNumerator and FPSDenominator describe the frame rate as a
	// rational number. Denominator 0 means "unknown".
	FPSNumerator   int
	FPSDenominator int

	// DurationMs is the duration in milliseconds.
	DurationMs int64

	// Bitrate is the average bitrate in bits per second.
	Bitrate int64

	// PixelFormat is the pixel format, e.g. "yuv420p".
	PixelFormat string

	// ColorSpace is the color space, e.g. "bt709", "bt2020".
	ColorSpace string

	// HasAudio reports whether this rendition has an audio track.
	HasAudio bool

	// URL is the publicly reachable URL for this rendition.
	URL string

	// SizeBytes is the file size in bytes, when known.
	SizeBytes int64
}

// ProviderAsset is the canonical, provider-agnostic representation of a
// media asset returned by any external catalog adapter.
type ProviderAsset struct {
	// Provider identifies the source catalog, e.g. "artlist", "pexels".
	Provider string

	// ExternalID is the provider's own asset identifier.
	ExternalID string

	// ID is retained for backward compatibility; it maps to ExternalID.
	ID string

	// Title is the human-readable title.
	Title string

	// Description is a longer human-readable description.
	Description string

	// Creator is the artist / author / contributor.
	Creator string

	// CollectionID and CollectionTitle identify the provider collection.
	CollectionID    string
	CollectionTitle string

	// PageURL is the provider's human-friendly asset page.
	PageURL string

	// ThumbnailURL is a still preview image.
	ThumbnailURL string

	// PreviewURL is a low-motion preview or playable preview.
	PreviewURL string

	// SourceRef is the primary downloadable URL (progressive, HLS, etc.).
	SourceRef string

	// DurationMs is the duration in milliseconds.
	DurationMs int64

	// Duration is retained for backward compatibility (time.Duration).
	// New code should prefer DurationMs.
	Duration time.Duration

	// Width and Height are the pixel dimensions.
	Width  int
	Height int

	// FPS is the frame rate as a floating-point value when the provider
	// reports it directly. When set, it takes precedence over the rational
	// FPSNumerator/FPSDenominator pair.
	FPS float64

	// FPSNumerator and FPSDenominator describe the frame rate.
	FPSNumerator   int
	FPSDenominator int

	// Orientation is "landscape", "portrait", "square", or empty.
	Orientation string

	// Keywords are provider-supplied tags/keywords.
	Keywords []string

	// Categories are provider-supplied category labels.
	Categories []string

	// LicenseClass is the license tier, e.g. "standard", "enterprise".
	LicenseClass string

	// ModelReleased and PropertyReleased indicate release status.
	ModelReleased    *bool
	PropertyReleased *bool

	// SourceName is retained for backward compatibility; it maps to Provider.
	SourceName string

	// MediaType is the canonical media type (video, image, music, etc.).
	MediaType asset.MediaType

	// PublishedAt is the provider publication time, when available.
	PublishedAt *time.Time

	// Score is a provider-specific relevance score.
	Score float64

	// Renditions lists the technical variants available for this asset.
	Renditions []ProviderRendition

	// RawMetadata holds any provider-specific fields not mapped above.
	RawMetadata map[string]any
}

// SearchRequest is the canonical query passed to a ProviderAdapter.
type SearchRequest struct {
	// Query is the free-form search query.
	Query string

	// Limit caps the number of results.
	Limit int

	// NextPageToken is an opaque pagination token.
	NextPageToken string
}

// SearchResult is the canonical reply from a ProviderAdapter.
type SearchResult struct {
	Assets        []ProviderAsset
	NextPageToken string
}

// ProviderAdapter is the unified interface implemented by every external
// catalog adapter (Artlist, Storyblocks, Pexels, Pixabay, etc.).
type ProviderAdapter interface {
	// Name returns the provider name, e.g. "pexels".
	Name() string

	// Search queries the provider catalog and returns canonical assets.
	Search(ctx context.Context, req SearchRequest) (SearchResult, error)
}
