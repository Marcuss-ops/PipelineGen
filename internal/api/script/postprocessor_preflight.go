// Package script — postprocessor_preflight.go is the canonical
// SCRIPTCONTRACT-2026-07-08 PR-2 implementation of the godlike/07
// NO-FAKE-AVAILABILITY composition-time preflight for the
// script.generate pipeline.
//
// godlike/06 SSOT:
//   - This file is the SOLE canonical implementation of the
//     preflight predicate for the script pipeline. No other code
//     in this package (or anywhere else) is allowed to compute
//     the same predicate.
//   - The typed-error sentinel + envelope (ErrPreflightProcessorMissing
//   - PreflightProcessorMissingError) live in
//     `internal/domain/script/errors_preflight.go` and are
//     consumed here via `domainScript.ErrPreflightProcessorMissing`.
//   - PreflightCaps is the SOLE canonical flat-deps surface for
//     the preflight input. New deps (for new processor categories)
//     MUST be added here AND in `internal/app/wire_script.go`
//     (canonical wireup surface) AND in
//     `internal/api/script/handler_generate_handler.go`
//     (constructor surface) — 3 surfaces lockstep per the
//     action plan §3.PR-2.
//
// godlike/07 NO-FAKE-AVAILABILITY:
//   - When the user envelope's OutputSpec requests a processor
//     whose composition-time dep is unwired, this function MUST
//     return a typed error. Silent skip is FORBIDDEN in this
//     surface.
//   - The preflight NEVER panics on nil args; it returns a
//     typed error so the HTTP handler can convert to a
//     canonical 503-class response.
//
// godlike/07 minimum-blast-radius:
//   - The preflight is purely additive. The pre-existing
//     silent-skip log.Warn in `registerScriptPostProcessors` is
//     RETAINED for offline diagnostic value (godlike/07 residue
//     accounting); this preflight adds the EXPLICIT-REQUESTED
//     detection layer on top of the existing graceful-degradation
//     path.
//   - PreflightCaps is a flat struct (3 bool fields) — no
//     dependency on *ComposeRoot (which would create a circular
//     import: `internal/app` already imports `internal/api/script`).
//   - The HTTP integration is a single function-call addition
//     in `handler_enqueue.go::enqueueEnvelopeFn` and a 1-arg
//     addition to `NewHandlerGenerate`; no other surface
//     contract changes.
package script

