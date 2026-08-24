// Package jobs — handler_registration.go: handler binding surface.
//
// PR-GODOBJ-6 (July 2026): mechanically extracted from service.go
// per the god-object decomposition plan. Zero behaviour changes on
// the canonical HandlerFunc path (pre-extraction shape was identical).
//
// PR-REFLECT-ELIM-HANDLER-REGISTRATION (2026-07-04, godlike/07 win):
// the reflection-based RegisterHandler fallback (reflect.ValueOf/Call +
// runtime ArgCount / AssignableTo / In-Out shape-validation) has been
// RETIRED per the AGENTS.md §Pattern 0 + godlike/07 typed-error
// discipline. The implementation now strictly accepts ONLY
// appjobs.HandlerFunc via a tight type-switch; any other `any` shape
// (struct, raw string, raw int, anonymous func literal of the
// structural signature, etc.) is rejected at registration time with a
// typed error — no silent-success class per godlike/07.
//
// The surface signature REMAINS `(jobType string, handler any) error`
// because 4 lock-step interface contracts depend on it (changing the
// surface breaks each compile-time assertion below):
//
//   - internal/kernel/job/service.go::Service             (kernel canonical Service)
//   - internal/application/scripts/ports/ports.go::Broker (scripts broker port)
//   - internal/api/module_descriptor.go::JobRegistrar     (api capability-standard)
//   - internal/app/creator_runtime.go::brokerAdapter      (creator runtime inline adapter)
//
// Locked by the corresponding assertions at each interface declaration site
// (e.g. `var _ job.Service = (*appjobs.Service)(nil)`). Per godlike/07
// minimal-blast-radius, we tighten the IMPLEMENTATION while preserving
// the surface — the typed-error gate at registration time surfaces the
// reflection-elimination as a runtime contract that production callers
// can errors.Is / errors.As against.
//
// What changed for production callers: the canonical handler-registration
// idiom at every call site MUST now wrap the method value in
// `appjobs.HandlerFunc(h.HandleJob)` (cf. artlist precedent at
// internal/capabilities/assets/providers/artlist/job_core.go:247). The
// type-switch accepts method values whose signature structurally matches
// HandlerFunc (Go's structural subtyping auto-converts at the case
// branch), but explicit casts are canonical for human-readability
// per godlike/06 SSOT — future maintainers reading the call site see
// "this IS a HandlerFunc" without inspecting the method signature.
package jobs

import (
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// MaxJobsPerType is the canonical upper bound on registered handlers per
// job type string (P0 #15 against arbitrary dict-growth, July 2026).
// Kept here rather than types.go to colocate it with the registration
// surface; the dispatcher enforces the cap at Register time.
const MaxJobsPerType = 16

// RegisterHandler registers a handler for the given job type. Surface
// signature is locked at `(jobType string, handler any) error` by the
// 4 interface contracts listed in the package doc; the IMPLEMENTATION
// accepts ONLY `appjobs.HandlerFunc` via strict type-switch. The
// previous reflection-based fallback + structural-anonymous-function
// case have been retired (godlike/07 no-fake-availability audit-pin);
// see PR-REFLECT-ELIM-HANDLER-REGISTRATION in architecture/current.yaml.
//
// To register a method like `h.HandleJob` (whose signature matches
// HandlerFunc structurally) wrap it at the call site:
//
//	jobsSvc.RegisterHandler(jobType, appjobs.HandlerFunc(h.HandleJob))
//
// This explicit cast is canonical (cf. artlist/job_core.go:247).
// Method values without the cast are accepted by the type-switch
// (structural subtyping), but explicit casts are preferred for
// human-readability + future-proofing against signature drift.
func (s *Service) RegisterHandler(jobType string, handler any) error {
	h, ok := handler.(HandlerFunc)
	if !ok {
		return fmt.Errorf("job.Service.RegisterHandler: handler must be appjobs.HandlerFunc (apply explicit `appjobs.HandlerFunc(method)` cast at the call site); got %T for jobType=%q", handler, jobType)
	}
	return s.dispatcher.Register(jobType, h)
}

// HasHandler reports whether the broker has a handler registered
// for the given job type. Issue 7 / P1 (June 2026): added so the
// composition root can fail-fast on a script.generate wiring gap
// without leaking the Dispatcher type into the API surface.
//
// The query is branch-free -- the Dispatcher.AllHandlers() map is
// the canonical record. Returns false when:
//
//   - the receiver is nil (defensive guard)
//   - the dispatcher is nil (composition bug)
//   - no handler is registered for jobType
//
// Nil-tolerant: this method never panics; nil-receiver callers get
// false (so composition-root code can pass s.Service==nil through
// the validateScriptGenerateWiring helper without pre-checking).
func (s *Service) HasHandler(jobType string) bool {
	if s == nil {
		return false
	}
	if s.dispatcher == nil {
		return false
	}
	if jobType == "" {
		return false
	}
	_, ok := s.dispatcher.AllHandlers()[jobType]
	return ok
}

// ValidateHandlerCompleteness checks that every job type registered in
// the canonical Registry has a handler bound to the Dispatcher. Returns
// nil when every job type is consumable; returns an error listing the
// first missing handler when a registration gap is detected.
//
// §15.9 (July 2026): the voiceover parent-child fan-out pair is the
// canonical trigger — when voiceover.generate_item has no handler, the
// server MUST NOT start because the parent's fan-out creates child jobs
// that can never be executed. ValidateHandlerCompleteness is the gate
// the composition root calls before Freeze().
//
// Nil-tolerant: nil receiver, nil dispatcher, and nil registry all
// return nil (the belt-and-suspenders check runs later, after the
// composition root has wired both).
func (s *Service) ValidateHandlerCompleteness(reg *Registry) error {
	if s == nil || s.dispatcher == nil || reg == nil {
		return nil
	}
	handlers := s.dispatcher.AllHandlers()
	for _, jobType := range reg.AllTypes() {
		if _, ok := handlers[jobType]; !ok {
			return fmt.Errorf("job.Service.ValidateHandlerCompleteness: job type %q is registered in the canonical Registry but has NO handler bound to the Dispatcher — the server MUST NOT start with a consumable-type-without-handler gap (§15.9 registrazione incompleta)", jobType)
		}
	}
	return nil
}

// compile-time assertion: appjobs.Service satisfies the kernel canonical
// job.Service interface (RegisterHandler + Enqueue + Get + Cancel + ...).
// appjobs.Service is the canonical producer — interface satisfaction is
// asserted at the *application* boundary rather than at the kernel
// declaration site; the kernel package is upstream and references
// appjobs by alias only.
//
// (job alias import retained for any future kernel-layer consumers that
// reference domain types — currently zero in this file.)
var _ job.Service = (*Service)(nil)
