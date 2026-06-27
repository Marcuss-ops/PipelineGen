// Package search — dedup.go implements the Wave 21 PR 9 4-key dedup
// policy used by Aggregator.Search after per-backend fanout.
//
// Dedup contract (project rule, PR 8 spec):
//
//	Two Candidates are "the same" if they share ANY of:
//
//	  1. AssetID         (canonical UUID/hex)
//	  2. Source+"|"+SourceRef
//	                    (provider-native identity, e.g.
//	                     "youtube|dQw4w9WgXcQ")
//	  3. canonical(PreviewURL)
//	                    (URL with query-string + fragment stripped
//	                     so tracking params and signed-token short-
//	                     expiry variants do NOT diverge identity)
//	  4. Hash            (content hash if the backend reported one)
//
// When a collision exists, the higher-Score entry wins. Ties
// resolve deterministically (existing stays; new is dropped). The
// 4-key path means a Candidate may carry ANY single identity field
// alone and still dedup reliably — empty fields are skipped in
// each keyspace lookup so backends that emit SparseRef-only or
// Hash-only hits do not collide on absent fields.
//
// ProviderErrors is a separate map keyed by backend Name() and
// is decided after dedup; later completion overwrites earlier
// errors per backend-key (last-write-wins; see concurrent.Group
// + per-backend timeout in aggregator.go).
//
// Cursor stability: the Aggregator computes a SkipSet from the
// incoming cursor's fingerprint so candidates emitted on a
// previous page do not appear on the next page.
package search

import (
	"encoding/json"
	"strings"
)

// dedupIndex tracks candidate identity across the 4 keys for fast
// merge-time insertions. Backing maps hold the *current* index of a
// given key's representative in the merged slice. Re-keying happens
// when the representative is replaced by a higher-score duplicate
// (the new entry's keys may differ from the old's).
//
// SourceRef lives in two keyspaces:
//   - bySourceRef: Source+"|"+SourceRef (BOTH non-empty required)
//   - bySourceRefSolo: SourceRef alone (covers backends that emit
//     SourceRef-only candidates — providers.SearchProvider, where
//     AssetID is empty and SourceRef carries the provider-native id)
type dedupIndex struct {
	byAssetID       map[string]int
	bySourceRef     map[string]int
	bySourceRefSolo map[string]int
	byURL           map[string]int
	byHash          map[string]int
	merged          []Candidate
}

// newDedupIndex allocates an empty index. Pre-size hint is omitted
// because callers don't usually know the cardinality in advance.
func newDedupIndex() *dedupIndex {
	return &dedupIndex{
		byAssetID:       make(map[string]int),
		bySourceRef:     make(map[string]int),
		bySourceRefSolo: make(map[string]int),
		byURL:           make(map[string]int),
		byHash:          make(map[string]int),
	}
}

// Add inserts a candidate, deduping via 4-key collision. Returns
// the index in the merged slice. -1 signals "no identity" (all 4
// fields were empty); the Aggregator drops such entries silently
// per dedup policy.
//
// Higher Score overrides; equal Score keeps existing (later
// arrival does NOT silently clobber committed ties).
func (d *dedupIndex) Add(c Candidate) int {
	if c.AssetID == "" && c.SourceRef == "" && c.PreviewURL == "" && c.Hash == "" {
		return -1
	}

	matches := d.lookupMatches(c)
	if len(matches) == 0 {
		// First time we see any of this candidate's keys — append.
		d.merged = append(d.merged, c)
		idx := len(d.merged) - 1
		d.indexKeys(c, idx)
		return idx
	}

	// Existing match — pick first match (any key collision is
	// sufficient). Replace if the new entry has a strictly higher
	// Score; re-key with the new candidate's identity in case it
	// has filled in different fields (e.g. backend-A emitted
	// SourceRef-only, backend-B emitted AssetID+Hash for the same
	// real-world item).
	bestIdx := matches[0]
	if c.Score > d.merged[bestIdx].Score {
		d.merged[bestIdx] = c
		d.indexKeys(c, bestIdx)
	}
	return bestIdx
}

// lookupMatches returns all matching indices for the candidate's
// 4 keys. Empty keys are skipped (don't pollute the lookup with
// ""→arbitrary collisions). The Source+|SourceRef key requires
// BOTH halves to be non-empty — using OR would collapse every
// candidate that shares an empty SourceRef but a populated
// Source (common in single-backend fanout where the backend
// emits "youtube"+AssetID-only hits).
func (d *dedupIndex) lookupMatches(c Candidate) []int {
	var matches []int
	if c.AssetID != "" {
		if idx, ok := d.byAssetID[c.AssetID]; ok {
			matches = append(matches, idx)
		}
	}
	if c.Source != "" && c.SourceRef != "" {
		key := c.Source + "|" + c.SourceRef
		if idx, ok := d.bySourceRef[key]; ok {
			matches = append(matches, idx)
		}
	}
	if c.SourceRef != "" {
		if idx, ok := d.bySourceRefSolo[c.SourceRef]; ok {
			matches = append(matches, idx)
		}
	}
	if c.PreviewURL != "" {
		key := canonicalURL(c.PreviewURL)
		if idx, ok := d.byURL[key]; ok {
			matches = append(matches, idx)
		}
	}
	if c.Hash != "" {
		if idx, ok := d.byHash[c.Hash]; ok {
			matches = append(matches, idx)
		}
	}
	return matches
}

