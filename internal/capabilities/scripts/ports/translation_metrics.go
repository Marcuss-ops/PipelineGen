// Package scripts — ports/translation_metrics.go: RE-EXPORT SHIM for
// the script translation postprocessor warnings recorder.
//
// This file is a thin compatibility layer that re-exports the typed
// port + bounded-reason enum from the leaf sub-package
// `internal/capabilities/scripts/ports/metrics`. The leaf
// sub-package was created to break the import cycle:
//
//	observability → ports → jobs → sqlite/jobs → observability
//
// The bounded enum + port + helper now live ONLY in
// `ports/metrics/metrics.go` (the SOLE canonical owner per
// godlike/06 one-canonical-owner-per-fact). This file uses Go
// type aliases to preserve the canonical import path
// `TranslationWarningReason` /
// `TranslationMetricsRecorder` for the 8 existing call sites
// in:
//
//   - internal/application/scripts/usecase/translation.go
//     (TranslationWarningReason + TranslationMetricsRecorder)
//   - internal/application/scripts/adapters/processor_translation.go
//     (local interface mirrors TranslationMetricsRecorder)
//   - internal/application/scripts/adapters/processor_translation_integration_test.go
//     (compiles against the type)
//
// godlike/06 SSOT (one canonical owner per fact):
//   - TranslationWarningReason enum: defined ONLY in ports/metrics/metrics.go;
//     this file is a re-export shim (type alias), NOT a redefinition.
//   - TranslationMetricsRecorder port: defined ONLY in ports/metrics/metrics.go;
//     this file is a re-export shim (type alias), NOT a redefinition.
//   - noopTranslationMetricsRecorder: defined ONLY here (ports-owned
//     sentinel; metrics package doesn't need to know about the noop
//     contract because the postprocessor's nil-port check fires
//     FIRST).
//
// The noop sentinel and the canonical NewNoopTranslationMetricsRecorder
// constructor LIVE in the ports package (not in metrics/) because the
// noop contract is a ports-owned concern: the postprocessor layer
// imports ports (for the noop fallback), not metrics. metrics/ stays
// a pure leaf (no contracts about nil receivers — that's a
// consumer-side concern).
//
// PR-TRANSLATE-SCRIPT-SPEC forward-pointer FP3 (2026-08-08): this
// re-export shim is the load-bearing piece that keeps the existing
// `TranslationWarningReason` import path working across the
// 8 call sites without forcing a re-import sweep. Go type aliases
// (`type A = B`) are byte-equivalent at compile time — the existing
// compile-time pins + noop receiver methods + interface assertions
// all continue to compile cleanly.
package ports

import (
	metrics "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports/metrics"
)

// TranslationWarningReason is the bounded enum for Prometheus
// label cardinality. Re-export of metrics.TranslationWarningReason
// via Go type alias. Defined ONLY in ports/metrics/metrics.go
// (godlike/06 SSOT).
type TranslationWarningReason = metrics.TranslationWarningReason

// ReasonEqualToSource: translator returned text byte-identical to
// the source segment (non-fatal; may be intentional if source is
// already in the target language). Re-export of
// metrics.ReasonEqualToSource.
const ReasonEqualToSource = metrics.ReasonEqualToSource

// ReasonTranslatorMissing: translator port is nil at composition
// time. Re-export of metrics.ReasonTranslatorMissing.
const ReasonTranslatorMissing = metrics.ReasonTranslatorMissing

// ReasonTargetLangMissing: plan.Languages is empty AND plan.Language
// is empty. Re-export of metrics.ReasonTargetLangMissing.
const ReasonTargetLangMissing = metrics.ReasonTargetLangMissing

// ReasonEmptyTranslation: translator returned whitespace for a
// non-empty source segment. Re-export of metrics.ReasonEmptyTranslation.
const ReasonEmptyTranslation = metrics.ReasonEmptyTranslation

// ReasonIncompleteValidation: post-translation validator rejected
// the rebuilt output. Re-export of metrics.ReasonIncompleteValidation.
const ReasonIncompleteValidation = metrics.ReasonIncompleteValidation

// ReasonClipIDChanged: defensive invariant fired (clip.clip_id
// byte-equality violated). Re-export of metrics.ReasonClipIDChanged.
const ReasonClipIDChanged = metrics.ReasonClipIDChanged

// ReasonDriveLinkChanged: defensive invariant fired (clip.drive_link
// byte-equality violated). Re-export of metrics.ReasonDriveLinkChanged.
const ReasonDriveLinkChanged = metrics.ReasonDriveLinkChanged

// ReasonSourceInvalid: input *ModelScriptOutputV1 is nil or has
// empty Text. Re-export of metrics.ReasonSourceInvalid.
const ReasonSourceInvalid = metrics.ReasonSourceInvalid

// ReasonPreValidationWarn: validator warning on the pre-translation
// enriched baseline. Re-export of metrics.ReasonPreValidationWarn.
const ReasonPreValidationWarn = metrics.ReasonPreValidationWarn

// ReasonUnknown: bounded-enum fallback for godlike/07
// typed-error-contract. Re-export of metrics.ReasonUnknown.
const ReasonUnknown = metrics.ReasonUnknown

// TranslationMetricsRecorder is the canonical SOLE consumer surface
// for the script_translation_warnings_total Prometheus counter.
// Re-export of metrics.TranslationMetricsRecorder via Go type
// alias. Defined ONLY in ports/metrics/metrics.go (godlike/06 SSOT).
type TranslationMetricsRecorder = metrics.TranslationMetricsRecorder

// Compile-time assertion that a nil interface (or nil concrete
// pointer) is the canonical nil-tolerance surface. The contract
// is "nil receiver → silent no-op" so the postprocessor doesn't
// panic on a composition gap.
var _ TranslationMetricsRecorder = (*noopTranslationMetricsRecorder)(nil)

// noopTranslationMetricsRecorder is the typed sentinel for
// composition-gap scenarios where the observability adapter is
// not wired. The zero-value struct satisfies the port (via the
// type alias above) with a silent no-op.
type noopTranslationMetricsRecorder struct{}

// IncTranslationWarning on a nil-receiver noop is a silent no-op.
func (*noopTranslationMetricsRecorder) IncTranslationWarning(string, TranslationWarningReason) {}

// NewNoopTranslationMetricsRecorder returns the canonical nil-tolerance
// fallback for the TranslationMetricsRecorder port. The composition
// root uses this when the observability adapter is unavailable so
// the script.generate pipeline does not abort on a metrics wiring
// gap.
func NewNoopTranslationMetricsRecorder() TranslationMetricsRecorder {
	return &noopTranslationMetricsRecorder{}
}
