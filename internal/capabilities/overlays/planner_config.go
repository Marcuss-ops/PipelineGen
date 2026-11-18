package overlays

// Package overlays — planner_config.go keeps the configuration DTO that the
// scripts projection still reads when it derives phrase-lane caps. The Go
// planner that consumed the per-scene limits was demolished on 2026-11-18:
// admission, overlap degradation and run-level budgets are Rust-owned
// (overlay-compiler). Only the fields reachable from the projection survive
// here; do not reintroduce planner policy in this package.

// PlannerConfig contains the conservative editorial limits for one scene.
// Retained for the projection surface; Rust applies these limits during
// editorial plan compilation.
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
	// RunLevelShortPhraseLimit and RunLevelLongPhraseLimit opt into the split
	// phrase lanes used by the editorial budget. Both must be positive to
	// activate: the short lane is admitted first, then the long lane fills
	// around it. A zero value keeps the certified single-budget behavior.
	RunLevelShortPhraseLimit int
	RunLevelLongPhraseLimit  int
	// RunLevelBulletOverlayLimit opts into an independent bullet lane. When
	// positive, chapter bullet cards are admitted on their own quota instead
	// of spending a long-phrase slot: the requested short-phrase ceiling and
	// the long-phrase ceiling are then both fully available to ordinary
	// narration phrases. Zero keeps the legacy shared-lane behavior.
	RunLevelBulletOverlayLimit int
	// RunLevelMapOverlayLimit is the per-scene map ceiling used by the
	// map-aware editorial budget. Zero keeps the certified default
	// (MaxMapOverlaysPerScene); a positive value raises it per run.
	RunLevelMapOverlayLimit int
	// RunLevelImageOverlayLimit is the run-level image ceiling used by the
	// editorial budget. Zero keeps the certified default
	// (MaxImageOverlaysPerRun); a positive value raises it per run so an
	// explicit payload max_image_overlays reaches the render plan instead of
	// being truncated by the default budget.
	RunLevelImageOverlayLimit int
	// DeferAdmission returns the complete candidate set without map trimming,
	// overlap degradation or editorial budgets. The caller applies the
	// BrainRust compile_editorial_plan selection and then calls
	// FinalizeDeferredPlan. It exists only for the single-call planner path;
	// the legacy BuildPlan path keeps admission here.
	DeferAdmission bool
}
