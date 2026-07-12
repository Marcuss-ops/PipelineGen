// Package idempotency — keys_test.go (Fase 5, July 2026)
//
// Pins the 3 canonical key shapes (AssetKey, JobKey,
// OutboxKey) defined in keys.go. The pure-function tests
// pin the key SHAPE (deterministic, distinct, format)
// + the fail-closed guards (empty inputs). The 5 dedup
// GUARANTEE tests (discovery, replay, scraper, crash
// recovery, upload retry) live in the integration test
// file added in Commit 5+ — they require DB-level UNIQUE
// constraints + multi-row scenarios that pure unit tests
// can't exercise.
package idempotency

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── AssetKey tests ─────────────────────────────────────────────

// TestAssetKey_HappyPath pins the canonical 4-tuple shape.
func TestAssetKey_HappyPath(t *testing.T) {
	key, err := AssetKey("artlist", "abc123", "v1", "deadbeefcafebabe1234567890abcdef")
	require.NoError(t, err)
	assert.Equal(t, "artlist:abc123:v1:deadbeefcafebabe1234567890abcdef", key)
}

// TestAssetKey_Deterministic pins the determinism property.
// ON CONFLICT (event_key) DO NOTHING only works if the key is
// byte-identical across calls.
func TestAssetKey_Deterministic(t *testing.T) {
	a, _ := AssetKey("artlist", "abc123", "v1", "deadbeef")
	b, _ := AssetKey("artlist", "abc123", "v1", "deadbeef")
	assert.Equal(t, a, b, "AssetKey must be deterministic (same inputs → same key)")
}

// TestAssetKey_DifferentInputsDifferentKeys pins that each
// of the 4 inputs is part of the key identity. A future
// refactor that drops one input would be caught here.
func TestAssetKey_DifferentInputsDifferentKeys(t *testing.T) {
	a, _ := AssetKey("artlist", "abc", "v1", "deadbeef")
	b, _ := AssetKey("youtube", "abc", "v1", "deadbeef")  // different provider
	c, _ := AssetKey("artlist", "xyz", "v1", "deadbeef")  // different clipID
	d, _ := AssetKey("artlist", "abc", "v2", "deadbeef")  // different sourceVersion
	e, _ := AssetKey("artlist", "abc", "v1", "cafebabe")  // different sha256
	assert.NotEqual(t, a, b, "different provider → different key")
	assert.NotEqual(t, a, c, "different clip_id → different key")
	assert.NotEqual(t, a, d, "different source_version → different key")
	assert.NotEqual(t, a, e, "different sha256 → different key")
}

// TestAssetKey_NoTitleDependency pins the user-spec literal
// "Niente dipendenza dal solo titolo". The test verifies the
// NEGATIVE case: if a future contributor adds a "name" or
// "title" parameter, the function signature changes (which
// the test would catch at compile time — the existing
// callers in the codebase would also need updates). The
// runtime assertion checks that the key never contains
// "title" or "name" as a substring.
func TestAssetKey_NoTitleDependency(t *testing.T) {
	a, _ := AssetKey("artlist", "abc", "v1", "deadbeef")
	assert.NotContains(t, a, "title", "AssetKey must NOT contain 'title' as a segment")
	assert.NotContains(t, a, "name", "AssetKey must NOT contain 'name' as a segment")
	// The key is exactly the 4-tuple concatenation — verify the
	// canonical shape.
	assert.Equal(t, "artlist:abc:v1:deadbeef", a)
}

// TestAssetKey_EmptyProvider pins ErrEmptyProvider.
func TestAssetKey_EmptyProvider(t *testing.T) {
	_, err := AssetKey("", "abc", "v1", "deadbeef")
	assert.ErrorIs(t, err, ErrEmptyProvider)
}

// TestAssetKey_EmptyClipID pins ErrEmptyClipID.
func TestAssetKey_EmptyClipID(t *testing.T) {
	_, err := AssetKey("artlist", "", "v1", "deadbeef")
	assert.ErrorIs(t, err, ErrEmptyClipID)
}

// TestAssetKey_EmptySourceVersion pins ErrEmptySourceVersion.
func TestAssetKey_EmptySourceVersion(t *testing.T) {
	_, err := AssetKey("artlist", "abc", "", "deadbeef")
	assert.ErrorIs(t, err, ErrEmptySourceVersion)
}

