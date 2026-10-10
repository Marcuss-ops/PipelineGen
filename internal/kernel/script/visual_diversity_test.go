package script

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func diversityOptions() []RecipeOption {
	return []RecipeOption{
		{RecipeID: "data_histogram_stagger_up", Family: "bars", MotionID: "rise", Density: 2, Eligible: true, ProductID: "p"},
		{RecipeID: "data_line_trace", Family: "line", MotionID: "trace", Density: 1, Eligible: true, ProductID: "p"},
		{RecipeID: "data_counter_roll", Family: "kpi", MotionID: "roll", Density: 0, Eligible: true, ProductID: "p"},
	}
}

func TestDiversityTenScenesVaryWhenCompatible(t *testing.T) {
	seed := "job-seed-001"
	var mem DiversityMemory
	used := map[string]int{}
	var first PlannedVariant
	for i := 0; i < 10; i++ {
		scene := fmt.Sprintf("scene-%02d", i)
		got, ok := PlanVariant(seed, scene, diversityOptions(), mem, []string{"fade", "wipe"})
		require.True(t, ok)
		if i == 0 {
			first = got
		}
		used[got.RecipeID]++
		mem.RecentRecipes = append([]string{got.RecipeID}, mem.RecentRecipes...)
		if len(mem.RecentRecipes) > 6 {
			mem.RecentRecipes = mem.RecentRecipes[:6]
		}
		mem.RecentMotions = append([]string{got.MotionID}, mem.RecentMotions...)
		if len(mem.RecentMotions) > 6 {
			mem.RecentMotions = mem.RecentMotions[:6]
		}
	}
	assert.Greater(t, len(used), 1, "ten scenes must not repeat one recipe when equivalents exist")

	// Determinism: same seed and history replays the first choice.
	again, ok := PlanVariant(seed, "scene-00", diversityOptions(), DiversityMemory{}, []string{"fade", "wipe"})
	require.True(t, ok)
	assert.Equal(t, first, again, "output must be deterministic per seed")
}

func TestDiversityNeverOverridesEligibility(t *testing.T) {
	only := []RecipeOption{
		{RecipeID: "data_histogram_stagger_up", Family: "bars", MotionID: "rise", Density: 2, Eligible: true, ProductID: "p"},
		{RecipeID: "data_pie_fancy", Family: "pie", MotionID: "spin", Density: 2, Eligible: false, ProductID: "p"},
	}
	mem := DiversityMemory{RecentRecipes: []string{"data_histogram_stagger_up", "data_histogram_stagger_up"}}
	for i := 0; i < 5; i++ {
		got, ok := PlanVariant("s", fmt.Sprintf("scene-%d", i), only, mem, nil)
		require.True(t, ok)
		assert.Equal(t, "data_histogram_stagger_up", got.RecipeID,
			"variety must never pick an ineligible recipe, even to avoid repetition")
	}
	_, ok := PlanVariant("s", "scene-x", []RecipeOption{{RecipeID: "x", Eligible: false}}, DiversityMemory{}, nil)
	assert.False(t, ok, "no eligible recipe means no plan, not a fabricated one")
}
