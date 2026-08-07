// Package usecase — quality_gate_misc.go
//
// Legacy lexical helpers preserved from the original quality_gate.go
// monolith. Kept verbatim (same package, same behavior) — no caller in
// this package references them today; they are retained for downstream
// consumers and parity with the ollama client equivalents.
package usecase

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/linguistics"
)

func isFunctionWord(word string) bool {
	if registry := linguistics.DefaultLexiconOrNil(); registry != nil {
		_, ok := registry.FunctionWords("fallback")[strings.ToLower(word)]
		return ok
	}
	return false
}

func looksLikeVerbBigram(words []string) bool {
	if len(words) < 2 {
		return false
	}
	verbCount := 0
	registry := linguistics.DefaultLexiconOrNil()
	var suffixes []string
	if registry != nil {
		suffixes = registry.VerbSuffixes("fallback")
	}
	for _, w := range words {
		lower := strings.ToLower(w)
		for _, suffix := range suffixes {
			if strings.HasSuffix(lower, suffix) {
				verbCount++
				break
			}
		}
	}
	return verbCount == len(words)
}
