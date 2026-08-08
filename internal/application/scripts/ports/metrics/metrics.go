// Package metrics — ports/metrics/metrics.go: canonical SSOT for the
// script translation postprocessor warning bounded-reason enum and
// the metrics recorder port.
//
// This is a TRULY-LEAF Go sub-package (zero upward imports) created
// to break the import cycle: observability → ports → jobs →
// sqlite/jobs → observability. The bounded enum + port + helper
// live here (no transitive deps), so observability can import this
// sub-package without creating a cycle.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - TranslationWarningReason enum: declared ONLY in this file.
//   - TranslationMetricsRecorder port interface: declared ONLY in
//     this file.
//   - NormalizeTargetLang helper: declared ONLY in this file.
//
// Re-export shim (ports/translation_metrics.go) uses Go type aliases
// to preserve the canonical path `ports.TranslationWarningReason` /
// `ports.TranslationMetricsRecorder` for the 8 existing call sites
// in usecase/translation.go + adapters/processor_translation.go +
// adapters/processor_translation_integration_test.go, so the cycle
// stays broken without rewriting all the import paths.
//
// godlike/07 NO-FAKE-AVAILABILITY: any value that does not match one
// of the canonical bounded values is coerced to ReasonUnknown at the
// metrics-adapter boundary (defense-in-depth — the typed port
// signature already prevents out-of-bounds values at compile time).
//
// PR-TRANSLATE-SCRIPT-SPEC forward-pointer FP3 (2026-08-08): this
// sub-package is the cycle-breaker. Originally the enum + port lived
// in `ports/translation_metrics.go` (the parent `ports` package),
// but that creates an import cycle when observability imports
// `ports` for the typed reason. Extracting to a leaf sub-package
// with zero upward imports breaks the cycle.
package metrics

import "strings"

// ExtractCountryForTelemetry maps a BCP-47 tag to the bounded country label
// used by script-generation branch telemetry. It is the application policy
// consumed by infrastructure metric adapters.
func ExtractCountryForTelemetry(bcp47 string) string {
	bcp47 = strings.TrimSpace(bcp47)
	if bcp47 == "" {
		return "XX"
	}
	parts := strings.Split(bcp47, "-")
	if len(parts) >= 2 && parts[1] != "" {
		return strings.ToUpper(parts[1])
	}
	return strings.ToUpper(parts[0])
}

// TranslationWarningReason is the bounded enum for Prometheus
// label cardinality (PR-TRANSLATE-SCRIPT-SPEC FP3, 2026-08-08). The
// TranslationProcessor emits one bounded reason per warning + per
// non-fatal typed error. Dashboards MUST groupBy(reason) before
// groupingBy(target_lang) to surface real failure classes — the
// bounded enum prevents label-cardinality explosion.
//
// godlike/06 SSOT (one canonical owner per fact): this enum lives
// ONLY in this file. The usecase package's `ClassifyReason` function
// returns this type; the metrics adapter's `IncTranslationWarning`
// accepts this type. Adding a new reason requires: (1) add a
// constant here, (2) update the usecase's ClassifyReason switch,
// (3) update any operator dashboard extractor that
// groupBy(reason).
type TranslationWarningReason string