// indexKeys rebuilds the 4 maps for an inserted/updated candidate.
// Called both on first add and on score-override replacement.
// Same Source-or-SourceRef OR-pattern as lookupMatches (matches the
// rule: both keys populated to collide via the compound key).
func (d *dedupIndex) indexKeys(c Candidate, idx int) {
	if c.AssetID != "" {
		d.byAssetID[c.AssetID] = idx
	}
	if c.Source != "" && c.SourceRef != "" {
		d.bySourceRef[c.Source+"|"+c.SourceRef] = idx
	}
	if c.SourceRef != "" {
		d.bySourceRefSolo[c.SourceRef] = idx
	}
	if c.PreviewURL != "" {
		d.byURL[canonicalURL(c.PreviewURL)] = idx
	}
	if c.Hash != "" {
		d.byHash[c.Hash] = idx
	}
}

// canonicalURL strips the query string + fragment from u so URLs
// that differ only by tracking params (utm_source, signed tokens,
// etc.) coalesce to the same identity. Hosts an empty string and
// short strings unchanged; defensive against nil pointers at the
// type systems + json shape layer.
func canonicalURL(u string) string {
	if u == "" {
		return ""
	}
	if idx := strings.IndexAny(u, "?#"); idx > 0 {
		return u[:idx]
	}
	return u
}

// Merge applies the 4-key dedup policy to incoming candidates,
// dropping items whose identity appears in skip (cursor stability)
// and ranking the survivors. Returns a freshly-allocated slice;
// the input is not mutated.
//
// Order: dedup first (preserving arrival order — backend errors
// can yield candidates that arrive out-of-score order), then
// rank by Score DESC with stable secondary (Source ASC, AssetID
// ASC) so pagination is byte-stable across calls.
func Merge(in []Candidate, skip map[string]struct{}) []Candidate {
	if len(in) == 0 {
		return []Candidate{}
	}
	idx := newDedupIndex()
	for _, c := range in {
		if skipSetContains(skip, c) {
			continue
		}
		idx.Add(c)
	}
	// Drop empty-identity placeholders (-1 returns) cleanly: the
	// merged slice already excludes them because Add returned -1
	// without appending (see top of Add).
	return RankByScore(idx.merged)
}

// SkipSetFromCursor returns a fingerprint lookup set derived from
// the incoming cursor. Items whose ANY 4-key matches the set are
// dropped from the next-page's results. Returns nil for an empty
// or malformed cursor (caller treats nil as "no skip").
//
// Malformed cursors do NOT error here — the Aggregator already
// validates q.Cursor via DecodeCursor before calling this helper.
//
// SourceRef is preserved in the fingerprint after PR 9 — cursors
// pre-PR-9 (no SourceRef field) round-trip cleanly because the
// JSON decoder fills the omitted field with "" and the lookup
// skips empty keys.
func SkipSetFromCursor(c Cursor) map[string]struct{} {
	if c == "" {
		return nil
	}
	blob := fingerprintBlob{}
	if err := json.Unmarshal([]byte(string(c)), &blob); err != nil {
		return nil
	}
	if blob.Version != cursorCodecVersion {
		return nil
	}
	out := make(map[string]struct{}, len(blob.Items)*4)
	for _, f := range blob.Items {
		if f.AssetID != "" {
			out["aid:"+f.AssetID] = struct{}{}
		}
		// Compound Source+"|"+SourceRef key requires BOTH halves
		// to be non-empty (matches the dedupIndex rule).
		if f.Source != "" && f.SourceRef != "" {
			out["ref:"+f.Source+"|"+f.SourceRef] = struct{}{}
		}
		// SourceRef solo key — covers provider backends whose
		// SourceRef carries the provider-native id and whose
		// AssetID is empty (i.e. no compound key would be writable
		// for them — this is the path that page-2 stability
		// requires).
		if f.SourceRef != "" {
			out["sref:"+f.SourceRef] = struct{}{}
		}
	}
	return out
}

// skipSetContains returns true if any of c's 4 keys is in skip.
// Mirrors the 4-key collision rule — a candidate is page-stale if
// ANY key fog matches a previously-served item.
//
// SourceRef participates in two skip-keyspaces (matching the
// dedupIndex design above): the compound Source+"|"+SourceRef key
// (BOTH required) AND the SourceRef-solo key. The solo key covers
// provider-backends (AssetID empty, SourceRef carries the
// provider-native id) where the compound key would not match.
func skipSetContains(skip map[string]struct{}, c Candidate) bool {
	if skip == nil {
		return false
	}
	if c.AssetID != "" {
		if _, ok := skip["aid:"+c.AssetID]; ok {
			return true
		}
	}
	if c.Source != "" && c.SourceRef != "" {
		if _, ok := skip["ref:"+c.Source+"|"+c.SourceRef]; ok {
			return true
		}
	}
	if c.SourceRef != "" {
		if _, ok := skip["sref:"+c.SourceRef]; ok {
			return true
		}
	}
	if c.PreviewURL != "" {
		if _, ok := skip["url:"+canonicalURL(c.PreviewURL)]; ok {
			return true
		}
	}
	if c.Hash != "" {
		if _, ok := skip["hash:"+c.Hash]; ok {
			return true
		}
	}
	return false
}
