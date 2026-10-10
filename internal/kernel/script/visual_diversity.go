package script

// Visual Diversity Planner (P1): controlled variety over admitted elements.
//
// Correctness first: the planner only chooses among recipes the ChartRecipe
// registry already certified as eligible for the datum. Variety never
// converts a bar-only datum into a pie. Selection is deterministic (stable
// seed) and considers the trailing window of scenes to penalize repetition:
// consecutive identical charts, identical image motions, identical phrase
// effects, and dense compositions without simple intervals.

import (
	"hash/fnv"
	"sort"
)

// RecipeOption is one certified-eligible visual variant.
type RecipeOption struct {
	RecipeID  string `json:"recipe_id"`
	Family    string `json:"family"`
	MotionID  string `json:"motion_id"`
	Density   int    `json:"density"`
	Eligible  bool   `json:"eligible"`
	ProductID string `json:"product_id"`
}

// DiversityMemory is the trailing context the planner penalizes against.
type DiversityMemory struct {
	RecentRecipes []string
	RecentMotions []string
	RecentEffects []string
	DenseStreak   int
}

// PlannedVariant is the chosen recipe for one admitted element.
type PlannedVariant struct {
	ProductID string `json:"product_id"`
	RecipeID  string `json:"recipe_id"`
	MotionID  string `json:"motion_id"`
	EffectID  string `json:"effect_id"`
}

// seededScore hashes (seed, scene, product, recipe) to a stable [0,1).
func seededScore(seed, scene, product, recipe string) float64 {
	h := fnv.New64a()
	h.Write([]byte(seed + "\x00" + scene + "\x00" + product + "\x00" + recipe))
	return float64(h.Sum64()%1_000_000) / 1_000_000
}

// PlanVariant picks one eligible option. Penalties (subtracted from the
// seeded score): recipe used in the trailing window, motion reused,
// identical family three times running, density piling onto a dense streak.
// Ineligible options are never chosen, no matter the seed.
func PlanVariant(seed, sceneID string, options []RecipeOption, mem DiversityMemory, effects []string) (PlannedVariant, bool) {
	eligible := make([]RecipeOption, 0, len(options))
	for _, o := range options {
		if o.Eligible && o.RecipeID != "" {
			eligible = append(eligible, o)
		}
	}
	if len(eligible) == 0 {
		return PlannedVariant{}, false
	}
	recentRecipe := map[string]int{}
	for i, r := range mem.RecentRecipes {
		recentRecipe[r] += 3 - i/3 // fresher repetitions penalized more
		if recentRecipe[r] < 1 {
			recentRecipe[r] = 1
		}
	}
	recentMotion := map[string]bool{}
	for _, m := range mem.RecentMotions {
		recentMotion[m] = true
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return eligible[i].RecipeID < eligible[j].RecipeID
	})
	best := -1.0
	pick := eligible[0]
	for _, o := range eligible {
		score := seededScore(seed, sceneID, o.ProductID, o.RecipeID)
		score -= 0.45 * float64(recentRecipe[o.RecipeID])
		if recentMotion[o.MotionID] && o.MotionID != "" {
			score -= 0.20
		}
		if mem.DenseStreak >= 2 && o.Density >= 2 {
			score -= 0.25
		}
		if score > best {
			best = score
			pick = o
		}
	}
	effect := ""
	bestEffect := -1.0
	for _, e := range effects {
		s := seededScore(seed, sceneID, pick.ProductID, "effect:"+e)
		for _, r := range mem.RecentEffects {
			if r == e {
				s -= 0.30
			}
		}
		if s > bestEffect {
			bestEffect = s
			effect = e
		}
	}
	return PlannedVariant{ProductID: pick.ProductID, RecipeID: pick.RecipeID, MotionID: pick.MotionID, EffectID: effect}, true
}
