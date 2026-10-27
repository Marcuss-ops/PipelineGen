package scriptgeneration

// overlay_manifest_identity.go owns the P5 manifest-entry identity layer
// (docs/operations/TICKET-MANIFEST-IDENTITY.md, gate 2): one manifest entry per
// overlay item — item_id → (track, in_us, out_us, content-hash) — plus its
// retry classification against the content-addressed overlay render cache.
//
// The entry content-hash is the item's render key on the CHILD plan the render
// path actually executes, so manifest entries and per-item renders share ONE
// content-addressed namespace: the cache row keyed by that identity certifies
// the entry's CLIP content, and a retried entry resolves to it regardless of
// which run rendered it. A manifest composition is certified separately by its
// manifest digest (overlay_manifest_timeline.go) and never overwrites clip
// rows. The child plan is rebased to start at zero, so the identity covers the
// clip's content (text, assets, styles, relative timing, canvas) and
// deliberately NOT its timeline placement: sliding an entry's window is
// metadata, not new content.
//
// Retry contract (gate 2): the same item_id + content-hash pair maps to the
// same entry work identity, so a retried entry resolves to its certified
// artifact instead of re-rendering, and the cache's Store (idempotent on the
// key digest) can never produce a second artifact for one entry.

import (
	"fmt"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// isOverlayBackgroundTemplate is the single source of truth for the legacy
// background spelling: a background is a canvas primitive, not a user-facing
// overlay. It must never become its own rendered item, manifest entry or
// Drive video.
func isOverlayBackgroundTemplate(templateID string) bool {
	switch strings.ToUpper(strings.TrimSpace(templateID)) {
	case "BACKGROUND", "VIDEO_BACKGROUND":
		return true
	default:
		return false
	}
}

// overlayRenderableCandidates is the single admission list for overlay render
// work: every plan item except background canvas primitives, in plan order
// with its original plan index preserved (child plan identity depends on it).
func overlayRenderableCandidates(plan capoverlay.OverlayPlan) []overlayItemCandidate {
	candidates := make([]overlayItemCandidate, 0, len(plan.Items))
	for index, item := range plan.Items {
		if isOverlayBackgroundTemplate(item.TemplateID) {
			continue
		}
		candidates = append(candidates, overlayItemCandidate{index: index, source: item})
	}
	return candidates
}

// manifestEntryWork is one manifest entry paired with the identity that
// certifies its rendered content.
type manifestEntryWork struct {
	// Index is the item's position in the parent plan, preserved so child
	// plan identity and error attribution stay stable across retries.
	Index int
	Item  capoverlay.OverlayItem
	Entry capoverlay.OverlayManifestEntry
	// Key is the content-addressed cache identity of the entry's rendered
	// content — the exact identity the per-item path certifies.
	Key OverlayRenderCacheKey
	// Artifact is the certified content already in the cache. Nil means the
	// entry is pending work.
	Artifact *RenderArtifact
}

// buildManifestEntryWorks derives the manifest entries for an explicit
// candidate subset of the parent plan (the manifest-timeline composition
// excludes maps and chain items, which keep their dedicated render paths).
// Track is computed across the whole subset so z-order survives the manifest
// projection unchanged.
func buildManifestEntryWorks(parent capoverlay.OverlayPlan, candidates []overlayItemCandidate, epoch string) ([]manifestEntryWork, error) {
	works := make([]manifestEntryWork, 0, len(candidates))
	type entryWindow struct{ in, out int64 }
	windows := make([]entryWindow, 0, len(candidates))
	tracks := make([]int, 0, len(candidates))
	for _, candidate := range candidates {
		index, item := candidate.index, candidate.source
		child, metadata, err := separateOverlayItemPlan(parent, item, index)
		if err != nil {
			return nil, err
		}
		key, ok := overlayRenderCacheKeyForPlan(child, epoch)
		if !ok {
			return nil, fmt.Errorf("overlay manifest entry %q: no content-addressed identity", item.ID)
		}
		track := 0
		for prior, window := range windows {
			if window.in < metadata.SourceEndUS && metadata.SourceStartUS < window.out && tracks[prior]+1 > track {
				track = tracks[prior] + 1
			}
		}
		entry := capoverlay.OverlayManifestEntry{
			ItemID:      strings.TrimSpace(item.ID),
			Track:       track,
			InUS:        metadata.SourceStartUS,
			OutUS:       metadata.SourceEndUS,
			ContentHash: key.RenderKey,
		}
		if err := entry.Validate(); err != nil {
			return nil, err
		}
		works = append(works, manifestEntryWork{Index: index, Item: item, Entry: entry, Key: key})
		windows = append(windows, entryWindow{in: metadata.SourceStartUS, out: metadata.SourceEndUS})
		tracks = append(tracks, track)
	}
	return works, nil
}
