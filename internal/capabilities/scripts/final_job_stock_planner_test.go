package scriptgeneration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	capabilityaudio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// countingStockResolver models the adapter: it caches by identity (so a second
// ask is free) and counts how many identities it actually resolved. The
// distinction is the whole point of the prewarm — the walk always ASKS for
// every source, and only the resolver can answer without paying.
type countingStockResolver struct {
	folder  []FinalJobStockFile
	dur     map[string]int64
	mu      sync.Mutex
	cache   map[string]map[string]any
	resolve int
	listed  int
	ids     []string
}

func newCountingStockResolver(folder []FinalJobStockFile, durations map[string]int64) *countingStockResolver {
	return &countingStockResolver{folder: folder, dur: durations, cache: make(map[string]map[string]any)}
}

func (r *countingStockResolver) ListFinalJobStockFolder(context.Context, string) ([]FinalJobStockFile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listed++
	return r.folder, nil
}

func (r *countingStockResolver) ResolveFinalJobAsset(context.Context, string) (map[string]any, error) {
	return nil, fmt.Errorf("stock fixtures must only be resolved through the stock path")
}

func (r *countingStockResolver) FinalJobPublishedFileSize(context.Context, string) (int64, error) {
	return 0, nil
}

func (r *countingStockResolver) ResolveFinalJobStockAsset(_ context.Context, id string) (map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if asset, ok := r.cache[id]; ok {
		return asset, nil
	}
	duration, ok := r.dur[id]
	if !ok {
		return nil, fmt.Errorf("unknown stock file %s", id)
	}
	asset := map[string]any{
		"asset_id":      id,
		"drive_file_id": id,
		"url":           "https://objectstore.invalid/" + id + ".mp4",
		"sha256":        strings.Repeat("b", 64),
		"size_bytes":    int64(1024),
		"duration_ms":   duration,
	}
	r.cache[id] = asset
	r.ids = append(r.ids, id)
	r.resolve++
	return asset, nil
}

func (r *countingStockResolver) resolved() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolve
}

// TestFinalJobStockPlannerKeepsChunkingAndFolderRotationAcrossScenes pins the
// selection rule the payload used to own inline: a scene consumes chunks from a
// folder that ROTATES across scenes, and the file after the one that filled the
// scene is the next scene's first file.
func TestFinalJobStockPlannerKeepsChunkingAndFolderRotationAcrossScenes(t *testing.T) {
	resolver := newCountingStockResolver(
		[]FinalJobStockFile{{ID: "stock-a", Name: "a.mp4"}, {ID: "stock-b", Name: "b.mp4"}, {ID: "stock-c", Name: "c.mp4"}},
		map[string]int64{"stock-a": 12_000, "stock-b": 4_000, "stock-c": 6_000},
	)
	planner := newFinalJobStockPlanner()
	scene0, err := planner.sceneClips(context.Background(), resolver, "folder-1", 25_000_000)
	if err != nil {
		t.Fatalf("scene 0: %v", err)
	}
	want0 := []struct {
		id      string
		chunkMS int64
	}{{"stock-a", 12_000}, {"stock-c", 6_000}, {"stock-b", 4_000}, {"stock-a", 3_000}}
	if len(scene0) != len(want0) {
		t.Fatalf("scene 0 chunks = %+v, want %d", scene0, len(want0))
	}
	for i, want := range want0 {
		if scene0[i].Asset["drive_file_id"] != want.id || scene0[i].ChunkMS != want.chunkMS {
			t.Fatalf("scene 0 chunk %d = %v/%d, want %s/%d", i, scene0[i].Asset["drive_file_id"], scene0[i].ChunkMS, want.id, want.chunkMS)
		}
	}
	// The cursor records the post-claim index of the LAST chunk, so the
	// rotation step after it is only persisted when another chunk follows.
	// Four chunks therefore leave the cursor at 7: the next scene starts at
	// the second file and finishes on the first.
	scene1, err := planner.sceneClips(context.Background(), resolver, "folder-1", 5_000_000)
	if err != nil {
		t.Fatalf("scene 1: %v", err)
	}
	if len(scene1) != 2 || scene1[0].Asset["drive_file_id"] != "stock-b" || scene1[0].ChunkMS != 4_000 ||
		scene1[1].Asset["drive_file_id"] != "stock-a" || scene1[1].ChunkMS != 1_000 {
		t.Fatalf("scene 1 chunks = %+v, want stock-b/4000 then stock-a/1000", scene1)
	}
	// One folder listing and one resolution per file: the planner caches both.
	if resolver.listed != 1 {
		t.Fatalf("folder listed %d times, want once per handoff", resolver.listed)
	}
	if resolver.resolved() != 3 {
		t.Fatalf("resolved %d stock files, want the 3 the folder offers", resolver.resolved())
	}
}

