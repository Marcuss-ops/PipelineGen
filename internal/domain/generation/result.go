package generation

import "encoding/json"

// Artifact is a portable description of a produced file or object.
type Artifact struct {
	Type string `json:"type"`
	URI  string `json:"uri"`
	Name string `json:"name,omitempty"`
}

// Result is the common output envelope for generation jobs.
type Result struct {
	PrimaryArtifact *Artifact       `json:"primary_artifact,omitempty"`
	Artifacts       []Artifact      `json:"artifacts,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
	Metadata        map[string]any  `json:"metadata,omitempty"`
}
