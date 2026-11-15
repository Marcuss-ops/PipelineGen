package script

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateRejectsFinalJobWithDisabledRender(t *testing.T) {
	env := &GenerationEnvelopeV2{
		Version:  2,
		FinalJob: true,
		Items: []GenerationItemV2{{
			ID:     "item-1",
			Source: SourceSpec{Type: "text", Topic: "topic"},
		}},
	}
	err := env.Validate()
	var invalid *PlanInvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("Validate error = %v, want PlanInvalidError", err)
	}
	if !strings.Contains(strings.Join(invalid.Details, " "), "output.render.enabled must be true") {
		t.Fatalf("details = %v, want field-level toggle diagnostic", invalid.Details)
	}
}
