// Package scripts — generation_registration.go carries the canonical
// broker-wiring surface for the script.generate handler (godlike/06
// SSOT: one owner per fact). The handler registers itself with the
// canonical ports.Broker under scriptpkg.TypeScriptGenerate. Worker
// dispatch routes TypeScriptGenerate jobs to this same handler
// regardless of single vs multi mode (the handler internally
// dispatches via HandleSingle / HandleBatch — see
// generation_handler.go).
//
// PR-GODOBJ-4 KILL list applied (per user spec, July 2026):
//   - FAIL-FAST typed NPE: nil receiver or nil broker surface as a
//     composition error, NOT a swallowed no-op per Issue 7 / P1
//     (June 2026). The pre-Issue-7 shape silently allowed nil
//     broker registrations, which produced a runtime "no handler
//     for script.generate" error the first time a script.generate
//     job was enqueued. The new contract: composition root must
//     wire a real broker; nil-broker tests use
//     TestRegisterJobs_FailsWhenBrokerMissing (stub test).
//   - NO filesystem ops in this file (KILL K1).
//   - NO dispatch logic (split: this file = wiring, generation_handler.go
//     = dispatch; single + batch separation enforced by HandleSingle
//     and HandleBatch in generation_handler.go).
//
// godlike/07 typed-error contract: both NPE cases return a typed
// composition error with `fmt.Errorf` wrapping the canonical
// "generate job handler: ..." prefix so log scanners + dashboards
// can route on the prefix.
package jobs

import (
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"

	jobscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// RegisterJobs binds h.Handle to the canonical ports.Broker under
// scriptpkg.TypeScriptGenerate. Returns nil on success; returns a
// typed composition error on:
//   - nil receiver
//   - nil broker (composition-root misconfiguration)
//   - broker.RegisterHandler error
func (h *GenerateJobHandler) RegisterJobs(jobsSvc ports.Broker) error {
	if h == nil {
		return fmt.Errorf("generate job handler: not constructed")
	}
	if jobsSvc == nil {
		return fmt.Errorf("generate job handler: jobs broker is required")
	}
	if err := jobsSvc.RegisterHandler(jobscript.TypeGenerate, appjobs.HandlerFunc(h.Handle)); err != nil {
		return fmt.Errorf("generate job handler: register: %w", err)
	}
	if h.log != nil {
		h.log.Info("registered script.generate job handler",
			zap.String("job_type", string(jobscript.TypeGenerate)),
		)
	}
	return nil
}
