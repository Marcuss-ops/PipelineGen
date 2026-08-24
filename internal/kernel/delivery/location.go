// Package delivery defines the canonical semantic-location DTO used by
// every HTTP endpoint and worker job that publishes an asset to Drive.
//
// godlike/06 SSOT: this package is the SOLE owner of the typed-contract
// shape of "where does this asset semantically live". Companion type
// internal/platform/delivery.PublishRequest is the post-mapper
// canonical form that the Publisher consumes (the same package also owns
// the BuildPublishRequest mapper that translates AssetLocationInput into
// the per-destination fields of PublishRequest).
//
// Wave 1 of SEMANTIC-LOCATION-API-2026-07-06 places this file here as
// the canonical foundation; future Wave 2..N migrations of the image /
// stock / voiceover / youtube / books / lessons / artlist / sfx / register
// endpoint families will consume this struct verbatim.
package delivery

// AssetLocationInput is the canonical semantic-location DTO. Endpoints,
// worker payloads, and CLI flags marshal into this shape; the
// per-destination PublishRequest is built from it via
// delivery.BuildPublishRequest.
//
// Field-by-field semantics (godlike/07 NO-FAKE-AVAILABILITY):
//
//   - Category groups assets under a logical taxonomy (e.g. "Boxe",
//     "Personaggi"). Required by Stock (StockPath category segment),
//     Artlist (search-term segment), SoundEffect (category segment),
//     optional by YouTube (channel-group), supported by Wave 1 for
//     forward-compatible mapping.
//
//   - Subject identifies the subject within the group. Required by
//     Image (Subject segment), YouTube (video_id segment), Stock
//     (3rd segment), Artlist (asset_id segment), Document (asset_id slug).
//
//   - Name is an optional human-friendly alternative to Subject. The
//     mapper prefers Subject over Name when both are non-empty (caller
//     precedence). When Subject is empty AND Name is non-empty, the
//     mapper silently falls back (NO error) — the typed-error envelope
//     covers missing-subject-as-mandatory per destination.
//
//   - Style is the visual style tag for Image-family destinations
//     (e.g. "Realistic", "Cinematic"). Required by ImagePath.
//
//   - Provider categorises the upstream source (e.g. "pexels",
//     "pixabay", "wikipedia"). Surfaced as a Drive subpath segment in
//     Wave 4 stock migration; carries to Qdrant payload in Wave 10.
//
//   - Project groups related assets under one umbrella (e.g. a book
//     processing run, a voiceover documentary). Required by Voiceover,
//     Book, and Script.
//
//   - Language is the BCP-47 tag (e.g. "en", "it-IT"). Required by
//     Voiceover and Script.
//
// Wire form: HTTP request bodies, job payload envelopes, and CLI flags
// marshal to this shape. Future fields go HERE (not on PublishRequest,
// which is the post-mapper canonical form and already saturation-tested
// for the 10 destination keys).
type AssetLocationInput struct {
	// Category groups assets under a logical taxonomy bucket.
	Category string `json:"category,omitempty"`

	// Subject identifies the subject within the group. Per-destination
	// mandatory-check enforced by BuildPublishRequest.
	Subject string `json:"subject,omitempty"`

	// Name is the optional human-friendly alternative to Subject. The
	// mapper prefers Subject over Name when both are non-empty.
	Name string `json:"name,omitempty"`

	// Style is the visual style tag for Image-family destinations.
	Style string `json:"style,omitempty"`

	// Provider categorises the upstream source (e.g. "pexels",
	// "pixabay", "wikipedia").
	Provider string `json:"provider,omitempty"`

	// Project groups related assets under one umbrella.
	Project string `json:"project_id,omitempty"`

	// Language is the BCP-47 tag for per-language subfoldering.
	Language string `json:"language,omitempty"`

	// ChannelID (PR-CLIPINGEST-PIPELINE step 9, July 2026) is the
	// canonical YouTube channel_id for the new YouTube asset layout.
	// Wire-level input: HTTP bodies, job payload envelopes, and CLI
	// flags populate this field for the new YouTubeAsset destination
	// (the legacy YouTubeClip destination continues to use Category as
	// the operator-curated group alias — the two coexist on this DTO).
	// Required for DestinationYouTubeAsset; the mapper surfaces
	// ErrAssetPublishLocationIncompleteForDestination when empty.
	ChannelID string `json:"channel_id,omitempty"`
}

// IsEmpty reports whether the AssetLocationInput is zero-value in every
// field. Endpoint validators MAY use this as a fast pre-check before
// invoking the full delivery.BuildPublishRequest typed errors. NO field
// is required at AssetLocationInput itself — the per-destination
// requirement is enforced by the mapper so zero-decode-from-JSON can
// round-trip cleanly.
func (l AssetLocationInput) IsEmpty() bool {
	return l.Category == "" &&
		l.Subject == "" &&
		l.Name == "" &&
		l.Style == "" &&
		l.Provider == "" &&
		l.Project == "" &&
		l.Language == ""
}

// SubjectOrName returns Subject if non-empty, otherwise Name, otherwise "".
// Mapper callers use this to soft-fall-back from the typed Subject
// contract onto the human-friendly Name when the API caller did not set
// Subject explicitly.
func (l AssetLocationInput) SubjectOrName() string {
	if l.Subject != "" {
		return l.Subject
	}
	return l.Name
}