// TestAssetKey_EmptySHA256 pins ErrEmptySHA256. The
// godlike/07 fail-closed contract: a caller that doesn't have
// the sha256 yet MUST use JobKey (the 3-tuple, no file hash)
// until the file is downloaded.
func TestAssetKey_EmptySHA256(t *testing.T) {
	_, err := AssetKey("artlist", "abc", "v1", "")
	assert.ErrorIs(t, err, ErrEmptySHA256)
	assert.Contains(t, err.Error(), "use JobKey",
		"err message must guide the caller to JobKey as the pre-download alternative")
}

// TestAssetKey_AllEmpty pins the first-error-wins behavior
// of the guard sequence (provider checked first).
func TestAssetKey_AllEmpty(t *testing.T) {
	_, err := AssetKey("", "", "", "")
	assert.ErrorIs(t, err, ErrEmptyProvider,
		"all-empty input surfaces the FIRST guard (provider) for fail-fast diagnostic clarity")
}

// ── JobKey tests ───────────────────────────────────────────────

// TestJobKey_HappyPath pins the canonical 3-tuple shape.
func TestJobKey_HappyPath(t *testing.T) {
	key, err := JobKey("artlist", "abc123", "v1")
	require.NoError(t, err)
	assert.Equal(t, "artlist:abc123:v1", key)
}

// TestJobKey_Deterministic pins the determinism property
// (same as AssetKey_Deterministic but for JobKey).
func TestJobKey_Deterministic(t *testing.T) {
	a, _ := JobKey("artlist", "abc", "v1")
	b, _ := JobKey("artlist", "abc", "v1")
	assert.Equal(t, a, b)
}

// TestJobKey_DifferentInputsDifferentKeys pins the 3-input
// identity. (No sha256 input — the file may not exist yet.)
func TestJobKey_DifferentInputsDifferentKeys(t *testing.T) {
	a, _ := JobKey("artlist", "abc", "v1")
	b, _ := JobKey("youtube", "abc", "v1")
	c, _ := JobKey("artlist", "xyz", "v1")
	d, _ := JobKey("artlist", "abc", "v2")
	assert.NotEqual(t, a, b, "different provider → different key")
	assert.NotEqual(t, a, c, "different clip_id → different key")
	assert.NotEqual(t, a, d, "different source_version → different key")
}

// TestJobKey_NoTitleDependency (same property as AssetKey).
func TestJobKey_NoTitleDependency(t *testing.T) {
	a, _ := JobKey("artlist", "abc", "v1")
	assert.NotContains(t, a, "title")
	assert.NotContains(t, a, "name")
	assert.Equal(t, "artlist:abc:v1", a)
}

// TestJobKey_EmptyGuards pins the 3 typed sentinels.
func TestJobKey_EmptyGuards(t *testing.T) {
	_, err := JobKey("", "abc", "v1")
	assert.ErrorIs(t, err, ErrEmptyProvider)
	_, err = JobKey("artlist", "", "v1")
	assert.ErrorIs(t, err, ErrEmptyClipID)
	_, err = JobKey("artlist", "abc", "")
	assert.ErrorIs(t, err, ErrEmptySourceVersion)
}

// TestJobKey_IsPrefixOfAssetKey pins the writer-friendly
// invariant: a caller can construct JobKey at job creation
// time (before the file is downloaded) and then upgrade to
// AssetKey (once the sha256 is computed) without changing
// the first 3 segments. This lets the writer thread the
// same dedup key through the pipeline.
func TestJobKey_IsPrefixOfAssetKey(t *testing.T) {
	jk, _ := JobKey("artlist", "abc", "v1")
	ak, _ := AssetKey("artlist", "abc", "v1", "deadbeef")
	assert.True(t, strings.HasPrefix(ak, jk+":"),
		"AssetKey must be JobKey + ':' + sha256 (writer-friendly prefix invariant)")
}

// ── OutboxKey tests ────────────────────────────────────────────

// TestOutboxKey_HappyPath pins the canonical 4-tuple shape.
func TestOutboxKey_HappyPath(t *testing.T) {
	key, err := OutboxKey("asset.index.requested.v1", "artlist", "abc123", "v1")
	require.NoError(t, err)
	assert.Equal(t, "asset.index.requested.v1:artlist:abc123:v1", key)
}

// TestOutboxKey_Deterministic (same property as the others).
func TestOutboxKey_Deterministic(t *testing.T) {
	a, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v1")
	b, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v1")
	assert.Equal(t, a, b)
}

