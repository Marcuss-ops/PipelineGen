package scriptgeneration

import (
	"testing"

	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
)

// basePackedTestPlan mirrors manifestTestPlan but with items whose literal
// windows carry large empty spans: item-01 [10s,12s), item-02 [30s,32s),
// item-03 [50s,52s). Packed, the composition covers 6 seconds total instead
// of 52.
func basePackedTestPlan() capoverlay.OverlayPlan {
	return capoverlay.OverlayPlan{
		SchemaVersion: capoverlay.SchemaVersionPlan,
		PlanID:        "test-packed-timeline",
		VideoID:       "test-packed-timeline",
		Width:         1920,
		Height:        1080,
		FPSNum:        30,
		FPSDen:        1,
		DurationMS:    100000,
		Items: []capoverlay.OverlayItem{
			{ID: "item-01", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 10_000_000, DurationUS: 2_000_000},
			{ID: "item-02", SceneID: "scene-01", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 30_000_000, DurationUS: 2_000_000},
			{ID: "item-03", SceneID: "scene-02", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 50_000_000, DurationUS: 2_000_000},
		},
	}
}

// TestPlanSupportsPackedComposition pins the packed-composition admission
// guard (overlay_manifest_packed.go): plans whose composition aligns a
// background or source layer to plan time may never pack; color-only canvases
// may.
func TestPlanSupportsPackedComposition(t *testing.T) {
	plan := basePackedTestPlan()

	if !planSupportsPackedComposition(plan) {
		t.Fatal("a background-free plan must support the packed composition")
	}

	colorPlan := plan
	colorPlan.Background = &capoverlay.OverlayBackground{Kind: "color"}
	if !planSupportsPackedComposition(colorPlan) {
		t.Fatal("a color background covers any canvas span and must not block packing")
	}

	videoPlan := plan
	videoPlan.Background = &capoverlay.OverlayBackground{Kind: "video"}
	if planSupportsPackedComposition(videoPlan) {
		t.Fatal("a video background is aligned to plan time and must never pack")
	}

	sourcePlan := plan
	sourcePlan.Source = &capoverlay.OverlaySource{AssetID: "asset-01", SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	if planSupportsPackedComposition(sourcePlan) {
		t.Fatal("an opaque source composition is aligned to plan time and must never pack")
	}
}

// TestComputePackedPlacementsUnion pins the packed canvas arithmetic: the
// union of the literal windows, laid out back to back, with every item inside
// its merged interval at the exact relative offset.
func TestComputePackedPlacementsUnion(t *testing.T) {
	plan := basePackedTestPlan()
	candidates := overlayRenderableCandidates(plan)
	if len(candidates) != 3 {
		t.Fatalf("expected 3 renderable candidates, got %d", len(candidates))
	}
	placements, packedEnd, err := computePackedPlacements(plan, candidates)
	if err != nil {
		t.Fatalf("computePackedPlacements: %v", err)
	}
	// Literal windows [10,12), [30,32), [50,52): the packed canvas is exactly
	// the sum of the three 2-second windows.
	want := map[string][2]int64{
		"item-01": {0, 2_000_000},
		"item-02": {2_000_000, 4_000_000},
		"item-03": {4_000_000, 6_000_000},
	}
	if packedEnd != 6_000_000 {
		t.Fatalf("packed end = %d, want 6_000_000", packedEnd)
	}
	for id, window := range want {
		placement, ok := placements[id]
		if !ok {
			t.Fatalf("no placement for %q", id)
		}
		if placement.ArtifactInUS != window[0] || placement.ArtifactOutUS != window[1] {
			t.Fatalf("%q packed = [%d,%d), want [%d,%d)", id, placement.ArtifactInUS, placement.ArtifactOutUS, window[0], window[1])
		}
	}
}

// TestComputePackedPlacementsMergesOverlaps pins the overlap case: windows
// that touch or overlap share one merged interval, so relative offsets (and
// with them the rendered z-order) survive the packing unchanged.
func TestComputePackedPlacementsMergesOverlaps(t *testing.T) {
	plan := basePackedTestPlan()
	plan.Items = []capoverlay.OverlayItem{
		{ID: "a", SceneID: "s1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 1_000_000, DurationUS: 2_000_000}, // [1,3)
		{ID: "b", SceneID: "s1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 2_500_000, DurationUS: 2_000_000}, // [2.5,4.5) overlaps a
		{ID: "c", SceneID: "s1", Kind: "text_phrase", TemplateID: "IMPORTANT_PHRASE", StartUS: 9_000_000, DurationUS: 1_000_000}, // [9,10) disjoint
	}
	candidates := overlayRenderableCandidates(plan)
	placements, packedEnd, err := computePackedPlacements(plan, candidates)
	if err != nil {
		t.Fatalf("computePackedPlacements: %v", err)
	}
	// Merged intervals: [1,4.5) (3.5s) plus [9,10) (1s) = 4.5s packed canvas.
	if packedEnd != 4_500_000 {
		t.Fatalf("packed end = %d, want 4_500_000", packedEnd)
	}
	want := map[string][2]int64{
		"a": {0, 2_000_000},
		"b": {1_500_000, 3_500_000},
		"c": {3_500_000, 4_500_000},
	}
	for id, window := range want {
		placement, ok := placements[id]
		if !ok {
			t.Fatalf("no placement for %q", id)
		}
		if placement.ArtifactInUS != window[0] || placement.ArtifactOutUS != window[1] {
			t.Fatalf("%q packed = [%d,%d), want [%d,%d)", id, placement.ArtifactInUS, placement.ArtifactOutUS, window[0], window[1])
		}
	}
}

// TestStampPackedPlacementsFailsClosed pins the stamping contract: every
// manifest entry receives a packed placement, and the stamped documentation
// still validates. A missing placement must error, never default to zero.
func TestStampPackedPlacementsFailsClosed(t *testing.T) {
	contents := map[string]string{
		"item-01": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"item-02": "123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0",
	}
	works := []manifestEntryWork{
		{Entry: capoverlay.OverlayManifestEntry{ItemID: "item-01", Track: 0, InUS: 10_000_000, OutUS: 12_000_000, ContentHash: contents["item-01"]}},
		{Entry: capoverlay.OverlayManifestEntry{ItemID: "item-02", Track: 0, InUS: 30_000_000, OutUS: 32_000_000, ContentHash: contents["item-02"]}},
	}
	err := stampPackedPlacements(works, map[string]packedPlacement{"item-01": {ArtifactInUS: 0, ArtifactOutUS: 2_000_000}})
	if err == nil {
		t.Fatal("a missing placement must fail closed")
	}
	if err := stampPackedPlacements(works, map[string]packedPlacement{
		"item-01": {ArtifactInUS: 0, ArtifactOutUS: 2_000_000},
		"item-02": {ArtifactInUS: 2_000_000, ArtifactOutUS: 4_000_000},
	}); err != nil {
		t.Fatalf("stampPackedPlacements: %v", err)
	}
	for i, work := range works {
		if err := work.Entry.Validate(); err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
	}
	if works[0].Entry.ArtifactInUS == nil || *works[0].Entry.ArtifactInUS != 0 || works[0].Entry.ArtifactOutUS == nil || *works[0].Entry.ArtifactOutUS != 2_000_000 {
		t.Fatalf("item-01 placement not stamped properly: %+v", works[0].Entry)
	}
	if works[1].Entry.ArtifactInUS == nil || *works[1].Entry.ArtifactInUS != 2_000_000 {
		t.Fatalf("item-02 placement not stamped properly: %+v", works[1].Entry)
	}
}

// TestPackedFusedOverlayBatchPlan pins the fused child plan on the packed
// canvas: items live at their packed positions, the child duration covers the
// packed end, and the item windows stay literal in relative terms.
func TestPackedFusedOverlayBatchPlan(t *testing.T) {
	plan := basePackedTestPlan()
	candidates := overlayRenderableCandidates(plan)
	placements, packedEnd, err := computePackedPlacements(plan, candidates)
	if err != nil {
		t.Fatalf("computePackedPlacements: %v", err)
	}
	work := overlayBatchWork{
		id:         "packed-work",
		sceneID:    "scene-01",
		startUS:    0,
		endUS:      overlayBatchWireEndUS(0, packedEnd),
		candidates: candidates,
		packed:     placements,
	}
	child, err := fusedOverlayBatchPlan(plan, work)
	if err != nil {
		t.Fatalf("fusedOverlayBatchPlan: %v", err)
	}
	if child.DurationMS != 6000 {
		t.Fatalf("child duration ms = %d, want 6000", child.DurationMS)
	}
	starts := map[string]int64{}
	for _, item := range child.Items {
		starts[item.ID] = item.StartUS
	}
	for id, want := range map[string]int64{"item-01": 0, "item-02": 2_000_000, "item-03": 4_000_000} {
		if starts[id] != want {
			t.Fatalf("item %q packed start = %d, want %d", id, starts[id], want)
		}
	}
}

// TestPackedFusedOverlayBatchPlanFailsWithoutPlacement pins the fail-closed
// guard: a packed work with missing placements must error, never fall back to
// an absolute position.
func TestPackedFusedOverlayBatchPlanFailsWithoutPlacement(t *testing.T) {
	plan := basePackedTestPlan()
	candidates := overlayRenderableCandidates(plan)
	work := overlayBatchWork{
		id:         "packed-work-missing",
		sceneID:    "scene-01",
		startUS:    0,
		endUS:      6_000_000,
		candidates: candidates,
		packed:     map[string]packedPlacement{"item-01": {ArtifactInUS: 0, ArtifactOutUS: 2_000_000}},
	}
	if _, err := fusedOverlayBatchPlan(plan, work); err == nil {
		t.Fatal("a packed work with a missing placement must fail closed")
	}
}

// TestPackedManifestEntryRejectsBadArtifactWindows pins the model-side
// validation of the packed fields: the pair must be set together, ordered,
// and never exceed the entry's literal window.
func TestPackedManifestEntryRejectsBadArtifactWindows(t *testing.T) {
	base := capoverlay.OverlayManifestEntry{
		ItemID: "item-01", Track: 0, InUS: 10_000_000, OutUS: 12_000_000,
		ContentHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	onlyIn := func() capoverlay.OverlayManifestEntry {
		entry := base
		in := int64(0)
		entry.ArtifactInUS = &in
		return entry
	}
	if onlyIn().Validate() == nil {
		t.Fatal("artifact_in_us without artifact_out_us must be rejected")
	}
	negative := func() capoverlay.OverlayManifestEntry {
		entry := base
		in, out := int64(-1), int64(1_000_000)
		entry.ArtifactInUS, entry.ArtifactOutUS = &in, &out
		return entry
	}
	if negative().Validate() == nil {
		t.Fatal("negative artifact_in_us must be rejected")
	}
	inverted := func() capoverlay.OverlayManifestEntry {
		entry := base
		in, out := int64(2_000_000), int64(0)
		entry.ArtifactInUS, entry.ArtifactOutUS = &in, &out
		return entry
	}
	if inverted().Validate() == nil {
		t.Fatal("artifact_out_us must be greater than artifact_in_us")
	}
	tooLong := func() capoverlay.OverlayManifestEntry {
		entry := base
		in, out := int64(0), int64(3_000_000)
		entry.ArtifactInUS, entry.ArtifactOutUS = &in, &out
		return entry
	}
	if tooLong().Validate() == nil {
		t.Fatal("a packed window longer than the entry window must be rejected")
	}
	valid := func() capoverlay.OverlayManifestEntry {
		entry := base
		in, out := int64(0), int64(2_000_000)
		entry.ArtifactInUS, entry.ArtifactOutUS = &in, &out
		return entry
	}
	if valid().Validate() != nil {
		t.Fatal("a valid packed window at offset zero with a 2s literal window must be accepted")
	}
}
