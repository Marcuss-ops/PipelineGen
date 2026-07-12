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
	b, _ := AssetKey("youtube", "abc", "v1", "deadbeef") // different provider
	c, _ := AssetKey("artlist", "xyz", "v1", "deadbeef") // different clipID
	d, _ := AssetKey("artlist", "abc", "v2", "deadbeef") // different sourceVersion
	e, _ := AssetKey("artlist", "abc", "v1", "cafebabe") // different sha256
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
		name          string
		clipID        string
		sourceVersion string
		sha256File    string
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

// ── BuildKey tests (FASE 5 Commit B / Commit B follow-up, July 2026) ──

// TestBuildKey_HappyPath pins the canonical 64-character lowercase
// SHA-256 hex output shape. The map content is the canonical
// artlist run-level dedup input (term + folder_id + strategy +
// dry_run + limit). The byte-stable output is critical: in-flight
// jobs queued with the legacy artlist.runDedupKey hash must MATCH
// across the migration (godlike/06 SSOT byte-stable).
func TestBuildKey_HappyPath(t *testing.T) {
	canonical := map[string]any{
		"term":           "city",
		"root_folder_id": "drive-folder",
		"strategy":       "verify",
		"dry_run":        false,
		"limit":          8,
	}
	key, err := BuildKey("artlist-run", canonical)
	require.NoError(t, err)
	// SHA-256 hex = 64 lowercase chars.
	assert.Len(t, key, 64)
	assert.Equal(t, strings.ToLower(key), key, "BuildKey MUST return lowercase hex (operator grep-ability)")

	// Byte-stability fixture (Commit B follow-up, July 2026): the
	// NEW BuildKey output MUST produce the EXACT 64-char hex the
	// legacy artlist.runDedupKey did for this canonical input.
	// Otherwise in-flight jobs queued under the legacy hash would
	// miss-dedup at the kernel job broker's UNIQUE on
	// `jobs.active_key`. The expected hex is derived off-line via
	// `echo -n '{"dry_run":false,"limit":8,"root_folder_id":"drive-folder","strategy":"verify","term":"city"}' | sha256sum`
	// (Go encoding/json sorts map keys alphabetically — the
	// canonical JSON is the sorted form, byte-exact identical to
	// the legacy runDedupKey pipeline).
	assert.Equal(t, "0b553b4dd6721826f37e70dc40f4863ef6ea8632272098b5a7b123781304a4cb", key,
		"BuildKey must produce byte-stable output identical to legacy artlist.runDedupKey — any drift breaks in-flight jobs")

	// Pin the canonical shape: a NEW producer with the same
	// canonical map must produce the same key (deterministic).
	canonical2 := map[string]any{
		"term":           "city",
		"root_folder_id": "drive-folder",
		"strategy":       "verify",
		"dry_run":        false,
		"limit":          8,
	}
	key2, err := BuildKey("artlist-run", canonical2)
	require.NoError(t, err)
	assert.Equal(t, key, key2, "BuildKey must be deterministic (same canonical + same provider → same key)")
}

// TestBuildKey_DifferentProviderSameKey pins the deliberate
// provider-validation-only invariant (Commit B byte-stability
// discipline, July 2026).
//
// The provider parameter is the VALIDATION discriminator
// (rejects empty provider + ':' in provider via typed sentinels)
// but is NOT part of the hash input. Two callers with different
// provider discriminators ("artlist-run" vs "stock-run") and
// IDENTICAL canonical content produce IDENTICAL SHA-256 hex
// keys.
//
// Why: the design verdict REQUIRED byte-stability with the legacy
// artlist.runDedupKey to avoid breaking in-flight jobs queued
// under the legacy hash. The legacy runDedupKey did NOT include
// the provider discriminator in its canonical map — only the
// 5 dedup segments (term + root_folder_id + strategy + dry_run
// + limit). Adding the provider to the hash would have moved
// every existing job's active_key to a different byte sequence,
// defeating the cross-deploy dedup that the kernel job broker's
// UNIQUE on `jobs.active_key` is supposed to preserve.
//
// godlike/06 SSOT rationale: the canonical identity for a
// run-level dedup key IS the (canonical-map content). The
// provider parameter is a SIDE-VALIDATION that ensures the
// caller identified which provider-family owns this key shape
// (so a caller can't accidentally invoke BuildKey with an empty
// discriminator or a ':'-bearing one). The byte output is
// SHA256(json.Marshal(canonical)) — provider only gates the
// pre-hash validation, not the hash itself.
//
// Cross-package dedup: future stock-run / youtube-run callers
// that delegate to BuildKey are safe from each other's in-flight
// jobs because their canonical content will DIFFER (different
// field set: stock uses ticker+date, youtube uses video+stage).
// The provider validation is a wire-shape invariant, not a
// hash-mixing key.
func TestBuildKey_DifferentProviderSameKey(t *testing.T) {
	canonical := map[string]any{
		"term": "city",
	}
	a, _ := BuildKey("artlist-run", canonical)
	b, _ := BuildKey("stock-run", canonical)
	c, _ := BuildKey("youtube-run", canonical)
	assert.Equal(t, a, b, "provider validation only — same canonical content MUST produce same hash (byte-stability with legacy runDedupKey)")
	assert.Equal(t, a, c, "provider validation only — same canonical content MUST produce same hash (byte-stability with legacy runDedupKey)")
	assert.Equal(t, b, c, "provider validation only — same canonical content MUST produce same hash (byte-stability with legacy runDedupKey)")
}