import (
	"errors"
	"fmt"

	"go.uber.org/zap"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// PreflightCaps is the canonical flat-deps surface consumed by
// requireRequestedProcessors. The 3 bool flags map 1:1 to the
// composition-time dep checks:
//
//   - VoiceoverEnabled: true iff `root.Domains.VoiceoverService != nil`
//     at composition time.
//   - ImagesEnabled: true iff `root.Domains.ImageService != nil`
//     at composition time.
//   - DocumentEnabled: true iff `root.Drive.DocClient != nil`
//     at composition time.
//
// godlike/06 SSOT: PreflightCaps is the SOLE canonical input
// shape for the preflight surface. Wireup code (in
// `internal/app/wire_script.go` or the canonical construction
// site) builds this struct from the root at composition time;
// the handler carries it to the request seam.
//
// godlike/07 zero-value semantics: a zero-value PreflightCaps
// (all false) is a CONSERVATIVE default — every user-requested
// processor will fail the preflight. This is intentional:
//   - If the wireup forgets to populate the struct, the request
//     fails closed (the user gets a clear 503, not a silent skip).
//   - A misconfigured deployment without VoiceoverService
//     cannot accidentally accept voiceover requests.
type PreflightCaps struct {
	VoiceoverEnabled bool
	ImagesEnabled    bool
	DocumentEnabled  bool
}

// requireRequestedProcessors walks the per-item OutputSpec for each
// GenerationItemV2 in the envelope and verifies that any explicitly
// requested postprocessor (OutputSpec.GenerateVoiceover /
// OutputSpec.GenerateSceneImages / OutputSpec.GenerateDocument) has
// its composition-time dep wired (per PreflightCaps). Returns nil
// on success; returns the FIRST preflight failure wrapped with
// domainScript.ErrPreflightProcessorMissing + a
// *domainScript.PreflightProcessorMissingError typed envelope on
// failure (per godlike/07 typed-error contract; callers probe via
// errors.Is + errors.As).
//
// The function is purely deterministic — no I/O, no side effects
// beyond the supplied log.Warn diagnostic on failure. Safe to call
// at the HTTP request seam on the hot path.
//
// godlike/06 SSOT: this is the SOLE canonical surface for the
// per-request postprocessor preflight. Called from
// `internal/api/script/handler_enqueue.go::enqueueEnvelopeFn`
// (the shared package-level function used by both HandlerGenerate
// and the legacy 410-Gone adapters; the legacy adapters never
// actually reach enqueueEnvelopeFn but the call signature is
// shared for compile-time consistency).
//
// godlike/07 typed-error contract: every error return path wraps
// `domainScript.ErrPreflightProcessorMissing` (canonical sentinel,
// errors.Is recoverable) AND embeds a
// `*domainScript.PreflightProcessorMissingError` (canonical typed
// envelope, errors.As recoverable) via dual-%w. Go 1.20+ preserves
// BOTH the sentinel and the typed envelope in the error chain;
// the test surface in `postprocessor_preflight_test.go` pins the
// invariants.
func requireRequestedProcessors(caps PreflightCaps, env *domainScript.GenerationEnvelopeV2, log *zap.Logger) error {
	if env == nil {
		return errors.New("script preflight: nil envelope (request bug)")
	}
	for i, item := range env.Items {
		if err := requireRequestedProcessorsOne(caps, item, log, i); err != nil {
			return err
		}
	}
	return nil
}

// requireRequestedProcessorsOne checks a single GenerationItemV2's
// OutputSpec against PreflightCaps. Returns nil on success or
// dual-%w-wrapped typed error on the first preflight failure.
func requireRequestedProcessorsOne(caps PreflightCaps, item domainScript.GenerationItemV2, log *zap.Logger, itemIdx int) error {
	// Voiceover: required composition-time dep = caps.VoiceoverEnabled.
	if item.Output.GenerateVoiceover && !caps.VoiceoverEnabled {
		if log != nil {
			log.Warn("script preflight: voiceover requested but disabled at composition",
				zap.Int("item_index", itemIdx),
				zap.String("item_id", item.ID))
		}
		return fmt.Errorf("%w: %w", domainScript.ErrPreflightProcessorMissing, &domainScript.PreflightProcessorMissingError{
			Processor: "voiceover",
			Reason:    "VoiceoverEnabled=false in PreflightCaps (composition not wired; root.Domains.VoiceoverService is nil)",
		})
	}

	// Scene images: required composition-time dep = caps.ImagesEnabled.
	if item.Output.GenerateSceneImages && !caps.ImagesEnabled {
		if log != nil {
			log.Warn("script preflight: scene images requested but disabled at composition",
				zap.Int("item_index", itemIdx),
				zap.String("item_id", item.ID))
		}
		return fmt.Errorf("%w: %w", domainScript.ErrPreflightProcessorMissing, &domainScript.PreflightProcessorMissingError{
			Processor: "images",
			Reason:    "ImagesEnabled=false in PreflightCaps (composition not wired; root.Domains.ImageService is nil)",
		})
	}

	// Document: required composition-time dep = caps.DocumentEnabled.
	if item.Output.GenerateDocument && !caps.DocumentEnabled {
		if log != nil {
			log.Warn("script preflight: document requested but disabled at composition",
				zap.Int("item_index", itemIdx),
				zap.String("item_id", item.ID))
		}
		return fmt.Errorf("%w: %w", domainScript.ErrPreflightProcessorMissing, &domainScript.PreflightProcessorMissingError{
			Processor: "document",
			Reason:    "DocumentEnabled=false in PreflightCaps (composition not wired; root.Drive.DocClient is nil)",
		})
	}

	return nil
}
