// Package termutil provides text term and name utilities used by script
// generation and asset matching. All functions are leaf-only — they import
// ONLY pkg/leaf utilities and have zero dependency on internal/.
//
// This package exists because AGENTS.md declares pkg/termutil as the canonical
// home for SubjectMatchesTopic, ExtractLikelyNames, TermsFromText, TopicTokens
// (utility row of section 🧰 Utilities to prefer). The functions were
// originally embedded in internal/application/assets/terms.go during Onda 3,
// but the migration to pkg/wordutil was never completed and the parent
// internal/infrastructure package was kept alive as a sentinel (doc.go) so the
// orphan alias `wordutil "github.com/Marcuss-ops/Pipelinegen/internal/infrastructure"`
// would still resolve. Blocco A.3 finally completes the migration: the
// functions live here, the sentinel is gone, and the lone external consumer
// (internal/application/assets/association/service.go) imports this package
// directly.
//
// (June 2026 split, repo-wide followup after the pkg/similarity 3-file
// PR-track): the package was previously a single terms.go + subject.go
// monolith + one bundled test file. Split into 4 single-concept files with
// sibling _test.go per concept. Back-compat is automatic — same package
// name `wordutil`, no consumer import updates needed.
//
// Split layout (alphabetical):
//
//	extract_likely_names.go   — ExtractLikelyNames + LooksLikePersonName (this file).
//	subject_matches_topic.go — SubjectMatchesTopic + ConciseSubject + PreferredEntitySubject.
//	terms_from_text.go       — TermsFromText + TermsFromFields + CleanTerms + TermOptions
//	                           struct + defaultOpts() + filterTerms() private helper.
//	topic_tokens.go          — TopicTokens.
package termutil

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// LooksLikePersonName checks if the text looks like a person's name.
func LooksLikePersonName(text string) bool {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || len(parts) > 5 {
		return false
	}
	score := 0
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		first := []rune(part)[0]
		if first >= 'A' && first <= 'Z' {
			score++
		}
	}
	return score >= 1 && len(parts) <= 4
}

// ExtractLikelyNames extracts words that look like names (capitalized, >2 chars).
func ExtractLikelyNames(text string) []string {
	var names []string
	words := strings.Fields(text)
	for _, w := range words {
		w = strings.Trim(w, ".,!?:;\"'()")
		if len(w) > 2 && len(w) > 0 && w[0] >= 'A' && w[0] <= 'Z' {
			names = append(names, w)
		}
	}
	return sliceutil.UniqueStrings(names)
}
