package script

// Preset encodes the endpoint variant for a generation job so the
// worker can apply the correct defaults without the caller repeating
// them in every field.
type Preset string

const (
	// PresetCustom means the caller filled in every flag explicitly.
	PresetCustom Preset = "custom"

	// PresetWithImages means the job came from the /generate-with-images
	// endpoint: scene images and voiceover are forced on, entity
	// extraction and metadata are forced off.
	PresetWithImages Preset = "with_images"
)
