package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVisualUsageTracker_Rotation pins that repeated appearances of the same
// entity cycle through its assets (round-robin) instead of always returning
// the highest-quality first candidate.
func TestVisualUsageTracker_Rotation(t *testing.T) {
	tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())
	candidates := []string{"floyd_001", "floyd_002", "floyd_003"}

	got := make([]string, 0, len(candidates))
	for i := 0; i < len(candidates); i++ {
		got = append(got, tracker.Pick("person:floyd", candidates, int64(i)).AssetID)
	}
	require.ElementsMatch(t, candidates, got, "one pass must cycle through every candidate exactly once")
	// The first pick starts at the first candidate (round-robin pointer 0).
	require.Equal(t, "floyd_001", got[0])
}

// TestVisualUsageTracker_SameAssetCooldown pins that an asset used less than
// the cooldown ago is not reused while a fresh alternative exists.
func TestVisualUsageTracker_SameAssetCooldown(t *testing.T) {
	tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())
	candidates := []string{"floyd_001", "floyd_002"}

	first := tracker.Pick("person:floyd", candidates, 0)
	require.Equal(t, "floyd_001", first.AssetID)

	// 1ms later: floyd_001 is still in cooldown (15s), so the fresh floyd_002 wins.
	second := tracker.Pick("person:floyd", candidates, 1_000)
	require.Equal(t, "floyd_002", second.AssetID)
	require.False(t, second.ForceVariant)
}

// TestVisualUsageTracker_FallbackForcesVariant pins that when every asset is
// still in cooldown the tracker reuses the least-recently-used one and flags
// ForceVariant so the sampler compensates with a different treatment.
func TestVisualUsageTracker_FallbackForcesVariant(t *testing.T) {
	tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())
	candidates := []string{"floyd_001"}

	first := tracker.Pick("person:floyd", candidates, 0)
	require.Equal(t, "floyd_001", first.AssetID)
	require.False(t, first.ForceVariant)

	second := tracker.Pick("person:floyd", candidates, 5_000)
	require.Equal(t, "floyd_001", second.AssetID, "no fresh alternative: same asset reused")
	require.True(t, second.ForceVariant, "forced reuse must signal a variant")
}

// TestVisualUsageTracker_FallbackLeastRecentlyUsed pins that among multiple
// in-cooldown assets the oldest last-use wins (never-used assets first).
func TestVisualUsageTracker_FallbackLeastRecentlyUsed(t *testing.T) {
	tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())
	candidates := []string{"floyd_001", "floyd_002"}

	// Use floyd_001 at t=0 and floyd_002 at t=1ms, then force a fallback before
	// either cooldown expires. floyd_001 (used earliest) must be reused.
	tracker.Pick("person:floyd", candidates, 0)
	tracker.Pick("person:floyd", candidates, 1_000)
	got := tracker.Pick("person:floyd", candidates, 2_000)
	require.Equal(t, "floyd_001", got.AssetID, "least-recently-used asset must be reused first")
	require.True(t, got.ForceVariant)
}

// TestVisualUsageTracker_FreshPreferredOverCooldown pins that a fresh asset is
// always preferred over forcing a variant of a used one: with three assets and
// three rapid picks, each pick returns a distinct fresh asset (no variant), and
// only the fourth pick — when all three are in cooldown — forces a variant.
func TestVisualUsageTracker_FreshPreferredOverCooldown(t *testing.T) {
	tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())
	candidates := []string{"floyd_001", "floyd_002", "floyd_003"}

	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		pick := tracker.Pick("person:floyd", candidates, int64(i*1_000))
		require.False(t, pick.ForceVariant, "pick %d has a fresh alternative", i)
		require.False(t, seen[pick.AssetID], "fresh picks must be distinct, %s repeated", pick.AssetID)
		seen[pick.AssetID] = true
	}
	require.Len(t, seen, 3)

	forced := tracker.Pick("person:floyd", candidates, 4_000)
	require.True(t, forced.ForceVariant, "all assets in cooldown → forced variant")
}

// TestVisualUsageTracker_CooldownExpiry pins that once the cooldown has passed
// the asset is fresh again and no variant is forced.
func TestVisualUsageTracker_CooldownExpiry(t *testing.T) {
	tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())
	candidates := []string{"floyd_001"}

	tracker.Pick("person:floyd", candidates, 0)
	got := tracker.Pick("person:floyd", candidates, 15_000_001)
	require.Equal(t, "floyd_001", got.AssetID)
	require.False(t, got.ForceVariant, "cooldown expired: asset is fresh again")
}

// TestVisualUsageTracker_EmptyCandidates pins the nil-safe / no-candidate
// behavior.
func TestVisualUsageTracker_EmptyCandidates(t *testing.T) {
	tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())
	require.Equal(t, UsagePick{}, tracker.Pick("person:floyd", nil, 0))
	require.Equal(t, UsagePick{}, tracker.Pick("person:floyd", []string{}, 0))

	var nilTracker *VisualUsageTracker
	require.Equal(t, UsagePick{}, nilTracker.Pick("person:floyd", []string{"floyd_001"}, 0))
}

// TestVisualUsageTracker_PerEntityIsolation pins that rotation state is keyed
// by entity id: two entities do not share the round-robin pointer (verified
// with disjoint asset sets so the global per-asset cooldown cannot interfere).
func TestVisualUsageTracker_PerEntityIsolation(t *testing.T) {
	tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())

	firstFloyd := tracker.Pick("person:floyd", []string{"floyd_001", "floyd_002"}, 0)
	firstCanelo := tracker.Pick("person:canelo", []string{"canelo_001", "canelo_002"}, 0)
	require.Equal(t, "floyd_001", firstFloyd.AssetID)
	require.Equal(t, "canelo_001", firstCanelo.AssetID, "each entity starts its own rotation at index 0")
}

// TestVisualUsageTracker_Deterministic pins that the same call sequence yields
// the same picks across two independently built trackers.
func TestVisualUsageTracker_Deterministic(t *testing.T) {
	run := func() []UsagePick {
		tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())
		candidates := []string{"floyd_001", "floyd_002", "floyd_003"}
		picks := make([]UsagePick, 0, 5)
		for i := int64(0); i < 5; i++ {
			picks = append(picks, tracker.Pick("person:floyd", candidates, i*1_000))
		}
		return picks
	}
	require.Equal(t, run(), run())
}

// TestVisualUsageTracker_Reset pins that Reset clears both rotation and cooldown
// state, allowing a fresh identical sequence.
func TestVisualUsageTracker_Reset(t *testing.T) {
	tracker := NewVisualUsageTracker(DefaultUsageTrackerConfig())
	candidates := []string{"floyd_001", "floyd_002"}

	tracker.Pick("person:floyd", candidates, 0)
	tracker.Pick("person:floyd", candidates, 1_000)
	tracker.Reset()

	got := tracker.Pick("person:floyd", candidates, 2_000)
	require.Equal(t, "floyd_001", got.AssetID, "after Reset rotation restarts at index 0")
	require.False(t, got.ForceVariant, "after Reset no asset is in cooldown")
}
