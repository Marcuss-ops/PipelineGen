package overlays

// Package overlays — planner_config.go owns the planner's configuration
// surface: the per-scene editorial limits, the production generation policy
// that derives them from a scene set, and the defaults applied when a caller
// leaves a limit unset. BuildPlan itself lives in planner.go; keeping the
// policy here means a limit change is made in exactly one file, next to the
// documentation that explains why the default is what it is.

import (
	"strings"
)

// PlannerConfig contains the conservative editorial limits for one scene.
// The planner is deterministic: ties preserve the order supplied by the
// caller, while higher scores win within each category.
type PlannerConfig struct {
	MaxPhrases     int
	MaxKeywords    int
	MaxImages      int
	MaxPhraseWords int
	// Extended semantic entity limits (NUMBER / BRAND TEXT / QUOTE / PRODUCT / LOGO).
	MaxNumbers    int
	MaxBrandTexts int
	MaxQuotes     int
	MaxProducts   int
	MaxLogos      int
	// MaxOverlap caps how many content items may overlap at any single
	// moment; beyond it the planner drops the lowest-priority overlapping
	// items (see DegradeOverlaps). Default: DefaultOverlapBudget (3).
	MaxOverlap int
	// RunLevelEditorialBudget applies the production image + grounded-phrase
	// contract and drops other content overlay kinds. Structural background
	// layers are not represented as OverlayItems and are unaffected.
	RunLevelEditorialBudget bool
	// RunLevelPhraseOverlayLimit overrides the run-level grounded-phrase
	// ceiling used by the editorial budget. Zero keeps the certified default
	// (MaxPhraseOverlaysPerRun); positive values are capped by
	// MaxPhraseOverlaysHardLimit.
	RunLevelPhraseOverlayLimit int
	// RunLevelMapOverlayLimit is the per-scene map ceiling used by the
	// map-aware editorial budget. Zero keeps the certified default
	// (MaxMapOverlaysPerScene); a positive value raises it per run.
	RunLevelMapOverlayLimit int
}

// AllCandidatesPlannerConfig is the production generation policy: every
// valid, uniquely identified image or phrase candidate received from the
// certified semantic surfaces is kept in the plan, subject to the run-level
// 5+5 budget. Other content overlay kinds are excluded. Timing validation,
// duplicate removal and the hard image-duration ceiling remain active. This is
// intentionally explicit instead of changing the conservative defaults used
// by the standalone planner/certification tests.
func AllCandidatesPlannerConfig(scenes []SceneInput) PlannerConfig {
	maxPerScene := func(count func(SceneInput) int) int {
		max := 0
		for _, scene := range scenes {
			if n := count(scene); n > max {
				max = n
			}
		}
		return max
	}
	maxPhraseWords := 0
	totalCandidates := 0
	for _, scene := range scenes {
		for _, phrase := range scene.Phrases {
			if n := len(strings.Fields(phrase.Text)); n > maxPhraseWords {
				maxPhraseWords = n
			}
		}
		totalCandidates += len(scene.Phrases) + len(scene.Keywords) + len(scene.Images) +
			len(scene.Numbers) + len(scene.BrandTexts) + len(scene.Quotes) + len(scene.Products) + len(scene.Logos) + len(scene.Maps)
		for _, group := range scene.MultiEntityGroups {
			totalCandidates += len(group.Items)
		}
	}
	// A positive value is required to avoid withDefaults restoring a cap. The
	// extra slot makes the overlap budget strictly larger than every possible
	// planner-owned candidate in this input.
	if totalCandidates < 1 {
		totalCandidates = 1
	}
	if maxPhraseWords < 1 {
		maxPhraseWords = 1
	}
	return PlannerConfig{
		MaxPhrases:              maxPerScene(func(s SceneInput) int { return len(s.Phrases) }),
		MaxKeywords:             maxPerScene(func(s SceneInput) int { return len(s.Keywords) }),
		MaxImages:               maxPerScene(func(s SceneInput) int { return len(s.Images) }),
		MaxPhraseWords:          maxPhraseWords,
		MaxNumbers:              maxPerScene(func(s SceneInput) int { return len(s.Numbers) }),
		MaxBrandTexts:           maxPerScene(func(s SceneInput) int { return len(s.BrandTexts) }),
		MaxQuotes:               maxPerScene(func(s SceneInput) int { return len(s.Quotes) }),
		MaxProducts:             maxPerScene(func(s SceneInput) int { return len(s.Products) }),
		MaxLogos:                maxPerScene(func(s SceneInput) int { return len(s.Logos) }),
		MaxOverlap:              totalCandidates + 1,
		RunLevelEditorialBudget: true,
	}
}

func (c PlannerConfig) withDefaults() PlannerConfig {
	if c.MaxPhrases <= 0 {
		c.MaxPhrases = 1
	}
	if c.MaxKeywords <= 0 {
		c.MaxKeywords = 3
	}
	if c.MaxImages <= 0 {
		c.MaxImages = 1
	}
	if c.MaxPhraseWords <= 0 {
		c.MaxPhraseWords = 20
	}
	if c.MaxNumbers <= 0 {
		c.MaxNumbers = 1
	}
	if c.MaxBrandTexts <= 0 {
		c.MaxBrandTexts = 1
	}
	if c.MaxQuotes <= 0 {
		c.MaxQuotes = 1
	}
	if c.MaxProducts <= 0 {
		c.MaxProducts = 1
	}
	if c.MaxLogos <= 0 {
		c.MaxLogos = 1
	}
	if c.MaxOverlap <= 0 {
		c.MaxOverlap = DefaultOverlapBudget
	}
	return c
}
