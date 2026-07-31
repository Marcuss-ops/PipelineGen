package script

import (
	"fmt"
	"strings"
)

// ErrModelOutputMalformed is the sentinel for any model-output
// decode or validation failure (malformed JSON, missing fields,
// unsupported schema version).
var ErrModelOutputMalformed = fmt.Errorf("script: model output malformed")

// ModelOutputError carries the structured details behind
// ErrModelOutputMalformed.
type ModelOutputError struct {
	Details []string
}

func (e *ModelOutputError) Error() string {
	if e == nil || len(e.Details) == 0 {
		return ErrModelOutputMalformed.Error()
	}
	return fmt.Sprintf("%s: %s", ErrModelOutputMalformed.Error(), strings.Join(e.Details, "; "))
}

func (e *ModelOutputError) Unwrap() error { return ErrModelOutputMalformed }