// TestOutboxKey_DifferentEventTypesDifferentKeys pins that
// the event_type is part of the dedup identity. The same
// asset with different event_types produces different keys
// (so a Qdrant upsert event and a Drive delete event for
// the same asset don't collide in the outbox).
func TestOutboxKey_DifferentEventTypesDifferentKeys(t *testing.T) {
	a, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v1")
	b, _ := OutboxKey("asset.drive.delete_requested.v1", "artlist", "abc", "v1")
	c, _ := OutboxKey("asset.published", "artlist", "abc", "v1")
	assert.NotEqual(t, a, b, "different event_type (index vs delete) → different key")
	assert.NotEqual(t, a, c, "different event_type (index vs published) → different key")
}

// TestOutboxKey_DifferentInputsDifferentKeys pins the 4-input
// identity. (eventType is the disambiguator; provider +
// clipID + sourceVersion mirror JobKey.)
func TestOutboxKey_DifferentInputsDifferentKeys(t *testing.T) {
	a, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v1")
	b, _ := OutboxKey("asset.index.requested.v1", "youtube", "abc", "v1")
	c, _ := OutboxKey("asset.index.requested.v1", "artlist", "xyz", "v1")
	d, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v2")
	assert.NotEqual(t, a, b)
	assert.NotEqual(t, a, c)
	assert.NotEqual(t, a, d)
}

// TestOutboxKey_NoTitleDependency.
func TestOutboxKey_NoTitleDependency(t *testing.T) {
	a, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v1")
	assert.NotContains(t, a, "title")
	assert.NotContains(t, a, "name")
}

// TestOutboxKey_EmptyGuards pins the 4 typed sentinels.
func TestOutboxKey_EmptyGuards(t *testing.T) {
	_, err := OutboxKey("", "artlist", "abc", "v1")
	assert.ErrorIs(t, err, ErrEmptyEventType)
	_, err = OutboxKey("asset.index.requested.v1", "", "abc", "v1")
	assert.ErrorIs(t, err, ErrEmptyProvider)
	_, err = OutboxKey("asset.index.requested.v1", "artlist", "", "v1")
	assert.ErrorIs(t, err, ErrEmptyClipID)
	_, err = OutboxKey("asset.index.requested.v1", "artlist", "abc", "")
	assert.ErrorIs(t, err, ErrEmptySourceVersion)
}

// ── Cross-key tests (no title, no provider, no clipID, no sourceVersion) ──

// TestKeys_NoTitleInput is the regression pin for the user
// spec literal "Niente dipendenza dal solo titolo". The
// runtime assertion checks that the keys never contain
// "title" or "name" as a substring. The compile-time
// guarantee is the function signatures: none of the 3
// constructors takes a title or name parameter.
func TestKeys_NoTitleInput(t *testing.T) {
	a, _ := AssetKey("artlist", "abc", "v1", "deadbeef")
	b, _ := JobKey("artlist", "abc", "v1")
	c, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v1")
	assert.NotContains(t, a, "title", "AssetKey must NOT depend on title")
	assert.NotContains(t, b, "title", "JobKey must NOT depend on title")
	assert.NotContains(t, c, "title", "OutboxKey must NOT depend on title")
}

// TestKeys_DistinctShapes pins the segment counts and
// ensures the 4-segment AssetKey and 4-segment OutboxKey
// are distinguished by the first segment (provider vs
// event_type). This is the canonical "no shape collision"
// invariant — a writer that produces AssetKey and a writer
// that produces OutboxKey for the same asset never collide
// in the outbox table.
func TestKeys_DistinctShapes(t *testing.T) {
	a, _ := AssetKey("artlist", "abc", "v1", "deadbeef")
	b, _ := JobKey("artlist", "abc", "v1")
	c, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v1")
	assert.Equal(t, 4, strings.Count(a, ":")+1, "AssetKey has 4 segments")
	assert.Equal(t, 3, strings.Count(b, ":")+1, "JobKey has 3 segments")
	assert.Equal(t, 4, strings.Count(c, ":")+1, "OutboxKey has 4 segments")
	// AssetKey and OutboxKey both have 4 segments but the
	// first segment is different (provider vs event_type).
	// A collision would require provider == event_type, which
	// is structurally improbable (providers are short tokens
	// like "artlist"; event_types are long dotted paths like
	// "asset.index.requested.v1"). Pin the no-collision
	// invariant with a representative case.
	assert.NotEqual(t, a, c, "AssetKey and OutboxKey for the same asset MUST differ (different first segment)")
}

