package script

// Preset encodes the endpoint variant for a generation job so the
// worker can apply the correct defaults without the caller repeating
// them in every field.
type Preset string

const (
	// PresetCustom means the caller filled in every flag explicitly.
	PresetCustom Preset = "custom"

	// PresetWithImages means the job requests scene images and voiceover
	// by default while leaving entity extraction and metadata off.
	PresetWithImages Preset = "with_images"

	// PresetFullMedia enables both scene images and voiceover outputs
	// when the caller leaves them at zero. Per §6 "Required preset
	// semantics" of docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md:
	//
	//	full_media | none | images and voiceover enabled explicitly
	//
	// Source effect: none. Output effect: images.enabled=true and
	// voiceover.enabled=true (when caller left them at zero). Caller
	// values always take precedence (caller > preset > config > safety).
	// The preset never alters unrelated fields silently — entities,
	// metadata and document remain caller-controlled; only images and
	// voiceover are flipped on when the caller leaves them at zero.
	// Wires Step 1 of issue 8 (ApplyPreset stub): the constant is
	// defined here; wiring of the per-field override lives in
	// internal/capabilities/scripts/adapters/generation_normalizer.go.
	PresetFullMedia Preset = "full_media"

	// PresetCatalog forces source.kind=catalog so the catalog source
	// resolver is selected without the caller repeating the kind. Per
	// §6 of docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md:
	//
	//	catalog | source.kind=catalog | none
	//
	// Source effect: source.kind=catalog. Output effect: none.
	// The preset never alters unrelated fields silently — voiceover,
	// scene images, entities, metadata remain caller-controlled.
	PresetCatalog Preset = "catalog"

	// PresetSearch forces source.kind=search so automatic clip search
	// is selected without the caller repeating the kind. Per §6 of
	// docs/architecture/godlike/14_UNIFIED_SCRIPT_GENERATION.md:
	//
	//	search | source.kind=search | none
	//
	// Source effect: source.kind=search. Output effect: none.
	// The preset never alters unrelated fields silently — voiceover,
	// scene images, entities, metadata remain caller-controlled.
	PresetSearch Preset = "search"
)
