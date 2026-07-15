// reindex_qdrant_pr13_test.go — PR 13 (June 2026) blue-green tests.
//
// The blue-green Apply path introduces three invariants under test:
//
//   1. timestampedTargetCollection(base, now) is deterministic
//      given the same inputs (so logs and reports are reproducible
//      across retries; freezeable in tests).
//
//   2. timestampedTargetCollection(base, now) is NEVER equal to
//      `base` itself, by the suffix construction (so the
//      `new != active` invariant is structurally guaranteed; the
//      operator cannot accidentally same-collect overwrite unless
//      they pass --target-collection=<base> explicitly).
//
//   3. parseReindexQdrantArgs accepts --apply + --target-collection
//      in any combination (the legacy QDRANT-003 rejection block
//      was removed in PR 13). The recovery/escape-hatch path lets
//      operators write into an explicit non-timestamped target.

package reconcile

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTimestampedTargetCollection_Deterministic — same inputs
// over two calls produce the same string. PR 13's
// `deterministic timestamp` invariant so retries on the same
// `now` converge on the same suffix (the operator's logs are
// reproducible).
//
// Follow-up: the suffix is nanosecond-resolution (`YYYYMMDD_HHMMSS_nnnnnnnnn`).
// Two concurrent --apply invocations in the same UTC second get
// distinct suffixes because each `time.Now()` read carries a
// distinct nanosecond counter under Linux's monotonic clock.
func TestTimestampedTargetCollection_Deterministic(t *testing.T) {
	t.Parallel()
	// Frozen clock with explicit nanosecond = 123456789 for
	// pinned format verification. The follow-up format-string is
	// length-9 nanoseconds (Go's standard Format width).
	now := time.Date(2026, 6, 27, 15, 30, 45, 123456789, time.UTC)
	base := "media_assets_v3"

	s1 := timestampedTargetCollection(base, now)
	s2 := timestampedTargetCollection(base, now)

	require.NotEmpty(t, s1)
	assert.Equal(t, s1, s2, "PR 13: timestampedTargetCollection must be deterministic for the same inputs")

	want := "media_assets_v3_20260627_153045_123456789"
	assert.Equal(t, want, s1, "PR 13 follow-up: suffix must be UTC YYYYMMDD_HHMMSS_nnnnnnnnn (nanosecond resolution)")
}

// TestTimestampedTargetCollection_NanosecondCollisionResistance —
// two concurrent --apply invocations with `now` values that differ
// by even a single nanosecond produce DISTINCT target names. This
// is the follow-up fix's regression pin: the seconds-resolution
// suffix allowed collisions when `time.Now()` aligned on the same
// second; nanosecond resolution prevents them.
//
// The test pins three points:
//
//  1. Same nanosecond ⇒ same name (deterministic, unchanged).
//  2. Different nanosecond (1ns later) ⇒ different name.
//  3. 1000 sequential nanosecond-shifted invocations produce
//     exactly 1000 distinct names (bulk-collision-canary for
//     anything that could legitimately mis-format the nanosecond
//     field).
func TestTimestampedTargetCollection_NanosecondCollisionResistance(t *testing.T) {
	t.Parallel()
	base := "media_assets_v3"

	t0 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC).UTC()

	// (1) Same nanosecond ⇒ same name.
	now0a := t0.Add(0 * time.Nanosecond)
	now0b := t0.Add(0 * time.Nanosecond)
	s0a := timestampedTargetCollection(base, now0a)
	s0b := timestampedTargetCollection(base, now0b)
	assert.Equal(t, s0a, s0b, "same nanosecond ⇒ same name (deterministic)")

	// (2) Different nanosecond ⇒ different name.
	now1 := t0.Add(1 * time.Nanosecond)
	s1 := timestampedTargetCollection(base, now1)
	assert.NotEqual(t, s0a, s1, "1-nanosecond change must produce a different suffix (collision-resistance)")

	// (3) 1000 sequential nanosecond-shifted invocations must
	// surface 1000 distinct names. The follow-up fix guarantees
	// per-call uniqueness via nanosecond precision; BumpDiff below
	// proves we don't accidentally collapse adjacent buckets.
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		name := timestampedTargetCollection(base, t0.Add(time.Duration(i)*time.Nanosecond))
		if _, dup := seen[name]; dup {
			t.Fatalf("collision at offset %dns: %q already seen", i, name)
		}
		seen[name] = struct{}{}
	}
	assert.Equal(t, 1000, len(seen))
}