// TestFinalJobStockPlannerKeepsTheFormerGOPRejectedSource ensures a stock
// source that cannot be packet-copied remains available to the local preparer,
// which re-encodes and certifies the exact requested window before handoff.
func TestFinalJobStockPlannerKeepsTheFormerGOPRejectedSource(t *testing.T) {
	const formerRejected = "1xvvjxin09xbwy7qqignzlmpjftckql_o"
	resolver := newCountingStockResolver(
		[]FinalJobStockFile{{ID: formerRejected, Name: "stock.mp4"}},
		map[string]int64{formerRejected: 5_000},
	)
	clips, err := newFinalJobStockPlanner().sceneClips(context.Background(), resolver, "folder-1", 5_000_000)
	if err != nil {
		t.Fatalf("GOP-incompatible source should reach the certified preparer: %v", err)
	}
	if len(clips) != 1 || clips[0].Asset["drive_file_id"] != formerRejected {
		t.Fatalf("clips = %+v, want the former GOP-rejected stock source", clips)
	}
}

// TestPrewarmFinalJobStockSourcesLeavesNothingForTheHandoffToResolve is the
// acceptance criterion of the prewarm: the walk during the overlay render has
// already paid for every Drive source, so the handoff resolves zero of them.
func TestPrewarmFinalJobStockSourcesLeavesNothingForTheHandoffToResolve(t *testing.T) {
	resolver := newCountingStockResolver(
		[]FinalJobStockFile{{ID: "stock-a", Name: "a.mp4"}},
		map[string]int64{"stock-a": 2_000},
	)
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{
			DurationUS: 2_000_000,
			Segments:   []capabilityaudio.TimelineSegment{{ID: "scene-1", DurationUS: 2_000_000}},
		},
		FinalAudio: certifiedFinalAudio(2000),
		Scenes:     []Scene{{ID: "scene-1", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}}},
	}
	planned, err := PrewarmFinalJobStockSources(context.Background(), result, resolver)
	if err != nil {
		t.Fatalf("prewarm: %v", err)
	}
	if planned != 1 {
		t.Fatalf("prewarm planned %d windows, want 1", planned)
	}
	if got := resolver.resolved(); got != 1 {
		t.Fatalf("prewarm resolved %d sources, want 1", got)
	}
	if _, _, err := BuildFinalJobPayloads(context.Background(), "run-1", GenerateRequest{SourceLanguage: "en"}, result, resolver); err != nil {
		t.Fatalf("payload build after prewarm: %v", err)
	}
	if got := resolver.resolved(); got != 1 {
		t.Fatalf("handoff resolved %d sources after the prewarm, want 0 more (total 1)", got-1)
	}
	if resolver.listed != 2 {
		// One listing per walk: the prewarm's planner and the handoff's planner
		// are distinct, and only the RESOLVED identities are shared.
		t.Fatalf("folder listed %d times, want one listing per walk", resolver.listed)
	}
}

// TestPrewarmFinalJobStockSourcesSkipsScenesWithoutAStockFolder keeps the walk
// off the clip-only and fixed-media paths: they never touch a folder, so asking
// for one would invent a selection the handoff never makes.
func TestPrewarmFinalJobStockSourcesSkipsScenesWithoutAStockFolder(t *testing.T) {
	resolver := newCountingStockResolver([]FinalJobStockFile{{ID: "stock-a", Name: "a.mp4"}}, map[string]int64{"stock-a": 2_000})
	result := &GenerateResult{
		CanonicalTimeline: &capabilityaudio.CanonicalTimeline{DurationUS: 4_000_000, Segments: []capabilityaudio.TimelineSegment{
			{ID: "scene-clip", DurationUS: 2_000_000},
			{ID: "scene-fixed", DurationUS: 2_000_000, FixedMedia: true},
		}},
		Scenes: []Scene{
			{ID: "scene-clip"},
			{ID: "scene-fixed", Stock: &scriptpkg.StockBinding{FolderID: "folder-1"}},
		},
	}
	if planned, err := PrewarmFinalJobStockSources(context.Background(), result, resolver); err != nil || planned != 0 {
		t.Fatalf("prewarm planned %d windows (err=%v), want none without a stock folder", planned, err)
	}
	if resolver.listed != 0 || resolver.resolved() != 0 {
		t.Fatalf("prewarm touched a folder for a scene that has none (listed=%d resolved=%d)", resolver.listed, resolver.resolved())
	}
}
