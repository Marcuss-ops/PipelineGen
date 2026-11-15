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
	for _, search := range manualSearchesForSegment(plan.MediaPlan, segment.ID) {
		if search.Slot != slot {
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

// manualSearchesForSegment is the single segment filter for caller-authored
// manual searches. Slot discovery and query routing must see the same rows.
func manualSearchesForSegment(plan mediadomain.MediaPlanSpec, segmentID string) []mediadomain.SegmentMediaSearch {
	segmentID = strings.TrimSpace(segmentID)
	out := make([]mediadomain.SegmentMediaSearch, 0)
	for _, search := range plan.Searches {
		if strings.TrimSpace(search.SegmentID) == segmentID {
			out = append(out, search)
		}
	}
	return out
}

// ManualSearchSlots returns the explicit slots for one segment. This is the
// slot-discovery half of the same resolver that routes the queries, so a
// search cannot be visible to one owner and invisible to the other.
func ManualSearchSlots(plan mediadomain.MediaPlanSpec, segmentID string) []mediadomain.SlotKind {
	seen := map[mediadomain.SlotKind]bool{}
	for _, search := range manualSearchesForSegment(plan, segmentID) {
		if slot := search.Slot; slot.IsValid() {
			seen[slot] = true
		}
	}
	ordered := []mediadomain.SlotKind{mediadomain.SlotPrimaryVideo, mediadomain.SlotSecondaryImage, mediadomain.SlotEvidenceOverlay, mediadomain.SlotPortrait, mediadomain.SlotDocument, mediadomain.SlotBackground, mediadomain.SlotMap}
	out := make([]mediadomain.SlotKind, 0, len(seen))
	for _, slot := range ordered {
		if seen[slot] {
			out = append(out, slot)
		}
	}
	return out
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
