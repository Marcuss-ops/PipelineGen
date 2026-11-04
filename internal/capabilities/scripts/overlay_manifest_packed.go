package scriptgeneration

// overlay_manifest_packed.go owns the P0 PACKED composition of the manifest
// timeline (docs/operations/TICKET-MANIFEST-CONTRACT.md §3 "packed timeline",
// TICKET-MAX-THROUGHPUT-OVERLAY-ARCHITECTURE.md §3.4): the composition
// renounces the empty plan-absolute frames. Items are re-timed on a packed
// canvas — the union of their LITERAL rendered windows, with disjoint spans
// padded to closed-GOP boundaries — so the GPU avoids the plan's silent spans
// while the final-job handoff can packet-copy each replacement window.
//
// The plan window (InUS/OutUS on the manifest entry) stays verbatim: it is
// the receipt, and the same packed artifact serves the future final handoff
// through the entry's artifact_in_us/artifact_out_us positions. Packing
// NEVER changes what an overlay draws, only where its frames live on the
// certified artifact.
//
// Safety contract (fail-closed):
//   - Packed is refused for plans that carry a source video layer or a
//     non-color background: those compositions align the background layer to
//     plan time inside the artifact, and packing would rebase it.
//   - Every candidate's literal window must be fully covered by the packed
//     union; an uncovered window is an error, never a silent offset of 0.
//   - Overlapping literal windows share a merged interval, so their relative
//     offsets (and therefore the rendered z-order) survive unchanged.

import (
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"sort"
	"strings"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

// overlayPackedGOPGridUS is the packet-copy start grid used by the final-job
// assembler. Separate packed intervals may be separated by empty canvas time;
// rounding each new interval up to this grid lets every replacement window
// start at a closed GOP without re-encoding a fragment at handoff time.
func overlayPackedGOPGridUS() int64 {
	contract := kernelmedia.DefaultAssemblyMediaContract()
	if contract.FPS.Num <= 0 || contract.FPS.Den <= 0 || contract.KeyframeInterval <= 0 {
		return 2_000_000 // Keep the canonical 24 fps / 48 frame contract fail-safe.
	}
	return int64(contract.KeyframeInterval) * 1_000_000 * int64(contract.FPS.Den) / int64(contract.FPS.Num)
}

func alignOverlayPackedOffset(offsetUS int64) int64 {
	grid := overlayPackedGOPGridUS()
	if offsetUS <= 0 || grid <= 0 {
		return 0
	}
	return ((offsetUS + grid - 1) / grid) * grid
}

// packedPlacement is one item's position on the packed composition canvas
// (artifact-relative microseconds). The pair is always a positive window of
// the item's literal duration.
type packedPlacement struct {
	ArtifactInUS  int64
	ArtifactOutUS int64
}

// planSupportsPackedComposition reports whether the manifest timeline may be
// packed for this plan: no source video layer, and no background that is
// aligned to plan time. A color background covers any span, so it packs
// safely; image/video backgrounds and opaque source compositions do not.
func planSupportsPackedComposition(plan capmodel.OverlayPlan) bool {
	if plan.Source != nil {
		return false
	}
	if plan.Background == nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(plan.Background.Kind), "color")
}

// candidateLiteralWindow is the item's rendered span on the fused
// composition, exactly what fusedOverlayBatchPlan materializes
// (metadata.SourceStartUS .. metadata.SourceEndUS). The padded standalone-clip
// target is deliberately NOT used: the fused item renders only its literal
// window, and the packed canvas renounces exactly those never-rendered
// padding holds.
func candidateLiteralWindow(plan capmodel.OverlayPlan, candidate overlayItemCandidate) (int64, int64, error) {
	_, metadata, err := separateOverlayItemPlan(plan, candidate.source, candidate.index)
	if err != nil {
		return 0, 0, fmt.Errorf("overlay item %q: %w", candidate.source.ID, err)
	}
	if metadata == nil || metadata.SourceEndUS <= metadata.SourceStartUS {
		startUS, endUS := capoverlay.OverlayItemWindowUS(candidate.source)
		if endUS <= startUS {
			return 0, 0, fmt.Errorf("overlay item %q has no positive certified window", candidate.source.ID)
		}
		return startUS, endUS, nil
	}
	return metadata.SourceStartUS, metadata.SourceEndUS, nil
}