// TestBuildKey_DifferentCanonicalDifferentKey pins that each
// canonical-segment-field is part of the identity. A future
// refactor that drops one segment would be caught here.
func TestBuildKey_DifferentCanonicalDifferentKey(t *testing.T) {
	base := map[string]any{"term": "city", "limit": 8}
	a, _ := BuildKey("artlist-run", base)
	b, _ := BuildKey("artlist-run", map[string]any{"term": "city", "limit": 16})
	c, _ := BuildKey("artlist-run", map[string]any{"term": "ocean", "limit": 8})
	d, _ := BuildKey("artlist-run", map[string]any{"term": "city", "limit": 8, "strategy": "verify"})
	assert.NotEqual(t, a, b, "different limit → different key")
	assert.NotEqual(t, a, c, "different term → different key")
	assert.NotEqual(t, a, d, "extra segment → different key")
}

// TestBuildKey_EmptyProvider_ReturnsErrInvalidRunForDedup pins
// the fail-closed guard: a missing provider discriminator is the
// canonical "caller forgot to thread the provider" wiring bug.
func TestBuildKey_EmptyProvider_ReturnsErrInvalidRunForDedup(t *testing.T) {
	_, err := BuildKey("", map[string]any{"term": "city"})
	assert.ErrorIs(t, err, ErrInvalidRunForDedup,
		"empty provider MUST trip ErrInvalidRunForDedup (godlike/07 no fake availability)")
}

// TestBuildKey_NilCanonical_ReturnsErrInvalidRunForDedup pins
// the empty-canonical guard. A nil canonical OR an empty
// (len==0) canonical are distinct code paths but produce the
// same sentinel.
func TestBuildKey_NilCanonical_ReturnsErrInvalidRunForDedup(t *testing.T) {
	_, err := BuildKey("artlist-run", nil)
	assert.ErrorIs(t, err, ErrInvalidRunForDedup,
		"nil canonical MUST trip ErrInvalidRunForDedup (operator-input error)")

	_, err = BuildKey("artlist-run", map[string]any{})
	assert.ErrorIs(t, err, ErrInvalidRunForDedup,
		"empty canonical MUST trip ErrInvalidRunForDedup (operator-input error)")
}

// TestBuildKey_ColonInProvider_ReturnsErrInvalidSegment pins the
// provider-discriminator segment-collision guard. The provider
// parameter IS a routing field (BuildKey is the canonical for the
// run-level discriminator); a ':' would silently produce a key
// that any future ':'-splitter would misparse.
func TestBuildKey_ColonInProvider_ReturnsErrInvalidSegment(t *testing.T) {
	_, err := BuildKey("art:list-run", map[string]any{"term": "city"})
	assert.ErrorIs(t, err, ErrInvalidSegment,
		"colon in provider discriminator MUST trip ErrInvalidSegment (godlike/06 segment stability)")
}

