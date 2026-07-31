package script

import (
	"fmt"
	"strings"
)

// Validate checks structural invariants. Returns a ModelOutputError
// with structured details on failure, or nil when the output is
// structurally valid.
//
// Rules:
//   - SchemaVersion must be 1.
//   - Text must be non-empty (after trimming whitespace).
//   - SpecScene must be valid (specscene version, scenes, indexes).
func (m *ModelScriptOutputV1) Validate() error {
	var details []string

	if m.SchemaVersion != 1 {
		details = append(details,
			fmt.Sprintf("unsupported schema_version %d (expected 1)", m.SchemaVersion))
	}
	if strings.TrimSpace(m.Text) == "" {
		details = append(details, "text is required and must be non-empty")
	}
	if err := m.SpecScene.Validate(); err != nil {
		if me, ok := err.(*ModelOutputError); ok {
			details = append(details, "specscene: "+strings.Join(me.Details, "; "))
		} else {
			details = append(details, "specscene: "+err.Error())
		}
	}

	if len(details) > 0 {
		return &ModelOutputError{Details: details}
	}
	return nil
}
