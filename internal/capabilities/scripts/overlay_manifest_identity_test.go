package scriptgeneration

// overlay_manifest_identity_test.go exercises the P5 manifest-entry retry
// contract (TICKET-MANIFEST-IDENTITY gate 2): the same item_id + content-hash
// pair never repeats work and never produces a duplicate artifact.

import (
	"context"
	"errors"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

func newManifestIdentityTestEnqueuer(t *testing.T) (*QueueRenderEnqueuer, *fakeOverlayRenderCache) {
	t.Helper()
	enqueuer, err := NewQueueRenderEnqueuer(&countingRenderQueueClient{})
	if err != nil {
		t.Fatal(err)
	}
	cache := newFakeOverlayRenderCache()
	enqueuer.SetOverlayRenderCache(cache, "test-epoch")
	return enqueuer, cache
}

func TestBuildOverlayManifestEntriesDeriveIdentityAndTracks(t *testing.T) {
	plan := overlayPlanWithPhrases(2)
	works, err := buildOverlayManifestEntries(plan, "test-epoch")
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"important_phrase", "important_word", "image_overlay", "phrase_extra_1", "phrase_extra_2"}
	wantIn := []int64{667_000, 2_167_000, 3_000_000, 1_000_000, 2_000_000}
	wantOut := []int64{2_033_000, 3_200_000, 4_533_000, 1_500_000, 2_500_000}
	wantTracks := []int{0, 0, 1, 1, 1}
	if len(works) != len(wantIDs) {
		t.Fatalf("entries = %d, want %d (a background is canvas, never a manifest entry)", len(works), len(wantIDs))
	}
	for i, work := range works {
		entry := work.Entry
		if entry.ItemID != wantIDs[i] {
			t.Fatalf("entry[%d].item_id = %q, want %q (order must follow the plan)", i, entry.ItemID, wantIDs[i])
		}
		if entry.InUS != wantIn[i] || entry.OutUS != wantOut[i] {
			t.Fatalf("entry %q window = [%d,%d), want [%d,%d)", entry.ItemID, entry.InUS, entry.OutUS, wantIn[i], wantOut[i])
		}
		if entry.Track != wantTracks[i] {
			t.Fatalf("entry %q track = %d, want %d", entry.ItemID, entry.Track, wantTracks[i])
		}
		child, _, err := separateOverlayItemPlan(plan, work.Item, work.Index)
		if err != nil {
			t.Fatal(err)
		}
		key, ok := overlayRenderCacheKeyForPlan(child, "test-epoch")
		if !ok {
			t.Fatalf("per-item cache key lost for %q", entry.ItemID)
		}
		if key != work.Key {
			t.Fatalf("entry %q key = %+v, want the per-item cache identity %+v", entry.ItemID, work.Key, key)
		}
		if entry.ContentHash != key.RenderKey {
			t.Fatalf("entry %q content-hash = %q, want the per-item render key %q", entry.ItemID, entry.ContentHash, key.RenderKey)
		}
		if err := entry.Validate(); err != nil {
			t.Fatalf("entry %q rejected: %v", entry.ItemID, err)
		}
		for j := 0; j < i; j++ {
			earlier := works[j].Entry
			overlaps := earlier.InUS < entry.OutUS && entry.InUS < earlier.OutUS
			if overlaps && entry.Track <= earlier.Track {
				t.Fatalf("entry %q (track %d) overlaps earlier %q (track %d) but does not draw above it", entry.ItemID, entry.Track, earlier.ItemID, earlier.Track)
			}
		}
	}
}

func TestBuildOverlayManifestEntriesZTrackSurvivesSparseOccupation(t *testing.T) {
	// r overlaps only the HIGH item q while the low track p occupies is free:
	// a greedy lowest-track assignment would put r below q and break z-order.
	plan := overlayPlanWithPhrases(0)
	plan.Items = []capoverlay.OverlayItem{
		{ID: "p", TemplateID: "IMPORTANT_WORD", StartMs: 0, EndMs: 1000, Text: "P"},
		{ID: "q", TemplateID: "IMPORTANT_WORD", StartMs: 0, EndMs: 5000, Text: "Q"},
		{ID: "r", TemplateID: "IMPORTANT_WORD", StartMs: 2000, EndMs: 3000, Text: "R"},
	}
	works, err := buildOverlayManifestEntries(plan, "test-epoch")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"p": 0, "q": 1, "r": 2}
	if len(works) != len(want) {
		t.Fatalf("entries = %d, want %d", len(works), len(want))
	}
	for _, work := range works {
		if work.Entry.Track != want[work.Entry.ItemID] {
			t.Fatalf("entry %q track = %d, want %d", work.Entry.ItemID, work.Entry.Track, want[work.Entry.ItemID])
		}
	}
}

