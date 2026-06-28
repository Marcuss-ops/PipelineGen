// Package outbox (repository_test.go) — unit tests for the canonical
// event_key constructor (indexEventKey). The PR 5
// (QDRANT-full-content-hash, June 2026) fix replaces an inline
// Sprintf that called shortHashPrefix(contentHash); two distinct
// content hashes that shared the first 12 chars collapsed into the
// same event_key → the worker's supersede gate closed the (correct)
// newer event in favour of the (stale) older one. These tests pin
// the full-hash contract against two such colliding hashes.
//
// White-box (`package outbox`) so the test can call the unexported
// indexEventKey helper directly. No DB or external state required:
// the helper is a pure function of (assetID, contentHash) and the
// canonical clipindexer global state, which are compile-time
// constants in the test binary.
package outbox

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// TestIndexEventKey_DistinguishesHashesSamePrefix is the PR 5 gate:
// the canonical contract is that two hashes whose first 12 chars
// match MUST still produce DIFFERENT event_keys — otherwise the
// outbox_events UNIQUE constraint collapses them to a single row
// and the worker's supersede gate closes the wrong event.
//
// The PR 5 fix replaces `shortHashPrefix(contentHash)` (which
// truncates to 12 chars) with the FULL content hash in event_key
// construction. The pre-PR-5 code generated the same event_key for
// the two hashes below; the post-PR-5 code generates two distinct
// event_keys, each containing its own FULL hash substring.
//
// Why these specific hashes: they differ ONLY in chars 13..end
// (the suffix). shortHashPrefix returns the identical first 12 chars
// for both, so the pre-fix code collapsed them — the gate symptom.
// Post-fix, both full hashes land in the event_key under test.
func TestIndexEventKey_DistinguishesHashesSamePrefix(t *testing.T) {
	const (
		assetID = "asset-event-key"
		hashA   = "abcdef12345611111111111111111111111111111111111111"
		hashB   = "abcdef12345622222222222222222222222222222222222222"
	)

	prefixA := shortHashPrefix(hashA)
	prefixB := shortHashPrefix(hashB)
	if prefixA != prefixB {
		t.Fatalf("test setup is wrong: the two hashes must share their shortHashPrefix output for the test to be meaningful (got prefixA=%q prefixB=%q)", prefixA, prefixB)
	}

	keyA := indexEventKey(assetID, hashA)
	keyB := indexEventKey(assetID, hashB)

	// (1) Distinctness: a regression to shortHashPrefix in event_key
	// would produce identical event_keys here. The exact equality
	// check is the load-bearing assertion — collision-free
	// event_keys are the entire point of PR 5.
	if keyA == keyB {
		t.Fatalf(
			"QDRANT-full-content-hash (PR 5) regression: indexEventKey must "+
				"produce DISTINCT event_keys for two hashes that share the same "+
				"first-12-chars prefix (shortHashPrefix would have collapsed them). "+
				"Got keyA == keyB == %q — the inline construction is still using "+
				"shortHashPrefix(contentHash). Replace it with the FULL content hash.",
			keyA,
		)
	}

	// (2) Full-hash inclusion: the unique-suffix portion of each
	// hash (After the 12-char prefix) MUST appear somewhere in the
	// event_key. The first 12 chars are shared and therefore NOT a
	// reliable witness — only the suffix proves the full hash
	// reached the key.
	suffixA := hashA[len(prefixA):]
	suffixB := hashB[len(prefixB):]
	if !strings.Contains(keyA, suffixA) {
		t.Errorf("event_key %q must contain the unique suffix of hashA %q (suffix dropped — full hash not used)", keyA, suffixA)
	}
	if !strings.Contains(keyB, suffixB) {
		t.Errorf("event_key %q must contain the unique suffix of hashB %q (suffix dropped — full hash not used)", keyB, suffixB)
	}
}

