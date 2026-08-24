// Package app — build_voiceover_validators.go
// Critical-handler validators for the voiceover job types
// (voiceover.generate + voiceover.generate_item). Extracted from
// build_bundles_voiceover.go as part of the July 2026 domain split:
// tts / destinations / jobs / validators.
package wiring

import (

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	domainvoiceover "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
)

// appendVoiceoverCriticalValidators populates the critical-handler
// validators slice with voiceover.generate + voiceover.generate_item bindings.
// Extracted from NewComposition per PG-028 (July 2026).
func appendVoiceoverCriticalValidators(domains *DomainBundle, jobs *JobsBundle, validators *[]CriticalHandler) {
	// voiceover.generate: literal Register re-call gated by
	// HasHandler check to preserve BLOC5.3 + Catena A P0 idempotency
	// (parent gate at late-bindings time). If the dispatcher already
	// holds a Catena A P0 binding, the validator no-ops so we don't
	// overwrite it with the BLOC5.3 caller-reference handler.
	if jobs.Service != nil {
		vh := domains.VoiceoverGenerateHandler
		if vh != nil {
			*validators = append(*validators,
				CriticalHandler{
					Name: "voiceover.generate",
					Bind: func(svc *appjobs.Service) error {
						if svc.HasHandler(domainvoiceover.TypeGenerate) {
							return nil // idempotent: Catena A P0 bind preserved
						}
						return vh.Register(svc)
					},
				},
			)
		}
	}
	if gih := domains.VoiceoverGenerateItemHandler; gih != nil && jobs.Service != nil {
		*validators = append(*validators,
			CriticalHandler{
				Name: "voiceover.generate_item",
				Bind: func(svc *appjobs.Service) error {
					return gih.Register(svc)
				},
			},
		)
	}
}
