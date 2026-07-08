// Package script — errors_preflight.go is the canonical godlike/06
// SSOT surface for the SCRIPTCONTRACT-2026-07-08 PR-2 typed-error
// contract on the postprocessor preflight.
//
// Pre-PR-2, the script pipeline silently self-skipped postprocessors
// whose composition-time deps were nil (the "graceful degradation"
// pattern). When the user EXPLICITLY requested a processor in the
// envelope (generate_voiceover=true / generate_scene_images=true /
// generate_document=true), the worker reported SUCCEEDED with empty
// artifacts — a godlike/07 NO-FAKE-AVAILABILITY violation.
//
// Post-PR-2, the canonical preflight `requireRequestedProcessors`
// (canonical impl lives at `internal/api/script/postprocessor_preflight.go`)
// runs at the HTTP request seam BEFORE enqueue. When a user-requested
// processor is unwired, the preflight returns
// ErrPreflightProcessorMissing wrapped with a
// PreflightProcessorMissingError typed envelope; the HTTP handler
// converts this to a 503-class response with the typed error mapped
// to the canonical error format.
//
// godlike/06 SSOT:
//   - This file is the SOLE canonical owner of the typed-error
//     contract (sentinel + envelope). No other code in this
//     codebase (production, test, or otherwise) may redefine
//     these symbols or instantiate alternative shapes.
//   - The dual-%w wrap pattern (errors.Is + errors.As both
//     recoverable) is the canonical preflight error contract;
//     callers MUST NOT synthesise alternative strings.
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   - The sentinel is the canonical "fail-closed" surface. No
//     caller is allowed to return this sentinel in a "degraded
//     but works" path; the sentinel is exclusively returned when
//     the user-requested processor is unwired.
//
// godlike/07 minimum-blast-radius:
//   - Zero new dependencies on this file. Pure stdlib errors+fmt.
//   - Zero surface contract changes to other code in this
//     package; the new types are additive.
package script

import (
	"errors"
	"fmt"
)

// ErrPreflightProcessorMissing is the canonical typed sentinel for the
// REQUIRED-PROCESSOR-MISSING failure class. The HTTP handler
// converts this to 503 (or 503-class) at the request seam.
//
// godlike/06 SSOT: this is the SOLE typed sentinel for the
// postprocessor preflight surface. Cross-package callers probe
// via errors.Is (canonical predicate).
var ErrPreflightProcessorMissing = errors.New("script preflight: required postprocessor missing at composition")

// PreflightProcessorMissingError is the canonical typed-data
// envelope for the preflight failure class. errors.As carrier for
// diagnostic metadata. The struct fields are the canonical
// preflight surface — no other code in this package may add fields
// to this type.
//
// godlike/06 SSOT: this is the SOLE typed envelope for the
// postprocessor preflight surface. The struct fields are
// locked (no future drift; new diagnostics live in Reason string
// or a separate scoped type).
type PreflightProcessorMissingError struct {
	// Processor is the canonical processor name. Closed set:
	//   "voiceover" | "images" | "document"
	Processor string
	// Reason is a human-readable diagnostic explaining what
	// composition-time dep is missing. Free-form string for
	// forward-compat with new processor categories; canonical
	// for the 3 closed-set processors the preflight currently
	// checks.
	Reason string
}

// Error implements the error interface. Returns the canonical
// preflight error string with processor + reason details. The
// string format is locked (the preflight test surface in
// `internal/api/script/postprocessor_preflight_test.go` pins the
// prefix "script preflight:" + the processor name in quotes + the
// reason suffix).
func (e *PreflightProcessorMissingError) Error() string {
	if e == nil {
		return "<nil PreflightProcessorMissingError>"
	}
	return fmt.Sprintf("script preflight: processor %q requested but unavailable at composition: %s", e.Processor, e.Reason)
}
