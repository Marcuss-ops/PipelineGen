// Package scripts — clip_resolver_port.go declares the canonical
// typed port that resolves a slice of ClipReference values into
// ClipEvidence rows from media_assets.
//
// ClipReference.Type is authoritative; the resolver NEVER
// auto-classifies from Value. Per-type lookups are explicit and
// exhaustive (each ReferenceType has exactly one resolver
// dispatch arm; missing arm → ResolveReasonInvalidType).
//
// The resolver NEVER auto-ingests missing assets (the ingest
// endpoint lives at RegisterFromYouTube + MediaingestHandler).
// Missing references are reported as UnresolvedReference so the
// caller can surface them diagnostically.
//
// Layering note: per AGENTS.md Pattern 0 the port is declared
// structurally here. The adapter that wraps
// *assets.ClipsRepository lives in the matching usecase package
// (clip_resolver.go). Legacy clip_source_builder.clipsResolverPort
// is unaffected; the typed port is for NEW consumers.
package ports

import (
	"context"
	"strings"
)

// ReferenceType is the canonical enum of input-reference shapes.
// Each value is a stable wire token for ClipReference.Type. The
// resolver's behavior is whichever row of switch (in
// adapters/clip_resolver.go) matches the input Type — there is no
// value-based fallback.
type ReferenceType string

const (
	// RefTypeMediaAssetID: the value IS the canonical primary
	// key of media_assets.id. UUIDs, prefix-typed segment ids
	// (yt_<video>_<seg>_<n>), Drive-shaped ids ingested as PKs:
	// all match this type when the caller treats them as PKs.
	RefTypeMediaAssetID ReferenceType = "media_asset_id"

	// RefTypeYouTubeVideoID: the value is a YouTube video id
	// (the 11-character base64url token used in youtube.com/watch
	// URLs). The resolver expands this to all media_assets rows
	// whose id starts with `yt_<value>_`. The prefix convention
	// is the canonical segment-encoding for YouTube assets and
	// is the single source of truth for "all segments per video".
	RefTypeYouTubeVideoID ReferenceType = "youtube_video_id"

	// RefTypeDriveFileID: the value is a Google Drive file id
	// (25-44 chars from drive.google.com/file/d/<ID>/). The
	// resolver matches media_assets.drive_file_id exactly.
	RefTypeDriveFileID ReferenceType = "drive_file_id"

	// RefTypeExternalProviderID: the value is a "<provider>::<external_id>"
	// compound (the canonical wire shape for non-Drive providers —
	// see asset_repository_adapter.FindByExternalRef). The
	// resolver parses the compound and routes via
	// source + COALESCE(metadata_json, '{}') ->> '$.external_id'
	// for non-google_drive providers, or fall back to
	// drive_file_id for google_drive.
	RefTypeExternalProviderID ReferenceType = "external_provider_id"
)

// allReferenceTypes is the canonical set of supported ReferenceType
// values. Used by Valid() and by future admin/telemetry paths that
// enumerate the supported set without hard-coding the constants.
var allReferenceTypes = []ReferenceType{
	RefTypeMediaAssetID,
	RefTypeYouTubeVideoID,
	RefTypeDriveFileID,
	RefTypeExternalProviderID,
}

// Valid reports whether r is one of the supported ReferenceType
// values. The resolver uses Valid() as a precondition before
// dispatching on Type — invalid types produce Unresolved references
// with Reason="invalid_type" rather than panicking.
func (r ReferenceType) Valid() bool {
	for _, t := range allReferenceTypes {
		if r == t {
			return true
		}
	}
	return false
}

// ClipReference is one typed input to the resolver.
// Type is authoritative — the resolver never inspects Value to
// derive a Type (no shape heuristics, no length-window guessing).
// Callers that pass raw strings MUST classify first and set the
// Type explicitly; the original design accepted Type=ignore and
// probed multiple DB columns sequentially, which silently mixed
// layers.
type ClipReference struct {
	Type  ReferenceType `json:"type"`
	Value string        `json:"value"`
}

