// Package usecase — quality_gate_claims.go
//
// Unsupported-claim telemetry and the grounded-source blocking rule of
// the editorial quality gate.
package usecase

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// unsupportedClaimsChecker fails when the entity-based claim counter is
// non-zero AND the source contract requires grounding. Unsupported-claim
// detection is useful telemetry for creative text, but it is not a
// reliable blocking criterion there: descriptive prose naturally adds
// details that are not literal tokens in the source text. Grounded
// sources and explicit grounding policies retain the strict behavior
// required by research and clip-native generation.
type unsupportedClaimsChecker struct{}

func (unsupportedClaimsChecker) Name() string { return "unsupported_claims" }

func (unsupportedClaimsChecker) Check(in qualityGateInput) []string {
	if in.q.UnsupportedClaims > 0 && unsupportedClaimsAreBlocking(in.plan) {
		return []string{"unsupported claims detected"}
	}
	return nil
}

// unsupportedClaimsAreBlocking keeps the entity-based claim counter as
// diagnostics for ordinary text generation while preserving a hard gate for
// sources whose contract requires grounding. An explicit policy is treated as
// grounded even when a lightweight test composition omits SourceKind.
func unsupportedClaimsAreBlocking(plan scriptpkg.ResolvedGenerationPlan) bool {
	if plan.GroundingPolicy != "" {
		return true
	}
	switch plan.SourceKind {
	case string(scriptpkg.SourceResearch), string(scriptpkg.SourceClips),
		string(scriptpkg.SourceCatalog), string(scriptpkg.SourceSearch),
		string(scriptpkg.SourceCurate):
		return true
	default:
		return false
	}
}

// countUnsupportedClaims returns the number of named entities in the
// generated text whose tokens do not appear in the source text. It
// tokenizes each entity name and requires every token to be present
// in the source token set, which avoids the false positives of
// substring matching (e.g. "John" inside "Johnson").
func countUnsupportedClaims(result *scriptpkg.GenerationResult, sourceText string) int {
	if result.Artifacts.Entities == nil {
		return 0
	}
	sourceTokens := make(map[string]struct{})
	for _, t := range tokenize(sourceText) {
		sourceTokens[t] = struct{}{}
	}
	count := 0
	check := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		tokens := tokenize(name)
		if len(tokens) == 0 {
			return
		}
		for _, t := range tokens {
			if _, ok := sourceTokens[t]; !ok {
				count++
				return
			}
		}
	}
	for _, p := range result.Artifacts.Entities.Persons {
		check(p.Value)
	}
	for _, p := range result.Artifacts.Entities.Places {
		check(p.Value)
	}
	for _, c := range result.Artifacts.Entities.Concepts {
		check(c.Value)
	}
	return count
}
