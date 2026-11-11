package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// internetImageDiscoveryMode is what a segment's discovery pass decides to do.
// It is a closed set because the caller branches on it: a new outcome must be a
// new constant, not a silently different query list.
type internetImageDiscoveryMode string

const (
	// internetImageDiscoveryBypass means the segment asked for no image at all
	// (no contextual query, no entity query, no manual query).
	internetImageDiscoveryBypass internetImageDiscoveryMode = "bypass"
	// internetImageDiscoveryReuse means the fan-out already owns discovery for
	// every requested identity; the provider must not be called again.
	internetImageDiscoveryReuse internetImageDiscoveryMode = "reuse_fanout"
	// internetImageDiscoverySearch means the merged queries must be resolved by
	// the query-keyed cache/provider path.
	internetImageDiscoverySearch internetImageDiscoveryMode = "search"
)

// internetImageDiscoveryPlan is the pure answer to "what does this segment
// search, and why?".
type internetImageDiscoveryPlan struct {
	Mode    internetImageDiscoveryMode
	Queries []string
}

// planInternetImageDiscovery merges the three query sources and decides whether
// discovery already happened. It is the rule that used to live inline in the
// segment loop, where the only way to test it was to run the stage:
//
//  1. identity wins over topic: in entity-image mode the entity queries REPLACE
//     the contextual ones (a courthouse view cannot stand in for a portrait);
//  2. an operator's manual query is always kept, appended after the derived
//     ones and deduped case-insensitively;
//  3. with no query left there is nothing to search, so the segment is bypassed;
//  4. a fan-out hit is sufficient for contextual images, but in entity mode it
//     only counts when every requested identity already has a matching
//     candidate — otherwise the segment search would be suppressed by a warm
//     generic scene image.
func planInternetImageDiscovery(contextual, entity, manual []string, entityMode, fanoutOwned bool, covers func(string) bool) internetImageDiscoveryPlan {
	// Explicit media-plan searches are the operator's complete retrieval scope
	// for this segment. Mixing them with inferred entities or semantic hints
	// lets generic fragments (for example a month or a court label) consume the
	// image budget with unrelated results.
	if explicit := imageQueryList(manual); len(explicit) > 0 {
		if fanoutOwned {
			return internetImageDiscoveryPlan{Mode: internetImageDiscoveryReuse, Queries: explicit}
		}
		return internetImageDiscoveryPlan{Mode: internetImageDiscoverySearch, Queries: explicit}
	}
	base := imageQueryList(contextual)
	if entityMode {
		if identity := imageQueryList(entity); len(identity) > 0 {
			base = identity
		}
	}
	queries := imageQueryList(append(append([]string(nil), base...), manual...))
	if len(queries) == 0 {
		return internetImageDiscoveryPlan{Mode: internetImageDiscoveryBypass}
	}
	covered := true
	if entityMode && covers != nil {
		for _, query := range queries {
			if !covers(query) {
				covered = false
				break
			}
		}
	}
	if fanoutOwned && covered {
		return internetImageDiscoveryPlan{Mode: internetImageDiscoveryReuse, Queries: queries}
	}
	return internetImageDiscoveryPlan{Mode: internetImageDiscoverySearch, Queries: queries}
}

// imageQueryList trims, drops empties and dedupes while preserving the order,
// so the same inputs always produce the same provider calls.
func imageQueryList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

// fanoutCoversImageQueries prevents a contextual-image cache hit from
// suppressing entity-specific searches. It returns true only when every
// requested image identity already has a ready, identity-matching candidate.
func fanoutCoversImageQueries(queries []string, segment *scriptpkg.VidRushSegmentResult) bool {
	if len(queries) == 0 || segment == nil {
		return false
	}
	for _, query := range queries {
		if len(entityImageMatches(normalizeEntityMatch(query), *segment)) == 0 {
			return false
		}
	}
	return true
}

// fanoutAlreadyOwnsInternetImageDiscovery reports whether the incremental
// VidRush fan-out already produced the internet-image candidate set for this
// segment. HIT_EXACT and REFRESHED are authoritative: a second provider search
// in the postprocessor would duplicate the exact work the fan-out just did.
//
// MISS is deliberately excluded: an empty/negative fan-out answer stays
// retryable by the postprocessor, matching the existing no-negative-cache
// contract. REFRESHED is also authoritative on a force-refresh plan because
// the fan-out performed that refresh itself.
func fanoutAlreadyOwnsInternetImageDiscovery(plan *scriptpkg.ResolvedGenerationPlan, segment *scriptpkg.VidRushSegmentResult) bool {
	if plan == nil || segment == nil {
		return false
	}
	state := strings.ToUpper(strings.TrimSpace(segment.Cache.InternetImages))
	if state != "HIT_EXACT" && state != "REFRESHED" {
		return false
	}
	if (plan.ForceRefresh || plan.MediaPlan.ForceRefreshAssets) && state != "REFRESHED" {
		return false
	}
	return len(internetImageCandidatesFromSelection(segment.Assets)) > 0
}

// reuseFanoutInternetImageCandidates projects the candidates already attached
// by the fan-out onto both asset surfaces the materializer reads. It never
// invents a candidate and never calls a provider.
func reuseFanoutInternetImageCandidates(segment *scriptpkg.VidRushSegmentResult) {
	if segment == nil {
		return
	}
	candidates := internetImageCandidatesFromSelection(segment.Assets)
	if len(candidates) == 0 {
		return
	}
	segment.Assets.Candidates = AppendProviderCandidatesUnique(segment.Assets.Candidates, candidates)
	segment.Assets.SecondaryImages = AppendProviderCandidatesUnique(segment.Assets.SecondaryImages, candidates)
}

func internetImageCandidatesFromSelection(assets scriptpkg.SegmentAssetSelection) []scriptpkg.SegmentAssetCandidate {
	out := make([]scriptpkg.SegmentAssetCandidate, 0, len(assets.Candidates)+len(assets.SecondaryImages))
	seen := make(map[string]struct{}, cap(out))
	for _, candidate := range append(append([]scriptpkg.SegmentAssetCandidate(nil), assets.Candidates...), assets.SecondaryImages...) {
		if !strings.EqualFold(strings.TrimSpace(candidate.Provider), scriptpkg.VidRushProviderInternetImages) {
			continue
		}
		key := vidRushCandidateIdentity(candidate)
		if key == "" {
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, candidate)
	}
	return out
}