// ── Determinism under repeated calls (the dedup-gate enabler) ──

// TestKeys_DeterministicAcross1000Calls pins that the
// 3 keys are byte-identical across 1000 repeated calls.
// This is the property that makes ON CONFLICT (event_key)
// DO NOTHING work — a non-deterministic key would produce
// different values across calls, defeating the dedup gate.
func TestKeys_DeterministicAcross1000Calls(t *testing.T) {
	aA, _ := AssetKey("artlist", "abc", "v1", "deadbeef")
	jA, _ := JobKey("artlist", "abc", "v1")
	oA, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v1")
	for i := 0; i < 1000; i++ {
		aB, _ := AssetKey("artlist", "abc", "v1", "deadbeef")
		jB, _ := JobKey("artlist", "abc", "v1")
		oB, _ := OutboxKey("asset.index.requested.v1", "artlist", "abc", "v1")
		if aA != aB {
			t.Fatalf("AssetKey non-deterministic at iteration %d: got %q want %q", i, aB, aA)
		}
		if jA != jB {
			t.Fatalf("JobKey non-deterministic at iteration %d: got %q want %q", i, jB, jA)
		}
		if oA != oB {
			t.Fatalf("OutboxKey non-deterministic at iteration %d: got %q want %q", i, oB, oA)
		}
	}
}

// ── Typed-sentinel identity (the godlike/07 dispatch contract) ──

// TestErrEmptyProvider_Type pins that ErrEmptyProvider is
// a typed sentinel usable with errors.Is. The sentinels
// MUST be value-identity (not constructed via fmt.Errorf %w)
// so callers can branch on them.
func TestErrEmptyProvider_Type(t *testing.T) {
	var err error = ErrEmptyProvider
	if !errors.Is(err, ErrEmptyProvider) {
		t.Errorf("ErrEmptyProvider must be a typed sentinel (errors.Is identity)")
	}
	// godlike/07 message format: contains the canonical failure
	// reason for grep-ability ("no fake availability").
	if !strings.Contains(err.Error(), "no fake availability") {
		t.Errorf("ErrEmptyProvider message must contain 'no fake availability' for grep-ability, got %q", err.Error())
	}
}

// TestErrEmptySHA256_HintsJobKey pins that the ErrEmptySHA256
// error message guides the caller to JobKey as the
// pre-download alternative. The hint is part of the
// godlike/07 fail-closed contract: the writer's
// `if sha256File == ""` check must surface a useful
// diagnostic, not just "empty input".
func TestErrEmptySHA256_HintsJobKey(t *testing.T) {
	var err error = ErrEmptySHA256
	assert.ErrorIs(t, err, ErrEmptySHA256)
	assert.Contains(t, err.Error(), "JobKey",
		"err message must hint that JobKey is the pre-download alternative")
}

// ── ':' delimiter-collision guards (Commit 1 follow-up + Commit 2 fix-up) ──

// TestAssetKey_ColonInProvider_Rejected pins the colon-collision
// guard on the ROUTING field (provider). A provider like
// "art:list" would otherwise silently produce a 5-segment key
// that any ':'-splitter would misparse.
func TestAssetKey_ColonInProvider_Rejected(t *testing.T) {
	_, err := AssetKey("art:list", "abc", "v1", "deadbeef")
	assert.Error(t, err, "AssetKey MUST reject a provider containing ':'")
	assert.ErrorIs(t, err, ErrInvalidSegment,
		"the rejection MUST be a typed ErrInvalidSegment (errors.Is dispatchable)")
	assert.Contains(t, err.Error(), "provider",
		"err message must name the offending field for operator triage")
}

// TestAssetKey_ColonInDataFields_Allowed pins the Commit 2
// follow-up relaxation: the data fields (clipID, sourceVersion,
// sha256File) are opaque and may legitimately contain ':' as a
// scheme prefix. Rejecting them would be a production-silent
// bug — stock pipeline clipIDs are "planner:<hash>:<index>" and
// source_versions are conventionally "sha256:<hex>".
func TestAssetKey_ColonInDataFields_Allowed(t *testing.T) {
	cases := []struct {
		name           string
		clipID         string
		sourceVersion  string
		sha256File     string
	}{
		{"planner-prefix-clipID", "planner:abc123:0", "v1", "deadbeef"},
		{"sha256-prefix-sourceVersion", "abc", "sha256:abc123", "deadbeef"},
		{"sha256-prefix-sha256File", "abc", "v1", "sha256:deadbeef"},
		{"planner-prefix-and-sha256", "planner:abc:0", "sha256:abc", "sha256:beef"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := AssetKey("artlist", c.clipID, c.sourceVersion, c.sha256File)
			assert.NoError(t, err, "data fields may legitimately contain ':'")
			assert.NotEmpty(t, got, "key must be constructed even when data fields contain ':'")
		})
	}
}

