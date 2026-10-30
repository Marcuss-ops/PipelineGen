package scriptgeneration

import (
	"sort"
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// packedSuperBatchPlan has six disjoint two-second items across three scenes:
// two micro-batches per scene, six in total. With three lanes the packed
// grouping must produce three balanced bins, never drop or duplicate an item.
func packedSuperBatchPlan() capoverlay.OverlayPlan {
	return capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        "test-packed-superbatch",
		VideoID:       "test-packed-superbatch",
		Width:         1920,
		Height:        1080,
		FPSNum:        30,
		FPSDen:        1,
		DurationMS:    100000,
		Items: []capoverlay.OverlayItem{
			{ID: "s1a", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 0, DurationUS: 2_000_000},
			{ID: "s1b", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 2_000_000, DurationUS: 2_000_000},
			{ID: "s2a", SceneID: "scene-02", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 4_000_000, DurationUS: 2_000_000},
			{ID: "s2b", SceneID: "scene-02", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 6_000_000, DurationUS: 2_000_000},
			{ID: "s3a", SceneID: "scene-03", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 8_000_000, DurationUS: 2_000_000},
			{ID: "s3b", SceneID: "scene-03", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 10_000_000, DurationUS: 2_000_000},
		},
	}
}

func TestPackManifestGroupsIntoSuperBatchesBalancesAndKeepsEveryItem(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PACK_SUPERBATCH", "enabled")
	plan := packedSuperBatchPlan()
	candidates := overlayRenderableCandidates(plan)
	micro, err := groupManifestCandidatesIntoMicroBatches(plan, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(micro) != 6 {
		t.Fatalf("micro-batches = %d, want 6", len(micro))
	}
	packed, err := packManifestGroupsIntoSuperBatches(plan, micro, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) != 3 {
		t.Fatalf("packed super-batches = %d, want 3", len(packed))
	}
	seen := map[string]int{}
	for _, group := range packed {
		if group.sceneID != "packed" {
			t.Fatalf("super-batch scene = %q, want packed", group.sceneID)
		}
		if len(group.candidates) != 2 {
			t.Fatalf("super-batch candidates = %d, want 2 (balanced 6 items / 3 bins)", len(group.candidates))
		}
		for i := 1; i < len(group.candidates); i++ {
			if group.candidates[i-1].index >= group.candidates[i].index {
				t.Fatal("candidates must stay in plan order inside a super-batch")
			}
		}
		for _, candidate := range group.candidates {
			seen[candidate.source.ID]++
		}
	}
	if len(seen) != 6 {
		t.Fatalf("items seen = %d, want 6", len(seen))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("item %q appears %d times, want exactly once", id, count)
		}
	}
}

func TestPackManifestGroupsIntoSuperBatchesKeepsOverlapTogether(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PACK_SUPERBATCH", "enabled")
	plan := packedSuperBatchPlan()
	// s1a and s1b overlap: they are one micro-batch and must never be split
	// across super-batches (their relative offset is the rendered z-order).
	plan.Items[1].StartUS = 1_500_000
	candidates := overlayRenderableCandidates(plan)
	micro, err := groupManifestCandidatesIntoMicroBatches(plan, candidates)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := packManifestGroupsIntoSuperBatches(plan, micro, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range packed {
		hasA, hasB := false, false
		for _, candidate := range group.candidates {
			hasA = hasA || candidate.source.ID == "s1a"
			hasB = hasB || candidate.source.ID == "s1b"
		}
		if hasA != hasB {
			t.Fatalf("overlapping s1a/s1b were split across super-batches: hasA=%v hasB=%v", hasA, hasB)
		}
	}
}

func TestPackManifestGroupsIntoSuperBatchesFallsBackOnUnsupportedPlan(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PACK_SUPERBATCH", "enabled")
	plan := packedSuperBatchPlan()
	plan.Background = &capoverlay.OverlayBackground{Kind: "video", AssetRefs: []capoverlay.OverlayAssetRef{{AssetID: "bg", URL: "file:///bg.mp4"}}}
	candidates := overlayRenderableCandidates(plan)
	micro, err := groupManifestCandidatesIntoMicroBatches(plan, candidates)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := packManifestGroupsIntoSuperBatches(plan, micro, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) != len(micro) {
		t.Fatalf("video-background plan must keep per-scene groups: got %d, want %d", len(packed), len(micro))
	}
}

func TestPackManifestGroupsIntoSuperBatchesIsOptIn(t *testing.T) {
	plan := packedSuperBatchPlan()
	candidates := overlayRenderableCandidates(plan)
	micro, err := groupManifestCandidatesIntoMicroBatches(plan, candidates)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERLAY_RENDER_PACK_SUPERBATCH", "")
	kept, err := packManifestGroupsIntoSuperBatches(plan, micro, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != len(micro) {
		t.Fatalf("unset flag must keep the certified base groups: got %d, want %d", len(kept), len(micro))
	}
	t.Setenv("OVERLAY_RENDER_PACK_SUPERBATCH", "enabled")
	packed, err := packManifestGroupsIntoSuperBatches(plan, micro, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(packed) != 3 {
		t.Fatalf("explicit enable must fold to packed super-batches: got %d, want 3", len(packed))
	}
}

// TestRuntimeBaseGroupsArePerScene pins the runtime base: the golden plan's
// 64 manifest items are grouped by scene first, never fragmented into the
// 58-way micro-batch partition. The packed path is the only cross-scene fold.
func TestRuntimeBaseGroupsArePerScene(t *testing.T) {
	plan := packedSuperBatchPlan()
	candidates := overlayRenderableCandidates(plan)
	groups := groupManifestCandidatesByScene(candidates)
	if len(groups) != 3 {
		t.Fatalf("scene groups = %d, want 3", len(groups))
	}
	for _, group := range groups {
		if len(group.candidates) != 2 {
			t.Fatalf("scene %q candidates = %d, want 2", group.sceneID, len(group.candidates))
		}
	}
}

func TestPackManifestGroupsIntoSuperBatchesIsDeterministic(t *testing.T) {
	t.Setenv("OVERLAY_RENDER_PACK_SUPERBATCH", "enabled")
	plan := packedSuperBatchPlan()
	candidates := overlayRenderableCandidates(plan)
	micro, err := groupManifestCandidatesIntoMicroBatches(plan, candidates)
	if err != nil {
		t.Fatal(err)
	}
	first, err := packManifestGroupsIntoSuperBatches(plan, micro, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := packManifestGroupsIntoSuperBatches(plan, micro, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("non-deterministic batch count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].groupFirstIndex != second[i].groupFirstIndex {
			t.Fatalf("non-deterministic batch order at %d", i)
		}
		firstIDs := make([]string, 0, len(first[i].candidates))
		secondIDs := make([]string, 0, len(second[i].candidates))
		for _, candidate := range first[i].candidates {
			firstIDs = append(firstIDs, candidate.source.ID)
		}
		for _, candidate := range second[i].candidates {
			secondIDs = append(secondIDs, candidate.source.ID)
		}
		sort.Strings(firstIDs)
		sort.Strings(secondIDs)
		if len(firstIDs) != len(secondIDs) {
			t.Fatalf("batch %d membership changed", i)
		}
		for j := range firstIDs {
			if firstIDs[j] != secondIDs[j] {
				t.Fatalf("batch %d membership changed", i)
			}
		}
	}
}
