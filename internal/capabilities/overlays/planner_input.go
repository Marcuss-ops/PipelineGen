// planner_input.go — planner input types, split out of planner.go (600-line file gate).
package overlays

// are the integer-microsecond canonical timing (authoritative); StartMs/EndMs
// are their millisecond projection.
type TimedAnnotation struct {
	Text       string
	Type       string
	StartMs    int64
	EndMs      int64
	StartUS    int64
	DurationUS int64
	Score      float64
	// PresentationRole is an internal producer hint for grounded editorial
	// title/bullet placement; it does not create a new wire kind.
	PresentationRole string
}

type ImageCandidate struct {
	AssetID    string
	URL        string
	LocalPath  string
	SHA256     string
	MediaType  string
	StartMs    int64
	EndMs      int64
	StartUS    int64
	DurationUS int64
	Score      float64
}

// MaxImageOverlayDurationMS is the hard editorial ceiling for every image,
// product and logo overlay.
const MaxImageOverlayDurationMS int64 = 5_000

type SceneInput struct {
	ID       string
	Phrases  []TimedAnnotation
	Keywords []TimedAnnotation
	Images   []ImageCandidate
	// Extended semantic entity annotations: numbers (stat highlights),
	// quotes, product images and logos. They terminate in the same
	// canonical primitives as the base set (Text / Image).
	Numbers []TimedAnnotation
	// ChartNumbers is the subset explicitly authorized by the per-scene Rust
	// chart extractor. Nil preserves legacy planner behavior; an empty non-nil
	// slice explicitly disables charts for a scene.
	ChartNumbers []TimedAnnotation
	BrandTexts   []TimedAnnotation
	Quotes       []TimedAnnotation
	Products     []ImageCandidate
	Logos        []ImageCandidate
	// Maps carries the scene's grounded place candidates. Each one must be
	// covered by a certified plate from PlanInput.PlateResolver before it can
	// become a map item; anything uncovered is silently skipped (never
	// guessed at).
	Maps []MapCandidate
	// MultiEntityGroups are explicitly authored adjacent image/phrase groups.
	// They are lowered to the existing image_layers and important_phrase
	// primitives; they do not create new renderer kinds.
	MultiEntityGroups []MultiEntityGroup
}

// PlanInput is the minimal upstream projection required by the overlay
// planner. It deliberately does not import the script domain package.
type PlanInput struct {
	PlanID    string
	VideoID   string
	ProjectID string
	// MotionSeed changes phrase-motion selection between newly compiled plans.
	// Empty preserves the historical deterministic PlanID/VideoID behavior.
	MotionSeed string
	// CoherenceSeed selects the run's dominant horizontal direction. Empty
	// derives the direction from MotionSeed (or PlanID when that is empty).
	CoherenceSeed string
	Width         int
	Height        int
	FPSNum        int
	FPSDen        int
	// RendererVersion is intentionally optional. PipelineGen emits semantic
	// overlay instructions; RenderingGen owns the concrete renderer selection.
	// Callers should leave this empty unless the queue contract explicitly
	// requires a renderer capability/version constraint.
	RendererVersion string
	// Background is copied verbatim into the sealed overlay plan when the
	// script/render payload explicitly requests one.
	Background *OverlayBackground
	// PhraseMotions optionally REPLACES the certified phrase-motion rotation
	// pool for this run (the animated IMPORTANT_PHRASE overlays). It carries a
	// channel profile's motion choice — PipelineGen resolves it at request
	// build; the planner only rotates within it, deterministically, exactly as
	// it does with the certified default pool. Empty keeps the default pool,
	// and an id outside CertifiedPhraseMotions() is a compile failure: the
	// rotation must never hand the renderer a motion it cannot run.
	PhraseMotions []string
	// PhraseMotionFamily optionally limits automatic phrase-motion selection
	// to a certified family and applies the one sampled motion to every phrase.
	PhraseMotionFamily string
	// HeavyPhrasePriority, when POSITIVE, splits the phrase lane by editorial
	// weight: a phrase whose priority (the score the candidate was admitted
	// with) is at least this value is HEAVY and takes a distinct, prominent
	// certified entrance, while the remaining phrases keep the calm default
	// rotation. Zero (the default) disables the split and reproduces the
	// previous single-rotation plan bit for bit, which is what every existing
	// caller and contract test relies on.
	HeavyPhrasePriority float64
	// ImageMotions optionally narrows the certified layer-only image motion pool.
	ImageMotions []string
	// AnimationCounts caps the distinct style pool for each runtime subfamily.
	// Missing or non-positive entries keep the FULL certified catalog so runs
	// rotate every entrance; a positive value bounds the window. Chain tuning
	// rides here too: chain_gap_ms, chain_max_segments, chain_disabled.
	// Lane keys are canonicalized by animationLaneKeys (preset_motion_selection.go):
	// images, images_with_text, entities, important_phrase, short_important_phrase,
	// maps, timeline_date, metric_stat — each with documented aliases resolved
	// in one place, so every reader of a lane sees the same limit.
	AnimationCounts map[string]int
	// EntityStyleID pins the entity-card composition family for generated
	// entity cards (a channel profile's or job's choice): "random" samples
	// the full 25 Apple Spatial registry, tag selectors (badge, camera,
	// side, typewriter, testo_sotto) narrow it, and a 01..25 variant pins
	// one composition. Empty keeps the certified "random" default. Invalid
	// selectors fail closed in BuildPlan.
	EntityStyleID string
	// PlateResolver resolves a grounded WGS84 point to the certified basemap
	// plate covering it. Nil (or an uncovered point) means the scene emits no
	// map: the planner never fabricates geography.
	PlateResolver PlateResolver
	Scenes        []SceneInput
	// Recap carries recap slots bound to placement scenes (see recap_plan.go).
	// Nil/empty disables recaps; entries that cannot contribute are skipped.
	// Recap items join the plan before overlap degradation and validation,
	// so collision and timing rules apply to them like any other item.
	Recap []RecapPlacement
	// Charts carries bar-chart slots derived from grounded scene numbers
	// (see chart_plan.go). Nil/empty disables charts; entries that cannot
	// contribute are skipped. Chart items join the plan before validation
	// (after the emission budget gate below, since SHAPE bypasses
	// DegradeOverlaps as structural).
	Charts []ChartPlacement
}
