package scriptjobs

import "encoding/json"

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

// GeneratePayload is the versioned envelope stored in the job queue.
// The worker unmarshals this directly from job.Payload.
type GeneratePayload struct {
	// Version is the payload schema version (currently 1).
	Version int `json:"version"`

	// Preset records which endpoint produced this job.
	Preset Preset `json:"preset"`

	// Spec holds the canonical generation parameters.
	Spec GenerationSpec `json:"spec"`
}

// NewGeneratePayload creates a GeneratePayload with version 1 and the
// supplied preset and spec.
func NewGeneratePayload(preset Preset, spec GenerationSpec) GeneratePayload {
	return GeneratePayload{
		Version: 1,
		Preset:  preset,
		Spec:    spec,
	}
}

// DecodeGeneratePayload unmarshals a raw job payload into GeneratePayload.
// Returns an error if the payload is invalid JSON or the version is
// unsupported.
func DecodeGeneratePayload(raw json.RawMessage) (*GeneratePayload, error) {
	var p GeneratePayload
	if len(raw) == 0 {
		return &p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.Version != 0 && p.Version != 1 {
		return nil, ErrUnsupportedVersion
	}
	return &p, nil
}