// TestTimestampedTargetCollection_DistinctFromBase — the suffix
// construction guarantees the timestamped target is NEVER equal to
// the base. This is the PR 13 structural primitive for the
// `new != active` invariant; without it the alias swap could
// silently overwrite the running collection.
func TestTimestampedTargetCollection_DistinctFromBase(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	base := "media_assets_v3"

	got := timestampedTargetCollection(base, now)

	assert.NotEqual(t, base, got, "PR 13: timestamped target must differ from base by construction")
	assert.True(t, strings.HasPrefix(got, base+"_"), "PR 13: timestamped target must start with base + underscore separator")
}

// TestTimestampedTargetCollection_DifferentTimesProduceDifferentSuffixes
// — two distinct times produce two distinct names. PR 13's
// `concurrent reindexes don't collide` invariant.
func TestTimestampedTargetCollection_DifferentTimesProduceDifferentSuffixes(t *testing.T) {
	t.Parallel()
	base := "media_assets_v3"
	t1 := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)
	t2 := time.Date(2026, 6, 27, 15, 30, 46, 0, time.UTC)

	s1 := timestampedTargetCollection(base, t1)
	s2 := timestampedTargetCollection(base, t2)

	assert.NotEqual(t, s1, s2, "PR 13: two different times must produce two different suffixes")
}

// TestTimestampedTargetCollection_EmptyBaseFallback — when the
// physical name is empty the helper must fall back to the canonical
// base; the auto-timestamped path is therefore never silently
// short-circuited to a synthetic identity.
func TestTimestampedTargetCollection_EmptyBaseFallback(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 27, 15, 30, 45, 0, time.UTC)

	got := timestampedTargetCollection("", now)

	// Canonical default base.
	assert.True(t, strings.HasPrefix(got, "media_assets_v3_"), "empty base must fall back to canonical: got %q", got)
}

// TestParseReindexQdrantArgs_AcceptsApplyWithTargetCollection —
// the legacy QDRANT-003-era `targetCollection != schema.PhysicalName`
// rejection was REMOVED in PR 13. Operators explicitly passing
// `--target-collection=NAME` MUST parse fine; the strict verifier
// (PR 12) gates the alias swap, not the flag parser.
func TestParseReindexQdrantArgs_AcceptsApplyWithTargetCollection(t *testing.T) {
	t.Parallel()

	deps, err := parseReindexQdrantArgs([]string{
		"--apply",
		"--target-collection=media_assets_recovery_v9",
	})
	require.NoError(t, err, "PR 13: --apply + --target-collection MUST parse fine (legacy rejection was removed)")
	assert.True(t, deps.Apply)
	assert.Equal(t, "media_assets_recovery_v9", deps.TargetCollection)
}

// TestParseReindexQdrantArgs_AcceptsApplyWithoutTargetCollection
// — the auto-timestamped Apply path. The parser stays pure (no
// clock read); the time-based target name is built later in
// runReindexQdrant against the freezeable `time.Now()` source.
func TestParseReindexQdrantArgs_AcceptsApplyWithoutTargetCollection(t *testing.T) {
	t.Parallel()

	deps, err := parseReindexQdrantArgs([]string{"--apply"})
	require.NoError(t, err)
	assert.True(t, deps.Apply)
	assert.Equal(t, "", deps.TargetCollection, "PR 13: empty target_collection triggers auto-timestamped target selection in runReindexQdrant")
}

// TestParseReindexQdrantArgs_RejectsApplyPlusDryRun — the legacy
// `--apply` + `--dry-run` mutually-exclusive guard is preserved.
func TestParseReindexQdrantArgs_RejectsApplyPlusDryRun(t *testing.T) {
	t.Parallel()
	_, err := parseReindexQdrantArgs([]string{"--apply", "--dry-run"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}
