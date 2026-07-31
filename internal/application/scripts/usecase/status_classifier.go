// Package usecase — status_classifier.go is the canonical status
// classifier for the script.generate orchestrator (Sprint 1.3,
// July 2026, godlike/08).
//
// ClassifyGenerationStatus is the SOLE writer of
// GenerationResult.Status in the success path. The orchestrator
// order it gates is documented at the verdict §"Centralize
// success classification" and reads:
//
//	build result  →  enforce contracts  →  quality decision
//	  →  collect warnings  →  classify status (this file)  →  emit
//
// The terminal-failure path (quality gate fail without
// SkipQualityGate, clip-native strict-mode violation, etc.)
// sets result.Status = scriptpkg.ItemStatusFailed directly
// before returning the typed error; ClassifyGenerationStatus is
// NOT invoked on that path so the canonical pre-set Status
// remains authoritative.
//
// Rules:
//   - result == nil                                       → FAILED
//   - reconciliation warning present                     → PARTIALLY_SUCCEEDED
//   - qualitySkipped == true                              → SUCCEEDED_WITH_WARNINGS
//   - len(result.Warnings) > 0                            → SUCCEEDED_WITH_WARNINGS
//   - otherwise                                           → SUCCEEDED
//
// Reconciliation warnings are special: the pipeline may still return
// prose and a durable result, but one or more Drive locations were
// cleared or could not be verified. PARTIALLY_SUCCEEDED prevents that
// degraded output from being reported as a clean generation success.
//
// godlike/06 SSOT: the canonical constants live in
// internal/domain/script/generation_result.go (ItemStatus*).
// This helper imports them rather than redefining local string
// literals — the verdict §"Usa sempre le costanti di dominio"
// explicitly forbids local "SUCCEEDED" / "success" strings.
package usecase

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ClassifyGenerationStatus returns the canonical per-item
// GenerationResult.Status for the success path of the
// script.generate pipeline.
//
// qualitySkipped is the caller-supplied flag tracking whether
// the editorial quality gate failure was ignored via the
// SkipQualityGate script parameter (the verdict's
// qualitySkipped input).
//
// The result MUST NOT be nil for a non-failure generation; the
// nil branch is the defensive return for callers that invoke
// this helper before the build phase produced a result.
func ClassifyGenerationStatus(result *scriptpkg.GenerationResult, qualitySkipped bool) string {
	if result == nil {
		return scriptpkg.ItemStatusFailed
	}
	for _, warning := range result.Warnings {
		if strings.HasPrefix(warning, "asset_location_reconciliation:") {
			return scriptpkg.ItemStatusPartiallySucceeded
		}
	}
	if qualitySkipped || len(result.Warnings) > 0 {
		return scriptpkg.ItemStatusSucceededWithWarnings
	}
	return scriptpkg.ItemStatusSucceeded
}