// TestBuildKey_ByteStableAcrossMapKeyOrder pins the canonical
// determinism invariant: Go's encoding/json sorts map keys
// alphabetically (stdlib contract for map[string]any), so two
// callers that build the SAME canonical content with DIFFERENT
// insertion orderings MUST get the same SHA-256 hex.
func TestBuildKey_ByteStableAcrossMapKeyOrder(t *testing.T) {
	a := map[string]any{}
	a["term"] = "city"
	a["limit"] = 8
	a["root_folder_id"] = "drive-folder"

	b := map[string]any{}
	b["root_folder_id"] = "drive-folder"
	b["term"] = "city"
	b["limit"] = 8

	c := map[string]any{}
	c["limit"] = 8
	c["root_folder_id"] = "drive-folder"
	c["term"] = "city"

	keyA, _ := BuildKey("artlist-run", a)
	keyB, _ := BuildKey("artlist-run", b)
	keyC, _ := BuildKey("artlist-run", c)

	assert.Equal(t, keyA, keyB, "BuildKey must be byte-stable across insertion order (1)")
	assert.Equal(t, keyA, keyC, "BuildKey must be byte-stable across insertion order (2)")
}

// TestBuildKey_DoesNotPanicOnMarshalableSubset pins the canonical
// happy path for each segment type the canonical map can carry
// (bool, int, string). The contract: BuildKey MUST accept any
// JSON-marshalable combination without panicking (godlike/07).
func TestBuildKey_DoesNotPanicOnMarshalableSubset(t *testing.T) {
	cases := []map[string]any{
		{"term": "city"},
		{"term": "city", "limit": 0},      // limit=0 is allowed (operator typed 0 → canonical 0)
		{"term": "city", "dry_run": true}, // bool canonical
		{"term": "", "limit": 8},          // empty DATA string allowed (operator fallback)
	}
	for i, c := range cases {
		key, err := BuildKey("artlist-run", c)
		assert.NoError(t, err, "case %d should not error", i)
		assert.Len(t, key, 64, "case %d must return 64-char hex", i)
	}
}

// TestErrInvalidRunForDedup_Type pins typed-sentinel identity
// (errors.Is dispatchable). Callers branch on errors.Is so the
// sentinel MUST be value-identity, not fmt.Errorf %w.
func TestErrInvalidRunForDedup_Type(t *testing.T) {
	var err error = ErrInvalidRunForDedup
	if !errors.Is(err, ErrInvalidRunForDedup) {
		t.Errorf("ErrInvalidRunForDedup must be a typed sentinel (errors.Is identity)")
	}
	assert.Contains(t, err.Error(), "no fake availability",
		"err message must contain 'no fake availability' for grep-ability")
	assert.Contains(t, err.Error(), "BuildKey",
		"err message must point the caller to BuildKey as the canonical owner")
}

// ── BuildKeyString tests (Commit A follow-up, July 2026) ──
//
// BuildKeyString is the BYTE-STABLE delegation surface for callers
// whose canonical content is already a pre-joined string (the
// stock/enrichment EnrichmentIdempotencyKey migration). Tests below
// pin: (1) happy-path byte-stability across N retries; (2) per-raw
// identity (different raw strings → different keys); (3) provider
// validation-only (different providers with same raw → same hash,
// per the byte-stability-with-legacy-rationale documented on
// BuildKey); (4) fail-closed guards (empty provider, empty raw,
// colon-in-provider); (5) byte-identity with the legacy
// hashutil.SHA256String helper (the load-bearing migration pin).

// TestBuildKeyString_HappyPath pins the canonical 64-char hex
// shape + byte-stability across N retries. The fixture pins a
// known SHA-256 hex so a future drift (e.g. someone adds the
// provider to the hash pipeline, breaking byte-equality with the
// legacy enrichment hash) fails this test loudly.
func TestBuildKeyString_HappyPath(t *testing.T) {
	raw := "stock:run_1b25ac8e5470:chunk:0:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789:v1"
	key, err := BuildKeyString("stock-enrich", raw)
	require.NoError(t, err)
	assert.Len(t, key, 64, "BuildKeyString must return 64-char hex")
	assert.Equal(t, strings.ToLower(key), key, "BuildKeyString MUST return lowercase hex")

	// Determinism across N retries.
	for i := 0; i < 1000; i++ {
		k, err := BuildKeyString("stock-enrich", raw)
		require.NoError(t, err)
		if k != key {
			t.Fatalf("retry %d: key drift: got %q, want %q", i, k, key)
		}
	}

	// Byte-stability fixture (Commit A, July 2026): the hash
	// computed for the canonical stock-enrichment input MUST be
	// byte-identical to what the legacy enrichment idempotency
	// helper produced (hashutil.SHA256String(raw)). The expected
	// hex is the SHA-256 of the 97-byte pre-joined string above.
	// Operators verify off-line via `echo -n '<raw>' | sha256sum`.
	assert.Equal(t, "ba93a47600b9bce576d7d7562629dc8ca01c8ff1d715284ad60c4161d6e3ccfd", key,
		"BuildKeyString must produce byte-stable output identical to legacy stock enrichment idempotency helper (in-flight outbox events rely on this)")
}

