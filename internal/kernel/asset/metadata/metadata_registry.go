// Package asset \u2014 metadata_registry.go is the canonical
// SSOT (godlike/06 "one owner per fact") for the SET OF
// ALLOWED name-spaced Asset.Metadata map keys.
//
// Why a registry: Asset.Metadata is `map[string]any` \u2014 the
// fifth column of godlike/06 SSOT compliance that the typed
// surface enforcement (PR-ASSET-METADATA-TYPED-SPLIT) does
// NOT cover. The registry pins the alphabet of LEGITIMATE
// `provider.*` namespace keys so future contributors cannot
// introduce a new key without an explicit owner + type
// declaration.
//
// godlike/07 NO-FAKE-AVAILABILITY: every name-spaced key in
// this registry MUST be backed by a real producer
// (godlike/06 SSOT owner). The registry is not a wish-list \u2014
// it documents shipped facts. The archcheck forward-
// prevention gate `percheck_metadata_key_registry` enforces
// the invariant at CI time.
//
// Conventions (SCANNER-RELEVANT, do not break):
//
//  1. Each entry MUST use the SINGLE-LINE struct-literal
//     shape: `{Key: "X", Owner: "Y", Type: "Z", Doc: "W"}`.
//     The archcheck scanner parses the slice via a regex
//     anchored on `{Key:` to `}` \u2014 multi-line struct
//     literals defeat the parser and surface as a
//     `registry_canonical_empty` CI violation.
//
//  2. Keys are lower-snake-case dot-separated
//     (`provider.subject`). Bare keys (no dot, e.g.
//     `drive_file_id`) are RESIDUE \u2014 they live in the
//     existing asset_accessors.go surface until typed-
//     strip PRs migrate them to the namespaced surface.
//
//  3. The alphabetical-sort delivery on
//     `AllowedMetadataKeys()` is deterministic; the
//     scanner never relies on init-order of the underlying
//     slice.
//
//  4. The slice MUST be non-empty in production (zero
//     entries trip a `registry_canonical_empty` CI
//     violation). The first 3 entries ship with this
//     commit (PR-METADATA-REGISTRY-FOUNDATION, July 2026):
//     youtube.video_id, youtube.channel_id,
//     artlist.clip_page_url. Future PRs ADD new entries via
//     direct append (the alphabet grows monotonically; the
//     scanner is additive, not subtractive).
package metadata

import (
	"sort"
	"strings"
)

// MetadataKeySpec is the canonical schema for one allowed
// Asset.Metadata key.
type MetadataKeySpec struct {
	// Key is the namespaced key \u2014 the literal string used
	// at producer/consumer sites as `Asset.Metadata[Key]`
	// or `Asset.GetMetadataString(Key)`. Lower-snake-case
	// dot-separated.
	Key string
	// Owner is the canonical producer package (godlike/06
	// SSOT "one owner per fact"). Routes residue-
	// accounting attribution when the scanner trips on a
	// future regression.
	Owner string
	// Type is the canonical value type. Mirrors
	// TypeScript-like notation so future generators +
	// downstream lints can interpret it: "string" |
	// "int" | "bool" | "float64" | "[]string" | "any".
	// The explicit string form (rather than a typed
	// enum) avoids locking the registry into a Go type
	// system \u2014 a future per-language generator can map
	// the string to its native type at the consumer site.
	Type string
	// Doc is a human-readable one-liner. The archcheck
	// scanner does NOT cross-reference Doc; it lives for
	// humans reading the registry, mirroring the field-
	// level godoc discipline throughout
	// internal/kernel/asset/.
	Doc string
}

// allowedMetadataKeys is the canonical SOLE whitelist for
// name-spaced Asset.Metadata map keys (godlike/06 SSOT).
//
// SCANNER-RELEVANT: each entry MUST be on a SINGLE LINE so
// the percheck_metadata_key_registry regex can parse the
// slice. See the conventions block at the top of this file.
var allowedMetadataKeys = []MetadataKeySpec{
	{
		Key:   "artlist.clip_page_url",
		Owner: "internal/capabilities/assets/providers/artlist/",
		Type:  "string",
		Doc:   "Artlist provider's source-of-truth clip page URL (the page that originated the canonical clip_id); distinct from Asset.ClipPageURL struct field (legacy single-output artifact URL \u2014 different lifecycle).",
	},
	{
		Key:   "youtube.channel_id",
		Owner: "internal/application/youtube/",
		Type:  "string",
		Doc:   "Canonical YouTube channel ID \u2014 the {channel_id} segment of https://www.youtube.com/channel/{channel_id}.",
	},
	{
		Key:   "youtube.video_id",
		Owner: "internal/application/youtube/",
		Type:  "string",
		Doc:   "Canonical YouTube video ID \u2014 the {video_id} segment of https://youtu.be/{video_id}.",
	},
}

// AllowedMetadataKeys returns the canonical whitelist
// sorted alphabetically by `Key` (deterministic). The
// archcheck scanner consumes this returned slice and trips
// on any name-spaced (`a.b.c`-containing)
// `Asset.Metadata["\u2026"]` literal or `Get/Set/Metadata[\u2026]
// ("\u2026")` accessor whose key is NOT in the set.
//
// godlike/06 SSOT contract: this function is the SINGLE
// public accessor for the registry. Internal callers
// MUST use the function; the underlying `allowedMetadataKeys`
// slice is unexported (the canonical SSOT surface).
//
// godlike/07 fail-closed: a zero-length canonical surface
// emits a `registry_canonical_empty` CI violation rather
// than a silent pass \u2014 an empty registry would convert
// the FORWARD-PREVENTION gate into an unconditional
// silent-pass which is a NO-FAKE-AVAILABILITY regression.
// The positive surface is intentionally tiny (3 entries)
// so PRs that add keys are visible at code-review time.
func AllowedMetadataKeys() []MetadataKeySpec {
	out := make([]MetadataKeySpec, len(allowedMetadataKeys))
	copy(out, allowedMetadataKeys)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Owner < out[j].Owner
	})
	return out
}

// HasMetadataKey returns true iff `key` is in the canonical
// whitelist. Used by archcheck `percheck_metadata_key_registry`
// for O(N) lookups against the alphabet (alphabet is small
// in the EXPAND phase but the function scales to a much
// larger growing set).
//
// Key match is EXACT: namespaced prefixes (e.g.
// "youtube.*") do NOT wildcard-match. Future PRs may add
// wildcard support if a query pattern emerges; the EXACT
// shape today mirrors godlike/06 "declared + owned by
// exactly one fact" discipline.
func HasMetadataKey(key string) bool {
	for _, k := range allowedMetadataKeys {
		if k.Key == key {
			return true
		}
	}
	return false
}

// ValidateMetadataKeyShape is a godlike/07 fail-closed
// helper used by both producer and consumer code: a key
// that begins/ends with `.` or contains whitespace OR
// uppercase characters is malformed. Producers MUST call
// this helper before writing into Asset.Metadata; consumers
// MUST call this helper before reading from it.
//
// godlike/06 SSOT invariant: all canonical keys are
// lower-snake-case dot-separated. Namespaces are sorted
// lowercase (`youtube.*`, `artlist.*`). Future namespaces
// (e.g. `stock.*`) follow the same shape.
func ValidateMetadataKeyShape(key string) bool {
	if key == "" {
		return false
	}
	if strings.HasPrefix(key, ".") || strings.HasSuffix(key, ".") {
		return false
	}
	for _, r := range key {
		if r == ' ' || r == '\t' || r == '\n' {
			return false
		}
		if r >= 'A' && r <= 'Z' {
			return false
		}
	}
	return true
}
