package generation

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Request is the persisted API payload for a generation job.
type Request struct {
	SchemaVersion int             `json:"schema_version"`
	Type          Type            `json:"type"`
	Input         json.RawMessage `json:"input"`
	Options       map[string]any  `json:"options,omitempty"`
}

// Validate checks the minimal canonical fields.
func (r Request) Validate() error {
	if strings.TrimSpace(r.Type.String()) == "" {
		return fmt.Errorf("type is required")
	}
	if len(r.Input) == 0 {
		return fmt.Errorf("input is required")
	}
	if r.SchemaVersion < 0 {
		return fmt.Errorf("schema_version must be >= 0")
	}
	return nil
}
