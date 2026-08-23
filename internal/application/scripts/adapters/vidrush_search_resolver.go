package adapters

import (
	"strings"

	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ResolveManualSegmentQueries returns the caller-supplied search queries for
// one segment/provider/slot. Locked assignments are authoritative and suppress
// provider search. Empty Providers and MediaTypes are intentionally treated as
// unconstrained, matching the media-plan contract.
func ResolveManualSegmentQueries(
	plan *scriptpkg.ResolvedGenerationPlan,
	segment scriptpkg.CanonicalSegment,
	provider string,
	slot mediadomain.SlotKind,
) []string {
	if plan == nil {
		return nil
	}
	if hasLockedSegmentAssignment(plan.MediaPlan.Assignments, segment.ID, slot) {
		return nil
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	queries := make([]string, 0)
	seen := make(map[string]struct{})
	for _, search := range plan.MediaPlan.Searches {
		if strings.TrimSpace(search.SegmentID) != strings.TrimSpace(segment.ID) || search.Slot != slot {
			continue
		}
		if !searchAllowsProvider(search.Providers, provider) || !searchAllowsSlotMediaTypes(search.MediaTypes, slot) {
			continue
		}
		query := strings.TrimSpace(search.Query)
		if query == "" {
			continue
		}
		key := strings.ToLower(query)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		queries = append(queries, query)
	}
	return queries
}

func hasLockedSegmentAssignment(assignments []mediadomain.SegmentMediaAssignment, segmentID string, slot mediadomain.SlotKind) bool {
	for _, assignment := range assignments {
		if assignment.Locked && assignment.SegmentID == segmentID && assignment.Slot == slot {
			return true
		}
	}
	return false
}

func searchAllowsProvider(providers []string, provider string) bool {
	if len(providers) == 0 {
		return true
	}
	for _, candidate := range providers {
		if strings.EqualFold(strings.TrimSpace(candidate), provider) {
			return true
		}
	}
	return false
}

func searchAllowsSlotMediaTypes(mediaTypes []string, slot mediadomain.SlotKind) bool {
	if len(mediaTypes) == 0 {
		return true
	}
	for _, mediaType := range mediaTypes {
		if mediadomain.IsMediaTypeAllowed(slot, strings.ToLower(strings.TrimSpace(mediaType))) {
			return true
		}
	}
	return false
}
