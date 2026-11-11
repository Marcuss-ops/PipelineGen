package script

import (
	"strings"

	adapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ValidateRequiredProcessors checks the frozen composition registry for every
// required processor. Composition fails closed if registration is incomplete
// or a required processor was accidentally downgraded to best-effort.
func ValidateRequiredProcessors(registry *adapters.PostProcessorRegistry) *scriptpkg.PlanInvalidError {
	if registry == nil {
		return &scriptpkg.PlanInvalidError{
			ItemID:  "wireScriptFlow",
			Details: []string{"preflight: postprocessor registry is nil"},
		}
	}
	if !registry.IsFrozen() {
		return &scriptpkg.PlanInvalidError{
			ItemID:  "wireScriptFlow",
			Details: []string{"preflight: postprocessor registry must be frozen before required-processors validation"},
		}
	}
	required := adapters.RequiredProcessorNames()
	var missing []string
	for _, name := range required {
		if !registry.Registered(name) {
			missing = append(missing, string(name))
		} else if registry.LookupPolicy(name) != adapters.ProcessorRequired {
			missing = append(missing, string(name)+" (registered with non-required policy)")
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &scriptpkg.PlanInvalidError{
		ItemID:  "wireScriptFlow",
		Details: []string{"preflight: required postprocessor(s) not registered at composition: " + strings.Join(missing, ", ")},
	}
}