// ClipEvidence is one resolved asset. The AssetID is the canonical
// media_assets.id; DriveLink + Name + Filename are convenience
// projections so the calling pipeline doesn't re-query the row
// after resolution.
type ClipEvidence struct {
	AssetID           string        `json:"asset_id"`
	ReferenceValue    string        `json:"reference_value"`
	ReferenceType     ReferenceType `json:"reference_type"`
	Name              string        `json:"name,omitempty"`
	Filename          string        `json:"filename,omitempty"`
	Description       string        `json:"description,omitempty"`
	Tags              []string      `json:"tags,omitempty"`
	TranscriptExcerpt string        `json:"transcript_excerpt,omitempty"`
	DriveLink         string        `json:"drive_link,omitempty"`
}

// UnresolvedReference pairs the input ClipReference with a stable
// Reason token. The caller's diagnostics code keys on Reason, not
// on message text.
type UnresolvedReference struct {
	Reference ClipReference `json:"reference"`
	Reason    string        `json:"reason"`
}

// Reasons for UnresolvedReference.Reason. Stable wire tokens; do
// not rename without coordinating with the admin tools that key on
// these strings.
const (
	// ResolveReasonEmptyValue: caller passed Type set but Value="".
	ResolveReasonEmptyValue = "empty_value"

	// ResolveReasonInvalidType: Type is not in allReferenceTypes.
	ResolveReasonInvalidType = "invalid_type"

	// ResolveReasonNotFound: lookup succeeded but no media_assets
	// row matched the canonical column for this ReferenceType.
	// Diagnostic action: caller surfaces unresolved_references to
	// operator; ingest path is separate (RegisterFromYouTube).
	ResolveReasonNotFound = "not_found"

	// ResolveReasonDBError: lookup failed with a non-ErrNoRows
	// error. Diagnostic action: caller alarms and degrades.
	ResolveReasonDBError = "db_error"

	// ResolveReasonExternalProviderValueFormat: RefTypeExternalProviderID
	// requires the "<provider>::<external_id>" compound; anything
	// else (missing separator, empty provider, empty external_id)
	// is malformed and surfaces this reason rather than attempting
	// a half-valid lookup.
	ResolveReasonExternalProviderValueFormat = "external_provider_value_format"
)

// ClipResolutionResult is the aggregate output. Resolved is the
// successful evidence list (1 entry per ClipReference whose
// Type produced a single row, N entries for fan-out types like
// RefTypeYouTubeVideoID). Unresolved is the 1:1 list of references
// that did NOT yield at least one row.
//
// The resolver sets these fields but does NOT error on partial
// resolution — only top-level DB errors fail the call. This lets
// the caller build a typed stage error that distinguishes
// "all unresolved" from "partial resolution" from "db error".
type ClipResolutionResult struct {
	Resolved   []ClipEvidence        `json:"resolved"`
	Unresolved []UnresolvedReference `json:"unresolved"`
}

// ClipResolver is the canonical typed resolver for ClipReference.
// Production wiring is NewClipResolver(repo, log) in
// internal/application/scripts/adapters/clip_resolver.go, which
// satisfies the port via the typed repo methods on
// *assets.ClipsRepository. Tests can wire a stub via
// the narrow clipResolverPortReadOnly interface defined next to
// NewClipResolver.
type ClipResolver interface {
	Resolve(ctx context.Context, refs []ClipReference) (*ClipResolutionResult, error)
}

// externalProviderSeparator is the canonical compound separator for
// RefTypeExternalProviderID values. Single source of truth — the
// admin ingest endpoint emits the same separator. Exported
// so callers constructing compound values do not magic-string the
// delimiter in unrelated packages.
const externalProviderSeparator = "::"

// ExternalProviderValue composes a provider + external_id pair into
// the canonical wire shape consumed by RefTypeExternalProviderID.
// Pair this with ParseExternalProviderValue on the receive side.
func ExternalProviderValue(provider, externalID string) string {
	return provider + externalProviderSeparator + externalID
}

// ParseExternalProviderValue splits a compound value into provider
// and external_id. Returns ok=false on malformed inputs (missing
// separator, empty side).
func ParseExternalProviderValue(v string) (provider, externalID string, ok bool) {
	idx := strings.Index(v, externalProviderSeparator)
	if idx <= 0 || idx >= len(v)-len(externalProviderSeparator) {
		return "", "", false
	}
	return v[:idx], v[idx+len(externalProviderSeparator):], true
}
