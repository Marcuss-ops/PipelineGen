// Package app — critical_handler_validator.go (Audit P0 #2 continuation, July 2026).
//
// ValidateCriticalHandlers aggregates the registration of all critical
// job handlers at the composition root BEFORE the HTTP server boots and
// the worker pool starts (godlike/05 fail-closed posture). The previous
// audit-P0.2 commit converted the voiceover handlers to error-return,
// but the OTHER 7 critical handlers (stockpipeline, catalogsync,
// image, clipindexer, youtube, etc.) still log-and-continue on
// registration failure. P0 #2 cont. consolidates the post-call surface
// check into a single canonical validator.
//
// Why this exists: godlike/07 ZERO LEGACY + no-fake-availability —
// the dispatcher must NEVER reach the worker pool with un-wired
// handlers (silent-success → jobs queue → no consumer → dead letter).
// The validator is the canonical place to assert that EVERY named
// handler bound successfully; if any bind fails, NewComposition
// returns non-nil and the server boot aborts.
//
// Why the slice-of-closures shape (instead of pulling wiring.ComposeRoot or
// domain services directly into the validator): the validator is
// decoupled from the composition-tree shape — the caller decides
// what handlers to register. This makes the validator trivially
// testable (closure mocks) and future-proof against composition
// refactors (the validator's surface stays stable even if
// BuildDomainBundle is split or merged).
package wiring

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
)

// CriticalHandler is the canonical bind-spec for the critical-handler
// validator. Name is the audit-pinned identifier (appears in error
// messages + log lines + operator dashboards); Bind is the closure
// that performs the actual jobs.Service.RegisterHandler call.
//
// The Bind closure MUST be nil-safe: if the handler construction path
// is unwired (e.g. destinations nil for an artlist-only deploy), the
// closure should return nil without binding. The validator only
// surfaces FAILURES, not unwired-but-optional paths.
//
// Pre-fill Naming convention: <capability>.<job_type> (e.g.
// "voiceover.generate", "stockpipeline.media_stock",
// "images.image_generate_google").
type CriticalHandler struct {
	Name string
	Bind func(svc *appjobs.Service) error
}

// ValidateCriticalHandlers iterates the provided CriticalHandlers
// slice and accumulates each Bind failure with errors.Join. Returns
// nil when all Binds succeed; returns a wrapped errors.Join when
// ANY Bind fails (fail-closed at boot).
//
// **Design shape — LITERAL Register re-call (PR-VALIDATOR-LITERAL-REGISTER, July 2026).**
// The user spec for audit-P0.2-continuation literally asked the
// validator to "chiama Register(svc)" for each critical handler. The
// v2 (PR-VALIDATOR-LITERAL-REGISTER) validator implements EXACTLY that:
// each Bind closure re-invokes the corresponding handler.Register(svc)
// method verbatim. Two effects take place at composition time:
//
//  1. **Bind surface normalization** — the inline late-bindings block
//     in NewComposition ALSO handles each binding (fail-closed via
//     wrapped composition errors). The validator's Bind closure
//     duplicates the call, giving the dispatcher's Register a second
//     invocation. The dispatcher idempotently OVERWRITES on duplicate
//     registers (or rejects — in which case the validator surfaces
//     the typed error and aborts).
//
//  2. **Silent-success class closure** — the v1 (pre-PR-VALIDATOR-
//     LITERAL-REGISTER) shape was HasHandler post-bind confirmation.
//     v1 could only catch silent-Warn failures; v2 catches every
//     class: nil-dispatcher rejection, duplicate-bind rejection
//     (where HasHandler would still return true), port-signature drift,
//     runtime cancel mid-Register.
//
// The two remaining exceptions to literal-Register re-call (auditable
// in CHANGELOG.md audit-P0.2-cont honest-limitations #5 and #6):
//
//   - **voiceover.generate** — BLOC5.3 + Catena A P0 idempotency
//     contract: the late-bindings HasHandler-gated Register decision
//     is replicated in the validator's Bind (`if svc.HasHandler(...) {
//     return nil } return vh.Register(svc)`). The dispatcher binding
//     chosen by the late-bindings block is preserved verbatim.
//   - **stockpipeline.media_stock** — bound AFTER NewComposition
//     returns (via registerInternalModules::WireStockPipeline). The
//     canonical stockpipeline validator pass lives in lifecycle.go
//     (post-WireStockPipeline + pre-ListenAndServe), NOT here.
//
// Contract (single source of truth for this validator):
//
//   - svc == nil → typed error (composition-root wiring bug;
//     the validator cannot run without a wired jobs.Service).
//   - log == nil → swaps in zap.NewNop() so the validator remains
//     usable from composition roots without explicit logger wiring.
//   - handlers slice is the canonical authoritative list — a
//     missing handler is a composition-shape bug, NOT a failure
//     to log. Empty handlers slice → nil error (no bindings to
//     validate).
//   - Each Bind failure is wrapped with the handler Name (`%s: %w`)
//     AND appended to the errs slice; the final aggregate is
//     `errors.Join(errs...)` wrapped in a typed prefix indicating
//     N binding failure(s).
//
// godlike/05 fail-fast posture: any non-nil error from this
// function MUST abort NewComposition so the caller never sees
// `(*wiring.ComposeRoot, nil)` with a half-registered dispatcher.
//
// godlike/07 no-fake-availability: validators do NOT fabricate a
// "soft success" on binding failure. A nil Bind closure that
// silently skips is a deliberate composition-time choice (yes-yes
// skip in some deploys), NOT the same as a binding-attempted-
// failed scenario which MUST surface as an error.
//
// **Dispatcher idempotency contract (v2 load-bearing assumption).**
// The v2 literal-Register shape RE-INVOKES each handler.Register(svc)
// call after the inline late-bindings block above has already bound
// to the same job type. For this to succeed at boot without aborting,
// `*appjobs.Service.dispatcher.Register(type, h)` MUST be idempotent —
// silently overwrite on duplicate-bind OR accept duplicates without
// error. Regression test `TestValidateCriticalHandlers_BindOnAlreadyRegisteredIsIdempotent`
// in critical_handler_validator_test.go pins this contract; a future
// dispatcher refactor that adds STRICT duplicate-rejection MUST be
// flagged as breaking-change to this contract (the validator would
// fail closed at every prod boot).
func ValidateCriticalHandlers(svc *appjobs.Service, log *zap.Logger, handlers []CriticalHandler) error {
	if svc == nil {
		return fmt.Errorf("ValidateCriticalHandlers: nil jobs.Service (composition-root wiring bug — aborting critical-handler validation)")
	}
	if log == nil {
		log = zap.NewNop()
	}

	var errs []error
	bound := 0
	skipped := 0
	for _, h := range handlers {
		if h.Bind == nil {
			skipped++
			continue
		}
		if err := h.Bind(svc); err != nil {
			log.Error("critical handler binding failed (audit-P0.2 cont.)",
				zap.String("handler", h.Name),
				zap.Int("binding_failures_so_far", len(errs)+1),
				zap.Error(err))
			errs = append(errs, fmt.Errorf("%s: %w", h.Name, err))
			continue
		}
		bound++
		log.Debug("critical handler bound",
			zap.String("handler", h.Name))
	}

	log.Info("validate critical handlers (audit-P0.2 cont.)",
		zap.Int("bound", bound),
		zap.Int("skipped", skipped),
		zap.Int("failed", len(errs)))

	if len(errs) > 0 {
		return fmt.Errorf("validate critical handlers (audit-P0.2 cont.): %d binding failure(s): %w",
			len(errs), errors.Join(errs...))
	}
	return nil
}
