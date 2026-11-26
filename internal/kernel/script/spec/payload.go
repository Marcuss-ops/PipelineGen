package spec

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

	// PresetBatch is a LEGACY alias of "no preset". Batch generation is
	// expressed by a multi-item envelope, not by a defaulting variant, so the
	// name routes to the pass-through row and is accepted only so payloads
	// written against the old table keep working.
	PresetBatch Preset = "batch"
)

// IsKnown reports whether the preset names a row this build implements.
//
// An empty preset is accepted: it is the "caller filled everything" shape and
// routes to the pass-through row, exactly like custom. Anything else — most
// notably the `"news"` value 61 real job payloads carried while the runtime
// silently did nothing — is a contract error. Accepting an unknown preset
// meant a payload could declare an editorial variant that never existed.
func (p Preset) IsKnown() bool {
	switch p {
	case "", PresetCustom, PresetWithImages, PresetFullMedia, PresetCatalog, PresetSearch, PresetBatch:
		return true
	}
	return false
}

// CanonicalPresets lists every accepted preset spelling for error messages.
func CanonicalPresets() []string {
	return []string{
		string(PresetCustom),
		string(PresetWithImages),
		string(PresetFullMedia),
		string(PresetCatalog),
		string(PresetSearch),
		string(PresetBatch),
	}
}