const (
	// ReasonEqualToSource: translator returned text byte-identical
	// to the source segment (non-fatal; may be intentional if
	// source is already in the target language).
	ReasonEqualToSource TranslationWarningReason = "equal_to_source"

	// ReasonTranslatorMissing: translator port is nil at composition
	// time; the processor skipped translation with a warning.
	ReasonTranslatorMissing TranslationWarningReason = "translator_missing"

	// ReasonTargetLangMissing: plan.Languages is empty AND
	// plan.Language is empty; nothing to translate.
	ReasonTargetLangMissing TranslationWarningReason = "target_lang_missing"

	// ReasonEmptyTranslation: translator returned whitespace for a
	// non-empty source segment; function returned ErrTranslationEmpty.
	ReasonEmptyTranslation TranslationWarningReason = "empty_translation"

	// ReasonIncompleteValidation: post-translation
	// ValidateAndEnrichSpecScene (re)gate rejected the rebuilt
	// output; function returned ErrTranslationIncomplete.
	ReasonIncompleteValidation TranslationWarningReason = "incomplete_validation"

	// ReasonClipIDChanged: defensive invariant fired: clip.clip_id
	// byte-equality with enriched baseline violated; should never
	// fire under per-text strategy.
	ReasonClipIDChanged TranslationWarningReason = "clip_id_changed"

	// ReasonDriveLinkChanged: defensive invariant fired:
	// clip.drive_link byte-equality with enriched baseline
	// violated; should never fire.
	ReasonDriveLinkChanged TranslationWarningReason = "drive_link_changed"

	// ReasonSourceInvalid: input *ModelScriptOutputV1 is nil or has
	// empty Text; function returned ErrTranslationSourceInvalid.
	ReasonSourceInvalid TranslationWarningReason = "source_invalid"

	// ReasonPreValidationWarn: ValidateAndEnrichSpecScene emitted a
	// validator warning on the pre-translation enriched baseline.
	ReasonPreValidationWarn TranslationWarningReason = "pre_validation_warning"

	// ReasonUnknown: bounded-enum fallback for godlike/07
	// typed-error-contract: every translation call that emits an
	// unknown warning has its reason coerced to ReasonUnknown at
	// the metrics-adapter boundary.
	ReasonUnknown TranslationWarningReason = "unknown"
)

// ScriptGenerationBranchRecorder records the selected script-generation branch.
type ScriptGenerationBranchRecorder interface {
	RecordScriptGenerationBranch(branch, bcp47 string)
}

// TranslationMetricsRecorder is the canonical SOLE consumer surface
// for the script_translation_warnings_total Prometheus counter.
// The observability adapter (internal/infrastructure/observability/metrics_translation.go)
// is the SOLE canonical producer; any other writer is a
// godlike/06 SSOT regression.
//
// targetLang is the resolved plan language (free-form string, e.g.
// "it", "en-US", "pt-BR"). The metrics adapter MUST normalize
// high-cardinality language strings to a bounded set per
// godlike/07 (see NormalizeTargetLang in this file).
//
// reason is the BOUNDED TranslationWarningReason enum (this file,
// SOLE canonical owner). The metrics adapter MUST coerce empty
// values to ReasonUnknown per godlike/07 typed-error contract.
type TranslationMetricsRecorder interface {
	IncTranslationWarning(targetLang string, reason TranslationWarningReason)
}

// VidRushMetrics records bounded per-segment pipeline events. Dynamic
// identifiers belong in structured logs, not metric labels.
type VidRushMetrics interface {
	IncSegments()
	IncExtractionCache(hit bool)
	IncAssetCache(provider string, hit bool)
	IncProviderRequest(provider string)
	IncProviderFailure(provider string)
	IncBinding()
	IncUnresolvedSegment()
}

// NormalizeTargetLang applies the canonical godlike/07
// NO-FAKE-AVAILABILITY cardinality guard to a target language
// string before it reaches the Prometheus target_lang label. The
// guard:
//
//  1. Empty/whitespace → "unknown" (composition-gap diagnostic).
//  2. Any other string → preserved verbatim (operator visibility
//     for off-list language codes).
//
// godlike/06 SSOT (one canonical owner per fact): this function is
// the SOLE canonical source of the target_lang label normalization
// for the translation postprocessor. Future agents that add new
// language-code canonicalization MUST extend this function (NOT
// inline the logic in the postprocessor or the metrics adapter).
//
// godlike/07 typed-error contract: the returned string is ALWAYS
// non-empty (the "unknown" fallback guarantees bounded cardinality
// even when the caller passes an empty plan language).
func NormalizeTargetLang(lang string) string {
	if strings.TrimSpace(lang) == "" {
		return "unknown"
	}
	return lang
}