// TestJobKey_ColonInProvider_Rejected pins the colon-collision
// guard on the ROUTING field (provider) for the 3-tuple JobKey.
func TestJobKey_ColonInProvider_Rejected(t *testing.T) {
	_, err := JobKey("art:list", "abc", "v1")
	assert.Error(t, err, "JobKey MUST reject a provider containing ':'")
	assert.ErrorIs(t, err, ErrInvalidSegment)
	assert.Contains(t, err.Error(), "provider")
}

// TestJobKey_ColonInDataFields_Allowed mirrors the AssetKey
// acceptance test for the 3-tuple JobKey.
func TestJobKey_ColonInDataFields_Allowed(t *testing.T) {
	cases := []struct {
		name          string
		clipID        string
		sourceVersion string
	}{
		{"planner-prefix-clipID", "planner:abc123:0", "v1"},
		{"sha256-prefix-sourceVersion", "abc", "sha256:abc123"},
		{"both-with-colon", "planner:abc:0", "sha256:abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := JobKey("artlist", c.clipID, c.sourceVersion)
			assert.NoError(t, err, "data fields may legitimately contain ':'")
			assert.NotEmpty(t, got)
		})
	}
}

// TestOutboxKey_ColonInRoutingFields_Rejected pins the
// colon-collision guard on the ROUTING fields (eventType,
// provider). Both must be segment-count-stable for the
// outbox dispatcher routing.
func TestOutboxKey_ColonInRoutingFields_Rejected(t *testing.T) {
	cases := []struct {
		name       string
		eventType  string
		provider   string
		colonField string
	}{
		{"colon-in-event_type", "asset.index:requested.v1", "artlist", "event_type"},
		{"colon-in-provider", "asset.index.requested.v1", "art:list", "provider"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := OutboxKey(c.eventType, c.provider, "abc", "v1")
			assert.Error(t, err, "OutboxKey MUST reject a routing field containing ':'")
			assert.ErrorIs(t, err, ErrInvalidSegment)
			assert.Contains(t, err.Error(), c.colonField)
		})
	}
}

// TestOutboxKey_ColonInDataFields_Allowed pins the Commit 2
// follow-up relaxation for the 4-tuple OutboxKey. The data
// fields (clipID, sourceVersion) are opaque and may
// legitimately contain ':' as a scheme prefix.
func TestOutboxKey_ColonInDataFields_Allowed(t *testing.T) {
	cases := []struct {
		name          string
		clipID        string
		sourceVersion string
	}{
		{"planner-prefix-clipID", "planner:abc123:0", "v1"},
		{"sha256-prefix-sourceVersion", "abc", "sha256:abc123"},
		{"both-with-colon", "planner:abc:0", "sha256:abc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := OutboxKey("asset.index.requested.v1", "artlist", c.clipID, c.sourceVersion)
			assert.NoError(t, err, "data fields may legitimately contain ':'")
			assert.NotEmpty(t, got)
		})
	}
}

// TestErrInvalidSegment_SentinelIdentity pins the typed-
// sentinel identity for the colon-collision guard. The
// invalidSegmentError type implements Is(target error) so
// errors.Is(perFieldError, ErrInvalidSegment) is true. This
// lets callers branch on the sentinel without depending on
// the per-field error type.
func TestErrInvalidSegment_SentinelIdentity(t *testing.T) {
	_, err := AssetKey("art:list", "abc", "v1", "deadbeef")
	require.Error(t, err)
	// errors.Is dispatch: the per-field error wraps the typed
	// sentinel via the Is() method.
	if !errors.Is(err, ErrInvalidSegment) {
		t.Errorf("invalidSegmentError MUST satisfy errors.Is(err, ErrInvalidSegment)")
	}
	// The Error() method adds the field name for operator triage.
	assert.Contains(t, err.Error(), "provider",
		"err message must name the offending field")
	assert.Contains(t, err.Error(), "segment delimiter",
		"err message must explain the constraint for operator grep-ability")
}
