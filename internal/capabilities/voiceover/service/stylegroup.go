// Package voiceover — stylegroup.go (PR-VO-TYPED-PRIMITIVES, July 2026).
//
// Typed envelope for the StyleGroup routing tag (PR-VO-B2, June
// 2026) that buckets voiceovers into a "style cohort" alongside
// Group (specific folder) and SubfolderName (leaf subfolder).
//
// PR-VO-TYPED-PRIMITIVES (July 2026) closes the audit-flagged
// primitive-obsession on StyleGroup across 11+ raw-string sites in
// types.go::DestinationRequest, types.go::ResolvedDestination,
// metadata.go::mergeUserMetadata, process_voiceover_item.go, and
// process.go.
//
// Permissive parse: empty is the canonical "no style" signal
// (matches the pre-PR-VO-TYPED-PRIMITIVES omitempty contract on
// DestinationRequest.StyleGroup + the legacy "StyleGroup injection
// happens when dest.StyleGroup != ''" branch in mergeUserMetadata).
// Strict parse would force every caller to handle a "no style"
// error path that has no real-world meaning — the canonical
// signal is the empty string.
//
// JSON wire compat: type StyleGroup string serialises the
// underlying string byte-for-byte. The "style_group" JSON tag
// on DestinationRequest works without changes (omitempty on
// empty StyleGroup continues to omit the wire key).
//
// DB wire compat: stored in the metadata JSON column (no
// dedicated column). The meta["style_group"] = styleGroup
// assignment works without explicit conversion (typed value IS
// a string at the any level).

package voiceover

import "strings"

// StyleGroup is the typed envelope for the per-batch style cohort
// routing tag. Defined type (NOT alias) so the type system
// catches the audit-flagged primitive-obsession at every
// assignment site. JSON wire shape is byte-equivalent with the
// pre-PR-VO-TYPED-PRIMITIVES string field.
type StyleGroup string

// EmptyStyleGroup is the canonical zero value. Use this (NOT the
// string-literal "") for typed comparison so the audit-pin
// discipline catches a future drift on the empty-marker.
const EmptyStyleGroup StyleGroup = ""

// ParseStyleGroup is the canonical permissive constructor for the
// StyleGroup typed envelope. Trims surrounding whitespace; empty
// after trim is the canonical "no style" signal (NOT an error).
//
// Use this at every TRUST BOUNDARY (HTTP handler, job payload
// unmarshal). Internal voiceover-internal sites that already hold
// a typed StyleGroup value do NOT need to re-parse.
func ParseStyleGroup(s string) StyleGroup {
	return StyleGroup(strings.TrimSpace(s))
}

// String returns the underlying string form. Useful for fmt
// interpolation + the meta["style_group"] assignment site.
func (s StyleGroup) String() string { return string(s) }

// IsEmpty returns true when s is the canonical zero value. Mirrors
// the pre-refactor `if dest.StyleGroup != ""` predicate in
// mergeUserMetadata with a typed envelope + a clearer call site.
func (s StyleGroup) IsEmpty() bool { return s == EmptyStyleGroup }
