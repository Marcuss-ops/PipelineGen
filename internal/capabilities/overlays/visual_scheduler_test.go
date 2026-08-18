package overlays

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const ms = int64(1_000_000)

func TestDefaultSchedulerConfig(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	require.Equal(t, 2, cfg.MaxConcurrent)
	require.Equal(t, int64(800_000), cfg.MinGapUS)
	require.Equal(t, int64(15_000_000), cfg.EntityCooldownUS)
}

// TestVisualScheduler_MaxConcurrent pins the concurrency cap: a third overlay
// overlapping the same window is dropped (starts are staggered ≥800ms so only
// the concurrency rule fires).
func TestVisualScheduler_MaxConcurrent(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "a", Priority: 100, StartUS: 1 * ms, DurationUS: 2 * ms},
		{IntentID: "b", Priority: 90, StartUS: 1_800_000, DurationUS: 1 * ms},
		{IntentID: "c", Priority: 80, StartUS: 2_600_000, DurationUS: 1 * ms},
	}
	got := DefaultVisualScheduler.Schedule(intents, DefaultSchedulerConfig())
	require.Equal(t, []string{"a", "b"}, intentIDs(got))
}

// TestVisualScheduler_MinGap pins the start-spacing rule: two intents that
// begin within 800ms of each other are not both kept, even when concurrency
// would allow them (MaxConcurrent=2).
func TestVisualScheduler_MinGap(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "a", Priority: 100, StartUS: 1 * ms, DurationUS: 1 * ms},
		{IntentID: "b", Priority: 90, StartUS: 1_500_000, DurationUS: 1 * ms},
	}
	cfg := DefaultSchedulerConfig()
	cfg.MaxConcurrent = 2
	got := DefaultVisualScheduler.Schedule(intents, cfg)
	require.Equal(t, []string{"a"}, intentIDs(got))
}

// TestVisualScheduler_EntityCooldown pins the same-entity cooldown: the same
// entity cannot reappear within 15s of its previous occurrence's end.
func TestVisualScheduler_EntityCooldown(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "first", EntityID: "person:floyd-mayweather-jr", Priority: 100, StartUS: 1 * ms, DurationUS: 1 * ms},
		{IntentID: "tooSoon", EntityID: "person:floyd-mayweather-jr", Priority: 90, StartUS: 5 * ms, DurationUS: 1 * ms},
		{IntentID: "later", EntityID: "person:floyd-mayweather-jr", Priority: 90, StartUS: 20 * ms, DurationUS: 1 * ms},
	}
	got := DefaultVisualScheduler.Schedule(intents, DefaultSchedulerConfig())
	// "tooSoon" (5s < 2s end + 15s) is dropped; "later" (20s ≥ 17s) survives.
	require.Equal(t, []string{"first", "later"}, intentIDs(got))
}

// TestVisualScheduler_PriorityWins pins that on a temporal conflict the
// higher-priority intent survives (MaxConcurrent=1 here).
func TestVisualScheduler_PriorityWins(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "low", Priority: 60, StartUS: 1 * ms, DurationUS: 1 * ms},
		{IntentID: "high", Priority: 100, StartUS: 1_200_000, DurationUS: 1 * ms},
	}
	cfg := SchedulerConfig{MaxConcurrent: 1}
	got := DefaultVisualScheduler.Schedule(intents, cfg)
	require.Equal(t, []string{"high"}, intentIDs(got))
}

func TestVisualScheduler_Deterministic(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "a", Priority: 100, StartUS: 1 * ms, DurationUS: 2 * ms},
		{IntentID: "b", Priority: 90, StartUS: 1_800_000, DurationUS: 1 * ms},
		{IntentID: "c", Priority: 80, StartUS: 2_600_000, DurationUS: 1 * ms},
	}
	first := intentIDs(DefaultVisualScheduler.Schedule(intents, DefaultSchedulerConfig()))
	for i := 0; i < 100; i++ {
		require.Equal(t, first, intentIDs(DefaultVisualScheduler.Schedule(intents, DefaultSchedulerConfig())))
	}
}

func TestVisualScheduler_PreservesOriginalOrder(t *testing.T) {
	intents := []VisualIntent{
		{IntentID: "z", Priority: 60, StartUS: 50 * ms, DurationUS: 1 * ms},
		{IntentID: "a", Priority: 100, StartUS: 1 * ms, DurationUS: 1 * ms},
		{IntentID: "m", Priority: 80, StartUS: 30 * ms, DurationUS: 1 * ms},
	}
	got := DefaultVisualScheduler.Schedule(intents, DefaultSchedulerConfig())
	// All fit (no temporal conflict), order preserved as in the input.
	require.Equal(t, []string{"z", "a", "m"}, intentIDs(got))
}

func TestVisualScheduler_Empty(t *testing.T) {
	require.Nil(t, DefaultVisualScheduler.Schedule(nil, DefaultSchedulerConfig()))
	require.Empty(t, DefaultVisualScheduler.Schedule([]VisualIntent{}, DefaultSchedulerConfig()))
}

func TestVisualScheduler_NormalizesDefaults(t *testing.T) {
	// A zero config still enforces the canonical defaults.
	intents := []VisualIntent{
		{IntentID: "a", Priority: 100, StartUS: 1 * ms, DurationUS: 2 * ms},
		{IntentID: "b", Priority: 90, StartUS: 1_500_000, DurationUS: 1 * ms},
	}
	got := DefaultVisualScheduler.Schedule(intents, SchedulerConfig{})
	require.Equal(t, []string{"a"}, intentIDs(got))
}
