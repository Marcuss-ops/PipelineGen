// Package overlays owns deterministic run-level admission of compiled
// overlay items. It enforces timeline-plan editorial budgets before render
// jobs are dispatched.
package overlays

// MaxImageOverlaysPerRun is the certified default run-level ceiling for image
// overlays. An explicit payload image limit may raise it; motion selection
// remains deterministic and may reuse catalog motions.
const MaxImageOverlaysPerRun = 20

// MaxEntityImageOverlaysPerRun is retained as the entity-image-specific name
// used by existing callers; its value is the common image overlay ceiling.
const MaxEntityImageOverlaysPerRun = MaxImageOverlaysPerRun

// MaxPhraseOverlaysPerRun is the default run-level ceiling for grounded
// phrase overlays. Phrase candidates are deduplicated across scenes, ranked
// by certified semantic score, then admitted to the render plan.
const MaxPhraseOverlaysPerRun = 5

// MaxPhraseOverlaysHardLimit is the absolute phrase-overlay ceiling for one
// run, even when a payload asks for more. Fifty is the acceptance ceiling
// (25 short + 25 long, the golden throughput mix); the default editorial
// selection remains five phrases.
const MaxPhraseOverlaysHardLimit = 50

// EffectivePhraseOverlayLimit resolves the effective run-level phrase ceiling.
// A positive caller limit may lower or raise the default, but never exceeds the
// hard maximum; zero or a negative value keeps the certified default. Keeping
// the policy here ensures every planner uses the same limit.
func EffectivePhraseOverlayLimit(requested int) int {
	if requested <= 0 {
		return MaxPhraseOverlaysPerRun
	}
	if requested > MaxPhraseOverlaysHardLimit {
		return MaxPhraseOverlaysHardLimit
	}
	return requested
}

// MaxMapOverlaysPerScene is the hard per-scene ceiling for map overlays. A
// scene rarely narrates more than a couple of places; the cap stops a wide
// plate manifest from turning every grounded mention into a rendered map.
const MaxMapOverlaysPerScene = 2

// MaxMapOverlaysPerRun bounds the number of grounded map overlays admitted to
// one run. Each map must still have a grounded location and a ready plate.
// Ten matches the golden throughput mix; smaller explicit requests are
// honored exactly (see effectiveMapOverlayCeiling).
const MaxMapOverlaysPerRun = 10

// MaxNumberOverlaysPerRun bounds value callouts independently so metrics,
// money and dates can survive the editorial budget without displacing the
// established image, phrase or map allowances. Ten matches the golden
// throughput mix of documented dates and metrics.
const MaxNumberOverlaysPerRun = 10

// MaxBrandTextOverlaysPerRun independently bounds grounded brand-name cards
// used when no verified logo asset is available.
const MaxBrandTextOverlaysPerRun = 5

// PhraseOverlayBudget reports the requested editorial phrase ceiling and how
// many unique grounded phrase overlays were actually materialized.
type PhraseOverlayBudget struct {
	Requested    int `json:"requested_phrase_overlays"`
	Materialized int `json:"materialized_phrase_overlays"`
	Shortfall    int `json:"phrase_overlay_shortfall"`
}
