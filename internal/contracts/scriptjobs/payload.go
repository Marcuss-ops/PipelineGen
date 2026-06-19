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
//
// Supports two formats:
//  1. Versioned envelope (current): {"version":1,"preset":"custom","spec":{...}}
//  2. Legacy flat payload: {"topic":"...","clip_ids":["a"],...}
//
// Returns an error if the payload is invalid JSON, the version is
// unsupported, or both formats produce an empty spec.
func DecodeGeneratePayload(raw json.RawMessage) (*GeneratePayload, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidPayload
	}

	// Try current versioned envelope first.
	var envelope GeneratePayload
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}

	// If the envelope populated a spec or carries an explicit version,
	// it's the new format.
	if envelope.Version > 0 || envelope.Spec.HasText() || envelope.Spec.HasClips() {
		if envelope.Version != 1 {
			return nil, ErrUnsupportedVersion
		}
		return &envelope, nil
	}

	// Legacy flat payload: the entire JSON object is a GenerationSpec.
	var legacy GenerationSpec
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil, err
	}
	if !legacy.HasText() && !legacy.HasClips() {
		return nil, ErrInvalidPayload
	}

	payload := NewGeneratePayload(PresetCustom, legacy)
	return &payload, nil
}
