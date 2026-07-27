// Package mediamemory — resolver_lookup.go is the canonical home
// for the Level 0/1 cache-lookup surface: normalizer-driven
// fingerprint computation + ConceptRepository fingerprint
// resolution.
//
// godlike/06 SSOT (single canonical home per layer): every
// (language, normalized_text) → PhraseFingerprint computation
// that lives outside the Normalizer itself routes through this
// file so the cache invalidation contract has one grep-able
// home.
//
// File split ownership (godlike/06 SSOT):
//   - resolver.go                 : Resolver port + VisualResolver struct + ResolverDeps + ctors + pins + EmbeddingVersion
//   - resolver_lookup.go          : canonicalConceptForLookup + fingerprintForNormalized + defaultResolverLimit  ← this file
//   - resolver_orchestration.go   : Resolve + resolveScene + candidatesForSlot + levelExactMatch + mediaTypesForSlot + priorSceneVideoID
//   - resolver_scoring.go         : rankedCandidate + buildFilterFlags + aspectMismatchFor + buildRankingInput + durationFitScore + clamp01 + sort + layerFromFilteredCandidate + upgradeSource
//   - resolver_projection.go      : bindingsToFilteredCandidates + candidatesToFilteredCandidates
//   - resolver_brain.go           : errInvalidPhrase + Search method (brain.MediaMemoryResolutionPort impl)
package mediamemory

import (
	"context"
)

// canonicalConceptForLookup computes the canonical phrase via the
// injected Normalizer (godlike/06 SSOT) and resolves the matching
// concept row by PhraseFingerprint. Returns ErrConceptNotFound on miss.
func (r *VisualResolver) canonicalConceptForLookup(ctx context.Context, scene SceneSpec) (MediaConcept, error) {
	c, err := r.normalizer.Normalize(ctx, scene.Text, scene.Language)
	if err != nil {
		return MediaConcept{}, err
	}
	return r.concepts.FindByFingerprint(ctx, c.Language, c.PhraseFingerprint)
}

// fingerprintForNormalized is the package-level helper kept for
// test compatibility ONLY. Production code MUST use
// defaultNormalizer.Fingerprint (or any Normalizer impl) — this
// helper is byte-equivalent so the in-package resolver tests can
// pre-compute fingerprints without depending on a Normalizer at
// construction time.
//
// godlike/06 SSOT: tests use this helper to seed ConceptRepository
// fixtures; production code reaches the canonical SHA256 via
// r.normalizer.Normalize(...).
func fingerprintForNormalized(language, normalized string) string {
	return NewDefaultNormalizer("").Fingerprint(language, normalized)
}
