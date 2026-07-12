// Package scripts — engine_metrics.go (PR-CS-1 FASE 14, July 2026).
//
// Canonical BCP-47 -> country mapping + thin increment wrapper for the
// /api/script/generate Branch A vs Branch B telemetry cutover.
//
// godlike/06 SSOT:
//   - the production-level promauto counter ScriptGenerationBranchTotal
//     lives at internal/infrastructure/observability/metrics_scripts.go
//     (one canonical owner per fact, parallel to ScriptGenerationTotal).
//   - this file owns the BCP-47 -> country parser
//     (ExtractCountryForTelemetry) and the per-call increment wrapper
//     (RecordScriptGenerationBranch).
//
// Branch labels emitted to the production counter:
//   - "a" -> ScriptSegment canonical path (PR-CS-1 FASE 14 CUTOVER default).
//   - "b" -> legacy SegmentTopics path (deprecation DL-SCRIPT-BRANCH-B-001).
//
// Rollback: env VELOX_SCRIPTS_SEGMENT_DEFAULT=false flips Branch A off;
// Branch B then becomes the default fallback.
package usecase

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// ExtractCountryForTelemetry maps a BCP-47 tag to a 2-letter country
// code for the script_generation_branch_total{country} label.
//
// Canonical mapping:
//
//	"it-IT"   -> "IT"
//	"en-US"   -> "US"
//	"pt-BR"   -> "BR"
//	"es-ES"   -> "ES"
//	"fr-FR"   -> "FR"
//	"de-DE"   -> "DE"
//	"it"      -> "IT"   (language-only fallback: uppercase the lang)
//	""        -> "XX"   (empty sentinel)
//	"ZH"      -> "ZH"   (already uppercase pass-through)
//
// godlike/06 SSOT: this function is the SOLE canonical owner of the
// BCP-47 -> country mapping for script-telemetry labels. Don't add a
// duplicate parser elsewhere - extend this one.
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

// RecordScriptGenerationBranch emits one increment to the canonical
// script_generation_branch_total{branch=*, country=*} promauto
// collector (declared at
// internal/infrastructure/observability/metrics_scripts.go).
//
// Branch label is canonicalised to lowercase ("a" or "b"). Empty
// branch is silently dropped (defensive guard - the increment should
// never be called with an empty branch).
//
// godlike/06 SSOT: this is the ONLY emission path; the engine_prompt.go
// per-branch tail call is the canonical call site. Do not add
// additional increments elsewhere without coordinating with
// metrics_scripts.go.
func RecordScriptGenerationBranch(branch string, bcp47 string) {
	if branch == "" {
		return
	}
	country := ExtractCountryForTelemetry(bcp47)
	observability.ScriptGenerationBranchTotal.WithLabelValues(strings.ToLower(branch), country).Inc()
}