// computePackedPlacements merges the candidates' literal windows into
// disjoint packed intervals (the union of every real overlay span), aligns
// each disjoint interval start to the final-job GOP grid, and returns each
// item's packed placement (keyed by ItemID) plus the packed canvas end. A
// candidate listed with the same ItemID twice is impossible by plan
// validation; a map item reaching this function is already rejected by
// partitionOverlayRenderWork and fusedOverlayBatchPlan double-checks it.
func computePackedPlacements(plan capmodel.OverlayPlan, candidates []overlayItemCandidate) (map[string]packedPlacement, int64, error) {
	type literalWindow struct {
		id      string
		in, out int64
	}
	windows := make([]literalWindow, 0, len(candidates))
	for _, candidate := range candidates {
		inUS, outUS, err := candidateLiteralWindow(plan, candidate)
		if err != nil {
			return nil, 0, err
		}
		windows = append(windows, literalWindow{id: strings.TrimSpace(candidate.source.ID), in: inUS, out: outUS})
	}
	sort.SliceStable(windows, func(i, j int) bool {
		if windows[i].in != windows[j].in {
			return windows[i].in < windows[j].in
		}
		return windows[i].out < windows[j].out
	})

	type interval struct{ in, out int64 }
	merged := make([]interval, 0, len(windows))
	for _, window := range windows {
		if n := len(merged); n > 0 && window.in <= merged[n-1].out {
			if window.out > merged[n-1].out {
				merged[n-1].out = window.out
			}
			continue
		}
		merged = append(merged, interval{in: window.in, out: window.out})
	}

	placements := make(map[string]packedPlacement, len(windows))
	packedEnd := int64(0)
	for _, window := range windows {
		var intervalOffset int64
		found := false
		for _, m := range merged {
			if m.in <= window.in && window.out <= m.out {
				intervalOffset += window.in - m.in
				found = true
				break
			}
			intervalOffset += m.out - m.in
			// A gap between literal intervals is free to the picture, so use it
			// to move the next interval's first frame onto the assembler's GOP
			// grid. This preserves each item's exact relative timing within its
			// interval and makes its packed source window packet-copyable.
			intervalOffset = alignOverlayPackedOffset(intervalOffset)
		}
		if !found {
			return nil, 0, fmt.Errorf("overlay item %q: literal window [%d,%d) is not covered by the packed union", window.id, window.in, window.out)
		}
		placements[window.id] = packedPlacement{
			ArtifactInUS:  intervalOffset,
			ArtifactOutUS: intervalOffset + (window.out - window.in),
		}
		if end := intervalOffset + (window.out - window.in); end > packedEnd {
			packedEnd = end
		}
	}
	return placements, packedEnd, nil
}

// stampPackedPlacements writes the packed artifact positions into the
// captured manifest entries. Every entry must receive a placement (the
// manifest-timeline path renders exactly this candidate subset together), so
// a miss is fail-closed.
func stampPackedPlacements(works []manifestEntryWork, placements map[string]packedPlacement) error {
	for i := range works {
		placement, ok := placements[works[i].Entry.ItemID]
		if !ok {
			return fmt.Errorf("overlay manifest entry %q: no packed placement was computed", works[i].Entry.ItemID)
		}
		inUS, outUS := placement.ArtifactInUS, placement.ArtifactOutUS
		works[i].Entry.ArtifactInUS = &inUS
		works[i].Entry.ArtifactOutUS = &outUS
	}
	return nil
}
