// Package outbox (repository_test.go) — unit tests for the canonical
// outbox event_key constructor. Fase 5 / Commit 2 (July 2026)
// replaced the legacy 5-segment indexEventKey helper (which
// included clipindexer model/version/collection segments) with
// the canonical 4-segment idempotency.OutboxKey (eventType:
// provider:clipID:sourceVersion). These tests pin:
//
//  1. The 4-segment shape: eventType:provider:clipID:sourceVersion.
//  2. The PR 5 gate (QDRANT-full-content-hash, June 2026): the
//     FULL content hash is preserved in the key — two hashes that
//     share their shortHashPrefix (first 12 chars) MUST still
//     produce DISTINCT event_keys, otherwise the outbox_events
//     UNIQUE constraint collapses them and the supersede gate
//     closes the wrong event.
//  3. Determinism: identical inputs produce identical keys
//     (the dedup-gate enabler).
//  4. Provider-segment distinctness: same clipID with different
//     providers MUST produce different event_keys (so a
//     YouTube clip and an Artlist clip with the same string ID
//     don't collide in the outbox).
//
// White-box (`package outbox`) so the test can call the
// canonical idempotency.OutboxKey directly. No DB or external
// state required: the key is a pure function of the inputs.
package outbox

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/pkg/idempotency"
)

// TestOutboxKey_DistinguishesHashesSamePrefix is the PR 5 gate
// (QDRANT-full-content-hash, June 2026) carried forward into
// the Commit 2 wire-in: the canonical contract is that two
// hashes whose first 12 chars match MUST still produce
// DIFFERENT event_keys — otherwise the outbox_events UNIQUE
// constraint collapses them to a single row and the worker's
// supersede gate closes the wrong event.
//
// The pre-PR-5 code generated the same event_key for the two
// hashes below; the canonical OutboxKey preserves the FULL
// source_version in the key, so two hashes that share their
// shortHashPrefix still produce distinct keys (the unique
// suffix of each hash lives in the trailing portion of the
// key).
//
// Why these specific hashes: they differ ONLY in chars 13..end
// (the suffix). shortHashPrefix returns the identical first 12
// chars for both, so a pre-fix collision would have collapsed
// them — the gate symptom. Post-fix, both full hashes land in
// the event_key under test.
func TestOutboxKey_DistinguishesHashesSamePrefix(t *testing.T) {
	const (
		assetID  = "asset-event-key"
		provider = "artlist"
		hashA    = "abcdef12345611111111111111111111111111111111111111"
		hashB    = "abcdef12345622222222222222222222222222222222222222"
	)

	prefixA := shortHashPrefix(hashA)
	prefixB := shortHashPrefix(hashB)
	if prefixA != prefixB {
		t.Fatalf("test setup is wrong: the two hashes must share their shortHashPrefix output for the test to be meaningful (got prefixA=%q prefixB=%q)", prefixA, prefixB)
	}

	keyA, errA := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, provider, assetID, hashA,
	)
	if errA != nil {
		t.Fatalf("OutboxKey A: %v", errA)
	}
	keyB, errB := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, provider, assetID, hashB,
	)
	if errB != nil {
		t.Fatalf("OutboxKey B: %v", errB)
	}

	// (1) Distinctness: a regression to shortHashPrefix in
	// event_key would produce identical event_keys here. The
	// exact equality check is the load-bearing assertion —
	// collision-free event_keys are the entire point of PR 5.
	if keyA == keyB {
		t.Fatalf(
			"QDRANT-full-content-hash (PR 5) regression: OutboxKey must "+
				"produce DISTINCT event_keys for two hashes that share the same "+
				"first-12-chars prefix (shortHashPrefix would have collapsed them). "+
				"Got keyA == keyB == %q — the dispatcher is still using the truncated prefix.",
			keyA,
		)
	}

	// (2) Full-hash inclusion: the unique-suffix portion of each
	// hash (after the 12-char prefix) MUST appear somewhere in
	// the event_key. The first 12 chars are shared and therefore
	// NOT a reliable witness — only the suffix proves the full
	// hash reached the key.
	suffixA := hashA[len(prefixA):]
	suffixB := hashB[len(prefixB):]
	if !strings.Contains(keyA, suffixA) {
		t.Errorf("event_key %q must contain the unique suffix of hashA %q (suffix dropped — full hash not used)", keyA, suffixA)
	}
	if !strings.Contains(keyB, suffixB) {
		t.Errorf("event_key %q must contain the unique suffix of hashB %q (suffix dropped — full hash not used)", keyB, suffixB)
	}
}