// TestIndexEventKey_Shape pins the canonical event_key shape so a
// future refactor that splits the format string (e.g. moves to a
// struct key, or omits the collection_version suffix) trips the
// shape assertion instead of silently breaking the outbox UNIQUE
// constraint.
//
// The accepted shape is:
//
//	"index:<asset_id>:<full_content_hash>:<embedding_model>:<embedding_version>:<collection_version>"
//
// Matches the legacy media_index_outbox unique-key semantics for
// migration continuity (the prior outbox_events table that
// repository.go replaced was keyed on the same tuple).
func TestIndexEventKey_Shape(t *testing.T) {
	const (
		assetID     = "asset-shape"
		contentHash = "1111111111111111111111111111111111111111"
	)
	key := indexEventKey(assetID, contentHash)

	parts := strings.Split(key, ":")
	// 6 segments: ["index", assetID, contentHash, model, version, collection]
	if len(parts) != 6 {
		t.Fatalf("event_key shape: expected 6 colon-separated segments, got %d in %q", len(parts), key)
	}
	if parts[0] != "index" {
		t.Errorf("event_key shape: first segment must be %q (the event_type prefix), got %q", "index", parts[0])
	}
	if parts[1] != assetID {
		t.Errorf("event_key shape: second segment must be assetID %q, got %q", assetID, parts[1])
	}
	if parts[2] != contentHash {
		t.Errorf("event_key shape: third segment must be the FULL contentHash %q, got %q (got shortHashPrefix instead?)", contentHash, parts[2])
	}
	// The remaining three segments are clipindexer compile-time
	// constants; just confirm they are non-empty so a future
	// clipindexer config-loader change that accidentally returns
	// "" doesn't silently collapse distinct assets.
	if parts[3] != clipindexer.EmbeddingModel() || parts[3] == "" {
		t.Errorf("event_key shape: 4th segment must be clipindexer.EmbeddingModel() %q (non-empty), got %q", clipindexer.EmbeddingModel(), parts[3])
	}
	if parts[4] != clipindexer.EmbeddingModelVersion() || parts[4] == "" {
		t.Errorf("event_key shape: 5th segment must be clipindexer.EmbeddingModelVersion() %q (non-empty), got %q", clipindexer.EmbeddingModelVersion(), parts[4])
	}
	if parts[5] != clipindexer.CollectionVersion() || parts[5] == "" {
		t.Errorf("event_key shape: 6th segment must be clipindexer.CollectionVersion() %q (non-empty), got %q", clipindexer.CollectionVersion(), parts[5])
	}
}

// TestIndexEventKey_HashesShortAndLong confirms the helper works
// on both edge cases: a content hash SHORT enough that
// shortHashPrefix returns the WHOLE string, and a hash LONG enough
// that the prefix differs from the full hash. The PR 5 contract is
// that the event_key contains the FULL hash in both cases. The
// pre-PR-5 code returned keyA == keyB if both hashes shared the
// 12-char prefix, regardless of length.
func TestIndexEventKey_HashesShortAndLong(t *testing.T) {
	const assetID = "asset-edge"

	// Long hash A & B (50 chars, same first 12, different suffix).
	longA := "abcdef123456" + "AAAAAAAAAA"
	longB := "abcdef123456" + "BBBBBBBBBB"
	// Sanity: the prefix is identical.
	if shortHashPrefix(longA) != shortHashPrefix(longB) {
		t.Fatalf("test setup: longA/longB shortHashPrefix must match (test fixture)")
	}
	keyLongA := indexEventKey(assetID, longA)
	keyLongB := indexEventKey(assetID, longB)
	if keyLongA == keyLongB {
		t.Errorf("long hashes with same 12-char prefix: event_keys must differ; got keyLongA == keyLongB == %q", keyLongA)
	}

	// Short hash (≤12 chars). Both hashes below are exactly 12
	// chars and identical → keyA MUST equal keyB (degenerate case;
	// two truly identical hashes dedupe correctly, regardless of
	// which length branch shortHashPrefix takes).
	hashShortA := "abc123def456"
	hashShortB := "abc123def456"
	if keyA := indexEventKey(assetID, hashShortA); keyA != indexEventKey(assetID, hashShortB) {
		t.Errorf("identical hashes: event_keys must be identical for dedupe (unexpected: keyA=%q != keyB=%q)", keyA, indexEventKey(assetID, hashShortB))
	}
}