func TestClassifyManifestEntriesRetryDoesNoRepeatedWork(t *testing.T) {
	enqueuer, cache := newManifestIdentityTestEnqueuer(t)
	plan := overlayPlanWithPhrases(2)
	certified, pending, err := enqueuer.classifyManifestEntries(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(certified) != 0 || len(pending) != 5 {
		t.Fatalf("first attempt: certified=%d pending=%d, want 0/5", len(certified), len(pending))
	}
	retryKeys := make([]string, len(pending))
	for i, work := range pending {
		retryKeys[i] = work.Entry.RetryKey()
		artifact := certifiedOverlayArtifact()
		enqueuer.certifyManifestEntry(context.Background(), work, &artifact)
	}
	if cache.storedCount() != 5 {
		t.Fatalf("cache rows = %d, want 5 (one certified artifact per entry)", cache.storedCount())
	}
	stores := cache.stores

	// Retry the same plan: every entry is the same item_id + content-hash
	// pair, so nothing may be re-rendered and no artifact may be duplicated.
	certified, pending, err = enqueuer.classifyManifestEntries(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(certified) != 5 || len(pending) != 0 {
		t.Fatalf("retry: certified=%d pending=%d, want 5/0 (no repeated work)", len(certified), len(pending))
	}
	for i, work := range certified {
		if work.Artifact == nil {
			t.Fatalf("certified entry %q must carry its artifact", work.Entry.ItemID)
		}
		if work.Entry.RetryKey() != retryKeys[i] {
			t.Fatalf("entry %q retry identity changed across retries", work.Entry.ItemID)
		}
	}
	if cache.stores != stores {
		t.Fatalf("retry stored %d rows, want 0 (no re-certification)", cache.stores-stores)
	}
	if cache.storedCount() != 5 {
		t.Fatalf("cache rows after retry = %d, want 5 (no duplicate artifacts)", cache.storedCount())
	}
}

func TestClassifyManifestEntriesContentChangeRequiresNewWork(t *testing.T) {
	enqueuer, cache := newManifestIdentityTestEnqueuer(t)
	plan := overlayPlanWithPhrases(0)
	certified, pending, err := enqueuer.classifyManifestEntries(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 {
		t.Fatalf("first attempt pending = %d, want 3", len(pending))
	}
	oldPhraseHash := ""
	for _, work := range pending {
		if work.Item.ID == "important_phrase" {
			oldPhraseHash = work.Entry.ContentHash
		}
		artifact := certifiedOverlayArtifact()
		enqueuer.certifyManifestEntry(context.Background(), work, &artifact)
	}
	// Slide image_overlay's window (same duration) without changing its
	// content: placement is timeline metadata, so the entry keeps its identity
	// and is still reused.
	plan.Items[3].StartMs = 3500
	plan.Items[3].EndMs = 5033
	// Change the phrase's content: same item_id, new content-hash ⇒ work.
	plan.Items[1].Text = "CHANGED CONTENT"
	certified, pending, err = enqueuer.classifyManifestEntries(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(certified) != 2 || len(pending) != 1 {
		t.Fatalf("after one content change: certified=%d pending=%d, want 2/1", len(certified), len(pending))
	}
	if pending[0].Item.ID != "important_phrase" {
		t.Fatalf("pending entry = %q, want the changed item", pending[0].Item.ID)
	}
	if oldPhraseHash == "" || pending[0].Entry.ContentHash == oldPhraseHash {
		t.Fatalf("changed content must produce a new content-hash")
	}
	slidCertified := false
	for _, work := range certified {
		if work.Item.ID == "image_overlay" {
			slidCertified = true
		}
	}
	if !slidCertified {
		t.Fatal("slid entry must remain certified (placement is not content)")
	}
	if cache.storedCount() != 3 {
		t.Fatalf("cache rows = %d, want 3 (slide and classify never store)", cache.storedCount())
	}
}

func TestClassifyManifestEntriesDegradesToPendingWhenCacheFails(t *testing.T) {
	enqueuer, cache := newManifestIdentityTestEnqueuer(t)
	cache.lookupErr = errors.New("injected cache outage")
	certified, pending, err := enqueuer.classifyManifestEntries(context.Background(), overlayPlanWithPhrases(0))
	if err != nil {
		t.Fatalf("cache outage must degrade to pending, not fail: %v", err)
	}
	if len(certified) != 0 || len(pending) != 3 {
		t.Fatalf("certified=%d pending=%d, want 0/3 on cache outage", len(certified), len(pending))
	}
}