// TestBuildKeyString_DifferentRawStringDifferentKey pins that
// each character of the raw input is part of the identity. A
// future refactor that drops a segment would be caught here.
func TestBuildKeyString_DifferentRawStringDifferentKey(t *testing.T) {
	a, _ := BuildKeyString("stock-enrich", "chunk-0")
	b, _ := BuildKeyString("stock-enrich", "chunk-1")
	c, _ := BuildKeyString("stock-enrich", "chunk-0:v1")
	d, _ := BuildKeyString("stock-enrich", "chunk-0:v2")
	assert.NotEqual(t, a, b, "different chunkID → different key")
	assert.NotEqual(t, a, c, "different version suffix → different key")
	assert.NotEqual(t, c, d, "different version literal → different key")
}

// TestBuildKeyString_DifferentProviderSameKey pins the deliberate
// provider-validation-only invariant (Commit A, July 2026).
// Identical rationale to BuildKey: provider is validation-only
// (rejects empty + ':' via sentinels) but is NOT part of the
// hash input. Two callers with different provider discriminators
// ("stock-enrich" vs "youtube-enrich") and the SAME raw content
// produce IDENTICAL SHA-256 hex keys.
//
// Why: byte-stability discipline. The legacy hashutil helper
// produced `sha256.Sum256([]byte(raw))` — no provider prefix.
// Adding the provider to the hash pipeline would have moved
// every existing key to a different byte sequence, breaking
// in-flight outbox events queued under the legacy hash. future
// youtube.RunDedupKey + stock.RunDedupKey callers that delegate
// to BuildKey / BuildKeyString are safe from each other's in-
// flight keys because their raw content differs.
func TestBuildKeyString_DifferentProviderSameKey(t *testing.T) {
	raw := "chunk-0:abc:v1"
	a, _ := BuildKeyString("stock-enrich", raw)
	b, _ := BuildKeyString("youtube-enrich", raw)
	c, _ := BuildKeyString("artlist-run", raw)
	assert.Equal(t, a, b, "provider validation only — byte-stability discipline")
	assert.Equal(t, a, c, "provider validation only — byte-stability discipline")
	assert.Equal(t, b, c, "provider validation only — byte-stability discipline")
}

// TestBuildKeyString_EmptyProvider_ReturnsErrInvalidRunForDedup
// pins the godlike/07 fail-closed guard for the validation-only
// parameter.
func TestBuildKeyString_EmptyProvider_ReturnsErrInvalidRunForDedup(t *testing.T) {
	_, err := BuildKeyString("", "any-raw")
	assert.ErrorIs(t, err, ErrInvalidRunForDedup)
}

// TestBuildKeyString_EmptyRaw_ReturnsErrInvalidRunForDedup pins
// the godlike/07 fail-closed guard for the HASH input. An empty
// raw is the canonical "caller produced an empty join" wiring
// bug — fail-closed with ErrInvalidRunForDedup so the gap is
// visible at the hash construction site rather than silently
// producing a key for an empty byte sequence (which would
// SHA-256 to a deterministic but invalid 64-char hex).
func TestBuildKeyString_EmptyRaw_ReturnsErrInvalidRunForDedup(t *testing.T) {
	_, err := BuildKeyString("stock-enrich", "")
	assert.ErrorIs(t, err, ErrInvalidRunForDedup,
		"empty raw MUST trip ErrInvalidRunForDedup (godlike/07 no fake availability)")
}

// TestBuildKeyString_ColonInProvider_ReturnsErrInvalidSegment
// pins the segment-collision guard (same as BuildKey).
func TestBuildKeyString_ColonInProvider_ReturnsErrInvalidSegment(t *testing.T) {
	_, err := BuildKeyString("stock:enrich", "any-raw")
	assert.ErrorIs(t, err, ErrInvalidSegment,
		"colon in provider MUST trip ErrInvalidSegment (godlike/06 segment stability)")
}
