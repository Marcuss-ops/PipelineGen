// Package dto — language helpers are the canonical in-package
// surface for language-list normalization. Moved down from
// internal/application/scripts/adapters/ in Commit H Phase 2
// (June 2026) so dto/metadata.go::BuildMetadataLanguages could
// reach it without a dto→adapters import cycle (adapters would
// later inverse-import dto once design aligned). The function
// remained unused in adapters; Phase 1c Commit 2/4 (June 2026)
// formally deletes the adapters-side copy and wires the dto-side
// canonical helper into the canonical BuildMetadataLanguages
// (per the user's user-spec-fixed-impl: lowercase + trim + dedupe,
// preservation of order, "en" always first via the
// prepend-then-normalize idiom at the caller).
package dto

import "strings"

// NormalizeLanguages lowercases, trims whitespace, deduplicates, and
// preserves order for a language list.
//
// Canonical invariant: the output preserves the input order after the
// three transforms (lower / trim / dedupe). Empty entries (after
// trim) are dropped. Case is folded to lowercase so language codes are
// stable across caller-style variants ("EN", "En", "en") — ISO 639-1
// canonical form.
//
// Phase 1c Commit 2/4 (June 2026): the lowercase step was added per
// the user's spec at "lowercase + trim + dedupe, with English always
// first"; the pre-Phase-1c implementation was trim + dedupe only.
// Adding the lowercase fold is safe because the function had ZERO
// production callers pre-commit (the post-move orphan was a true
// dead code site).
func NormalizeLanguages(languages []string) []string {
	out := make([]string, 0, len(languages))
	seen := make(map[string]struct{}, len(languages))
	for _, lang := range languages {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang == "" {
			continue
		}
		if _, ok := seen[lang]; ok {
			continue
		}
		seen[lang] = struct{}{}
		out = append(out, lang)
	}
	return out
}
