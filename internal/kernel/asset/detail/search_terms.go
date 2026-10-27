// Package detail — owner of the media_assets string-array column format
// contract (migration-museum audit point 8; see docs/migrations/
// BASELINE_INVENTORY.md "media_assets mirror fields").
//
// # The search_terms format question, closed
//
// `media_assets.search_terms` was added three times by migration history —
// 070 (ALTER … DEFAULT ”), 091 (a deliberate no-op whose header claims the
// canonical default is '[]') and 097 (ALTER … DEFAULT ”, kept for slot
// audit) — which left the expected format ambiguous: JSON array or plain
// text? This file is the single owner of the answer:
//
//   - stored form: a JSON array of strings — e.g. ["tokyo","news"]
//   - empty: the historical schema default ” and the canonical empty
//     encoding '[]' are BOTH valid and both mean "no terms"
//   - writers leave the column untouched for an empty list (the schema
//     default ” already means empty) and write the JSON array otherwise
//   - readers treat ”, '[]' and malformed JSON as "no terms" — a single
//     legacy row must never fail a read
//
// The same contract covers every string-array column of the media domain
// (media_assets.tags, allowed_channels, allowed_regions, …): they share one
// codec. EncodeStringArrayColumn and ParseStringArrayColumn below are that
// codec; the wire sites delegate here instead of keeping hand copies (the
// copies that produced the ambiguity: app/wiring encodeStringSliceJSON,
// postgres/media clipTagsJSON and decodeTagsJSON).
//
// # Historical note
//
// This file used to be a ghost: it documented DeriveSearchTerms /
// mergeSearchTerms / normalizeToken / addNormalized long after those
// helpers were deleted in the MEDIA LEGACY READ-PLANE DEMOLITION
// (2026-09-16 RebuildSearchTerms, 2026-10-26 the rest), and it described a
// '[]' column default the schema never had. Documentation alive past its
// code is how the format ambiguity survived three migrations — the
// contract above is written against the live writers and readers, not
// against history.
package detail

import (
	"encoding/json"
	"strings"
)

// EncodeStringArrayColumn renders a string slice for a string-array column.
// An empty (or nil) slice encodes as "" so the writer can leave the column
// at its schema default; ” and '[]' are equivalent on the read side (see
// ParseStringArrayColumn). A marshalling failure degrades to "" — an
// unencodable value is never partially written.
func EncodeStringArrayColumn(values []string) string {
	if len(values) == 0 {
		return ""
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(raw)
}

// ParseStringArrayColumn reads a string-array column back. ”, '[]' and
// malformed JSON all degrade to nil ("no terms") — search hydration must
// not fail on a single legacy row.
func ParseStringArrayColumn(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}
