package scriptgeneration

import (
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// manifestKeyTimelineEntryA/B are the two stable slot entries whose
// composition identity is exercised below: ids reused verbatim across
// regenerations, a content address each, and one entry with a placement.
func manifestKeyTimelineEntries() []capoverlay.OverlayManifestEntry {
	return []capoverlay.OverlayManifestEntry{
		{
			ItemID:      "metric-marcus__number__01",
			Track:       1,
			InUS:        0,
			OutUS:       4_000_000,
			ContentHash: "1111111111111111111111111111111111111111111111111111111111111111",
		},
		{
			ItemID:      "metric-ella__number__02",
			Track:       2,
			InUS:        2_000_000,
			OutUS:       6_000_000,
			ContentHash: "2222222222222222222222222222222222222222222222222222222222222222",
			Placement:   &capoverlay.ManifestPlacement{WPx: 10, HPx: 10},
		},
	}
}

func manifestKeyTimeline(planID string) capoverlay.OverlayManifest {
	return capoverlay.OverlayManifest{
		SchemaVersion: capoverlay.SchemaVersionManifest,
		PlanID:        planID,
		VideoID:       "video-run-" + planID,
		Entries:       manifestKeyTimelineEntries(),
	}
}

func manifestKeyChildPlan() capoverlay.OverlayPlan {
	return capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        "run-999",
		VideoID:       "video-run-999",
		Width:         1920,
		Height:        1080,
		FPSNum:        24,
	}
}

// TestOverlayManifestCacheKeyIgnoresRunIdentity pins the reuse contract of the
// manifest timeline composition: plan ids are per-run (run_f…), and
// OverlayManifest.Digest hashes them, so keying the cache on the digest named
// identical pixels differently on every run — comp rows were written and never
// read. The cache key must key on the entries' stable composition identity
// (slot id, layer, window, content address, placement) so a re-run of the same
// timeline shares one cache row.
func TestOverlayManifestCacheKeyIgnoresRunIdentity(t *testing.T) {
	first, ok := overlayManifestCompositionCacheKey(manifestKeyChildPlan(), manifestKeyTimeline("run-111"), "epoch-1")
	if !ok {
		t.Fatal("a well-formed manifest must yield a cache key")
	}
	retry, ok := overlayManifestCompositionCacheKey(manifestKeyChildPlan(), manifestKeyTimeline("run-222"), "epoch-1")
	if !ok {
		t.Fatal("the retry manifest must yield a cache key")
	}
	if first != retry {
		t.Fatal("the same timeline content across runs must share one cache key")
	}

	// The epoch splits rows: a Chronon/renderer upgrade is not the same bytes
	// even for identical content.
	otherEpoch, ok := overlayManifestCompositionCacheKey(manifestKeyChildPlan(), manifestKeyTimeline("run-111"), "epoch-2")
	if !ok || otherEpoch == first {
		t.Fatal("a different epoch must name a different cache row")
	}

	// Editing one entry's content must not reuse the previous timeline.
	edited := manifestKeyTimeline("run-111")
	edited.Entries[1].ContentHash = "3333333333333333333333333333333333333333333333333333333333333333"
	differentContent, ok := overlayManifestCompositionCacheKey(manifestKeyChildPlan(), edited, "epoch-1")
	if !ok || differentContent == first {
		t.Fatal("different entry content must name a different cache row")
	}

	// Same pixels at a different placement are a different composition.
	moved := manifestKeyTimeline("run-111")
	moved.Entries[1].Placement = &capoverlay.ManifestPlacement{WPx: 20, HPx: 10}
	differentPlacement, ok := overlayManifestCompositionCacheKey(manifestKeyChildPlan(), moved, "epoch-1")
	if !ok || differentPlacement == first {
		t.Fatal("a different placement rect must name a different cache row")
	}

	// A packed timeline is a different artifact frame grid: the packed
	// positions must split the row from the plan-absolute composition, and the
	// nil/zero distinction must hold.
	packed := manifestKeyTimeline("run-111")
	zero := int64(0)
	packed.Entries[1].ArtifactInUS = &zero
	packed.Entries[1].ArtifactOutUS = &zero
	packedRow, ok := overlayManifestCompositionCacheKey(manifestKeyChildPlan(), packed, "epoch-1")
	if !ok || packedRow == first {
		t.Fatal("packed positions must name a different cache row")
	}
	shifted := int64(1200)
	packed.Entries[1].ArtifactOutUS = &shifted
	shiftedRow, ok := overlayManifestCompositionCacheKey(manifestKeyChildPlan(), packed, "epoch-1")
	if !ok || shiftedRow == packedRow {
		t.Fatal("packed positions of zero and a shifted value must not alias (nil/zero vs value)")
	}
}

// TestOverlayManifestCacheKeyRejectsIncompleteEntry pins the fail-closed
// behavior: an entry without a canonical content address cannot name a cache
// row (the identity is incomplete rather than silently reusable).
func TestOverlayManifestCacheKeyRejectsIncompleteEntry(t *testing.T) {
	empty := manifestKeyTimeline("run-111")
	empty.Entries[0].ContentHash = ""
	if _, ok := overlayManifestCompositionCacheKey(manifestKeyChildPlan(), empty, "epoch-1"); ok {
		t.Fatal("an entry without a content hash must yield no cache key")
	}
}