// TestOutboxKey_Shape pins the canonical 4-segment event_key
// shape so a future refactor that splits the format string
// (e.g. moves to a struct key, or re-adds infra-level
// segments) trips the shape assertion instead of silently
// breaking the outbox UNIQUE constraint.
//
// The accepted shape is:
//
//	"<event_type>:<provider>:<clip_id>:<source_version>"
//
// eventType is the dispatch-routing prefix (e.g.
// "asset.index.requested"). The infra-level fields
// (model/version/collection) from the legacy 5-segment
// shape are intentionally OMITTED — they're not part of
// the dedup identity (a model change must not break dedup,
// per the Commit 2 design rationale).
func TestOutboxKey_Shape(t *testing.T) {
	const (
		provider    = "artlist"
		assetID     = "asset-shape"
		contentHash = "1111111111111111111111111111111111111111"
	)
	key, err := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, provider, assetID, contentHash,
	)
	if err != nil {
		t.Fatalf("OutboxKey: %v", err)
	}

	parts := strings.Split(key, ":")
	// 4 segments: [eventType, provider, clipID, sourceVersion].
	// The legacy 5-segment shape had 6 segments
	// ([index, assetID, contentHash, model, version, collection]);
	// the regression guard is the segment-count assertion below.
	if len(parts) != 4 {
		t.Fatalf("event_key shape: expected 4 colon-separated segments, got %d in %q (legacy 5-segment shape returned 6; if you see 6 the wire-shape rollback to indexEventKey happened)", len(parts), key)
	}
	if parts[0] != outboxevents.EventAssetIndexRequested {
		t.Errorf("event_key shape: first segment must be event_type %q, got %q", outboxevents.EventAssetIndexRequested, parts[0])
	}
	if parts[1] != provider {
		t.Errorf("event_key shape: second segment must be provider %q, got %q", provider, parts[1])
	}
	if parts[2] != assetID {
		t.Errorf("event_key shape: third segment must be clipID %q, got %q", assetID, parts[2])
	}
	if parts[3] != contentHash {
		t.Errorf("event_key shape: fourth segment must be the FULL source_version %q, got %q (got shortHashPrefix instead?)", contentHash, parts[3])
	}
	// Regression guard: the legacy 5-segment shape used an
	// "index:" prefix. If a future refactor accidentally
	// re-introduces the legacy helper, this assertion fires.
	if strings.HasPrefix(key, "index:") {
		t.Errorf("event_key must NOT use the legacy 5-segment 'index:' prefix; got %q (Commit 2 wire-in regression)", key)
	}
}

// TestOutboxKey_HashesShortAndLong confirms the helper works
// on both edge cases: a content hash SHORT enough that
// shortHashPrefix returns the WHOLE string, and a hash LONG
// enough that the prefix differs from the full hash. The PR 5
// contract is that the event_key contains the FULL hash in
// both cases. The pre-PR-5 code returned keyA == keyB if both
// hashes shared the 12-char prefix, regardless of length.
func TestOutboxKey_HashesShortAndLong(t *testing.T) {
	const (
		assetID  = "asset-edge"
		provider = "artlist"
	)

	// Long hash A & B (22 chars, same first 12, different suffix).
	longA := "abcdef123456" + "AAAAAAAAAA"
	longB := "abcdef123456" + "BBBBBBBBBB"
	// Sanity: the prefix is identical.
	if shortHashPrefix(longA) != shortHashPrefix(longB) {
		t.Fatalf("test setup: longA/longB shortHashPrefix must match (test fixture)")
	}
	keyLongA, errA := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, provider, assetID, longA,
	)
	if errA != nil {
		t.Fatalf("OutboxKey longA: %v", errA)
	}
	keyLongB, errB := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, provider, assetID, longB,
	)
	if errB != nil {
		t.Fatalf("OutboxKey longB: %v", errB)
	}
	if keyLongA == keyLongB {
		t.Errorf("long hashes with same 12-char prefix: event_keys must differ; got keyLongA == keyLongB == %q", keyLongA)
	}

	// Identical short hashes (≤12 chars): dedupe correctly —
	// two truly identical source_versions must produce identical
	// event_keys (the dedup-gate enabler).
	const hashShort = "abc123def456"
	keyS1, errS1 := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, provider, assetID, hashShort,
	)
	if errS1 != nil {
		t.Fatalf("OutboxKey short: %v", errS1)
	}
	keyS2, errS2 := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, provider, assetID, hashShort,
	)
	if errS2 != nil {
		t.Fatalf("OutboxKey short: %v", errS2)
	}
	if keyS1 != keyS2 {
		t.Errorf("identical source_versions: event_keys must be identical for dedupe (unexpected: keyS1=%q != keyS2=%q)", keyS1, keyS2)
	}
}

// TestOutboxKey_ProviderSegmentDistinctness pins the wire-in
// contract: the provider segment of the event_key is part of
// the dedup identity. A YouTube clip and an Artlist clip with
// the same string clipID MUST produce different event_keys —
// otherwise a cross-provider replay would collapse in the
// outbox UNIQUE INDEX.
func TestOutboxKey_ProviderSegmentDistinctness(t *testing.T) {
	const (
		clipID      = "shared-id-123"
		contentHash = "1111111111111111111111111111111111111111"
	)
	ytKey, err := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, "youtube", clipID, contentHash,
	)
	if err != nil {
		t.Fatalf("OutboxKey youtube: %v", err)
	}
	artKey, err := idempotency.OutboxKey(
		outboxevents.EventAssetIndexRequested, "artlist", clipID, contentHash,
	)
	if err != nil {
		t.Fatalf("OutboxKey artlist: %v", err)
	}
	if ytKey == artKey {
		t.Errorf("same clipID with different providers MUST produce different event_keys; both = %q", ytKey)
	}
	// Verify the provider segment is the differentiator.
	if !strings.Contains(ytKey, ":youtube:") {
		t.Errorf("YouTube event_key must contain ':youtube:' segment; got %q", ytKey)
	}
	if !strings.Contains(artKey, ":artlist:") {
		t.Errorf("Artlist event_key must contain ':artlist:' segment; got %q", artKey)
	}
}
