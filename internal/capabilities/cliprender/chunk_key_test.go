package cliprender

import (
	"fmt"
	"strings"
	"testing"
)

// chunk_key_test.go pins the SEGMENT-IDENTITY half of the I1 chunk producer.
//
// render_cache.go records WHY the whole-clip cache was left whole-clip: a
// segmented render needs a per-SEGMENT entry whose key is
// `(fingerprint, frame_range)`, and adding that key before a producer exists
// would be a dead field that silently never matches. The identity half of that
// key already exists — chunk_plan.go derives every window's JobID from the plan
// content address plus the window's exact boundaries — so these tests pin the
// contract a future per-window cache key must rest on, WITHOUT introducing the
// dead field the cache contract forbids.

// chunkWindowKey is the readable form of a window used in failure messages.
func chunkWindowKey(c Chunk) string { return fmt.Sprintf("[%d,%d)", c.StartFrame, c.EndFrame) }

// TestChunkWindowIdentityIsTheRangeKey pins that a window's id is a function of
// the plan revision AND the exact range: the same window addresses the same id
// forever, and two different windows of one plan can never share an id.
func TestChunkWindowIdentityIsTheRangeKey(t *testing.T) {
	plan := chunkTestPlan(t)
	set, err := BuildChunkSet(plan, 5, 48)
	if err != nil {
		t.Fatal(err)
	}
	if set.WindowCount() < 2 {
		t.Fatalf("fixture must produce more than one window, got %d", set.WindowCount())
	}

	// Stability: rebuilding the same plan and request yields the same ids.
	again, err := BuildChunkSet(plan, 5, 48)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for i, c := range set.Chunks {
		if again.Chunks[i].JobID != c.JobID {
			t.Fatalf("window %s is not stable across builds: %q vs %q",
				chunkWindowKey(c), c.JobID, again.Chunks[i].JobID)
		}
		// Pairwise distinct: a duplicate id would make two windows address one
		// cache entry, so the second render would never happen.
		if prev, ok := byID[c.JobID]; ok {
			t.Fatalf("windows %s and %s share the id %q; the range is not in the key",
				prev, chunkWindowKey(c), c.JobID)
		}
		byID[c.JobID] = chunkWindowKey(c)
	}
}

// TestChunkIdentityIsTheWindowNotTheSplit pins the property that makes a retry
// cheap: two DIFFERENT partitions of the same plan that happen to produce the
// same window carry the SAME id, because the identity is the window and not the
// request that produced it. A producer that re-partitions (say it discovers
// more lanes) therefore reuses an already-rendered window instead of rendering
// it twice.
func TestChunkIdentityIsTheWindowNotTheSplit(t *testing.T) {
	plan := chunkTestPlan(t)
	coarse, err := BuildChunkSet(plan, 3, 48)
	if err != nil {
		t.Fatal(err)
	}
	fine, err := BuildChunkSet(plan, 5, 48)
	if err != nil {
		t.Fatal(err)
	}
	if coarse.WindowCount() == fine.WindowCount() {
		t.Fatalf("fixture must produce two different splits; both have %d windows", coarse.WindowCount())
	}

	byWindow := make(map[string]string, len(coarse.Chunks))
	for _, c := range coarse.Chunks {
		byWindow[chunkWindowKey(c)] = c.JobID
	}
	shared := 0
	for _, c := range fine.Chunks {
		id, ok := byWindow[chunkWindowKey(c)]
		if !ok {
			continue
		}
		shared++
		if id != c.JobID {
			t.Fatalf("window %s carries two ids across splits: %q vs %q", chunkWindowKey(c), id, c.JobID)
		}
	}
	if shared == 0 {
		t.Fatal("no window is shared by the two splits; the fixture no longer exercises the property")
	}
}

// TestChunkIdentityCarriesThePlanRevision pins that the range alone is not the
// key either: two plan revisions with the SAME frame count and bit-identical
// windows must not share a window id, or an edit that leaves the timeline
// length unchanged would be served from the pre-edit render.
func TestChunkIdentityCarriesThePlanRevision(t *testing.T) {
	plan := chunkTestPlan(t)
	base, err := BuildChunkSet(plan, 5, 48)
	if err != nil {
		t.Fatal(err)
	}
	// Edit plan CONTENT without touching duration or frame rate, so the windows
	// are identical and only the plan digest moves.
	edited := resealed(t, plan, func(p *ClipRenderPlanV1) {
		p.Source.AssetID = "yt_other_source"
	})
	other, err := BuildChunkSet(edited, 5, 48)
	if err != nil {
		t.Fatal(err)
	}
	for i := range base.Chunks {
		if chunkWindowKey(base.Chunks[i]) != chunkWindowKey(other.Chunks[i]) {
			t.Fatalf("windows must be identical for this case: %s vs %s",
				chunkWindowKey(base.Chunks[i]), chunkWindowKey(other.Chunks[i]))
		}
		if base.Chunks[i].JobID == other.Chunks[i].JobID {
			t.Fatalf("window %s reused the id %q across two plan revisions",
				chunkWindowKey(base.Chunks[i]), base.Chunks[i].JobID)
		}
	}
	if base.AnchorJobID() == other.AnchorJobID() {
		t.Fatal("the assembly anchor reused an id across two plan revisions")
	}
}

// TestChunkIdentityIsNotAWholeClipFingerprint pins the non-conflation rule the
// cache key depends on. The clip lane addresses a whole render by a 64-char
// SHA-256 hex fingerprint; a window must live in a DIFFERENT namespace, so a
// per-window entry can never be served for a whole-clip request (or the
// reverse) even if the two maps were ever merged into one.
func TestChunkIdentityIsNotAWholeClipFingerprint(t *testing.T) {
	plan := chunkTestPlan(t)
	set, err := BuildChunkSet(plan, 3, 48)
	if err != nil {
		t.Fatal(err)
	}

	// A real whole-clip fingerprint, computed through the production path.
	req := &RenderRequest{SourceAssetID: plan.Source.AssetID}
	req.Normalize()
	clipFP, err := req.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	if !isSHA256Hex(clipFP) {
		t.Fatalf("whole-clip fingerprint %q is not a canonical digest; the fixture is wrong", clipFP)
	}

	for _, c := range set.Chunks {
		if isSHA256Hex(c.JobID) {
			t.Fatalf("chunk id %q is a canonical digest: it could collide with a whole-clip fingerprint", c.JobID)
		}
		if !strings.HasPrefix(c.JobID, "chunk-") {
			t.Fatalf("chunk id %q is not namespaced; conflation with another key family becomes possible", c.JobID)
		}
		if c.JobID == clipFP {
			t.Fatalf("chunk id %q equals the whole-clip fingerprint", c.JobID)
		}
		if c.JobID == set.AnchorJobID() {
			t.Fatalf("window %s shares the anchor's id %q", chunkWindowKey(c), c.JobID)
		}
	}

	anchor := set.AnchorJobID()
	if isSHA256Hex(anchor) {
		t.Fatalf("anchor id %q is a canonical digest", anchor)
	}
	if !strings.HasPrefix(anchor, "anchor-") {
		t.Fatalf("anchor id %q is not namespaced", anchor)
	}
	if anchor == clipFP {
		t.Fatalf("anchor id %q equals the whole-clip fingerprint", anchor)
	}
}
