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
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"strconv"
	"strings"
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
func overlayRenderableCandidates(plan capmodel.OverlayPlan) []overlayItemCandidate {
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
	Item  capmodel.OverlayItem
	Entry capmodel.OverlayManifestEntry
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
func buildManifestEntryWorks(parent capmodel.OverlayPlan, candidates []overlayItemCandidate, epoch string) ([]manifestEntryWork, error) {
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
		if !ok && !overlayPlanCacheAssetsIdentified(child) && len(child.Items) == 1 {
			// A source/background is not part of the entry content hash. An
			// unidentified item asset, however, cannot certify that hash.
			itemOnly := child
			itemOnly.Source, itemOnly.Background = nil, nil
			if !overlayPlanCacheAssetsIdentified(itemOnly) {
				return nil, fmt.Errorf("overlay manifest entry %q: asset has no content-addressed identity", item.ID)
			}
			renderKey := strings.ToLower(strings.TrimSpace(child.Items[0].RenderKey))
			if renderKey == "" {
				renderKey = capmodel.ComputeRenderKey(child, child.Items[0])
			}
			if len(renderKey) == 64 && isLowerHex(renderKey) {
				key.RenderKey, ok = renderKey, true
			}
		}
		if !ok {
			return nil, fmt.Errorf("overlay manifest entry %q: no content-addressed identity", item.ID)
		}
		track := 0
		for prior, window := range windows {
			if window.in < metadata.SourceEndUS && metadata.SourceStartUS < window.out && tracks[prior]+1 > track {
				track = tracks[prior] + 1
			}
		}
		entry := capmodel.OverlayManifestEntry{
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

// finalJobArtifactWindowTolerance is how far a manifest window may overhang the
// artifact's reported duration before the certificate calls it a defect. It is
// exactly one frame: `out_us` is ceil'd to a frame edge, the duration is a frame
// count, and neither side is allowed to grow past that rounding. An unknown
// frame rate yields 0, which keeps the strict comparison.
func finalJobArtifactWindowTolerance(fpsNum, fpsDen int) int64 {
	if fpsNum <= 0 || fpsDen <= 0 {
		return 0
	}
	return int64(1_000_000) * int64(fpsDen) / int64(fpsNum)
}

// manifestEntryArtifactPositions reports the entry's packed positions and
// whether they are distinct from its plan receipt.
func manifestEntryArtifactPositions(entry capmodel.OverlayManifestEntry) (int64, int64, bool) {
	if entry.ArtifactInUS == nil || entry.ArtifactOutUS == nil {
		return 0, 0, false
	}
	return *entry.ArtifactInUS, *entry.ArtifactOutUS, true
}

func finalJobManifestYearItem(entry capmodel.OverlayManifestEntry, items []capmodel.OverlayItem) (capmodel.OverlayItem, bool) {
	parts := strings.Split(strings.TrimSpace(entry.ItemID), "__")
	if len(parts) != 4 || parts[2] != "date_number" {
		return capmodel.OverlayItem{}, false
	}
	if _, err := strconv.Atoi(parts[1]); err != nil {
		return capmodel.OverlayItem{}, false
	}
	var match capmodel.OverlayItem
	found := false
	for _, item := range items {
		if !strings.EqualFold(strings.TrimSpace(item.Kind), "number") || strings.TrimSpace(item.Text) != parts[1] {
			continue
		}
		startUS, endUS := item.StartUSValue(), item.EndUSValue()
		if entry.InUS >= endUS || startUS >= entry.OutUS {
			continue
		}
		if found {
			return capmodel.OverlayItem{}, false
		}
		match, found = item, true
	}
	return match, found
}
