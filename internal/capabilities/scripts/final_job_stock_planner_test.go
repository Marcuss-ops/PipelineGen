package scriptgeneration

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

// countingStockResolver models the adapter: it caches by identity (so a second
// ask is free) and counts how many identities it actually resolved. The
// distinction matters because the walk always ASKS for every source, while a
// cached metadata reference answers without paying again.
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

// TestFinalJobStockPlannerConsumesWholeFilesAndRotatesAcrossScenes pins the
// whole-file rule, shuffled order, and cursor continuity. The old walk took a
// partial tail from the last file; the remote Master rejects that (r56).
func TestFinalJobStockPlannerConsumesWholeFilesAndRotatesAcrossScenes(t *testing.T) {
	resolver := newCountingStockResolver(
		[]FinalJobStockFile{
			{ID: "stock-a", Name: "a.mp4"},
			{ID: "stock-b", Name: "b.mp4"},
			{ID: "stock-c", Name: "c.mp4"},
			{ID: "stock-d", Name: "d.mp4"},
			{ID: "stock-e", Name: "e.mp4"},
		},
		map[string]int64{"stock-a": 5_000, "stock-b": 5_000, "stock-c": 5_000, "stock-d": 5_000, "stock-e": 5_000},
	)
	planner := newFinalJobStockPlanner("run-whole-file-order")
	scene0, err := planner.sceneClips(context.Background(), resolver, "folder-1", 20_000_000)
	if err != nil {
		t.Fatalf("scene 0: %v", err)
	}
	ordered := planner.files["folder-1"]
	if len(scene0) != 4 {
		t.Fatalf("scene 0 chunks = %+v, want 4", scene0)
	}
	for i, clip := range scene0 {
		if clip.Asset["drive_file_id"] != ordered[i].ID || clip.ChunkMS != 5_000 {
			t.Fatalf("scene 0 chunk %d = %v/%d, want shuffled file %s/5000", i, clip.Asset["drive_file_id"], clip.ChunkMS, ordered[i].ID)
		}
	}
	// The cursor records the file after the last claim, so the next scene starts
	// at the next entry in this run's shuffled pool order.
	scene1, err := planner.sceneClips(context.Background(), resolver, "folder-1", 5_000_000)
	if err != nil {
		t.Fatalf("scene 1: %v", err)
	}
	if len(scene1) != 1 || scene1[0].Asset["drive_file_id"] != ordered[4].ID || scene1[0].ChunkMS != 5_000 {
		t.Fatalf("scene 1 chunks = %+v, want next shuffled file %s/5000", scene1, ordered[4].ID)
	}
	for _, clip := range append(scene0, scene1...) {
		id := clip.Asset["drive_file_id"].(string)
		if clip.ChunkMS != resolver.dur[id] {
			t.Fatalf("clip %s uses %dms of a %dms file; partial stock windows are forbidden", id, clip.ChunkMS, resolver.dur[id])
		}
	}
	if resolver.listed != 1 {
		t.Fatalf("folder listed %d times, want once per handoff", resolver.listed)
	}
	if resolver.resolved() != 5 {
		t.Fatalf("resolved %d stock files, want the 5 the folder offers", resolver.resolved())
	}
}

func TestFinalJobStockPlannerShufflesPerRunAndIsRepeatable(t *testing.T) {
	files := make([]FinalJobStockFile, 100)
	durations := make(map[string]int64, len(files))
	inputOrder := make([]string, len(files))
	for i := range files {
		id := fmt.Sprintf("stock-%03d", i)
		files[i] = FinalJobStockFile{ID: id, Name: id + ".mp4"}
		durations[id] = 1_000
		inputOrder[i] = id
	}
	selectIDs := func(runID string) []string {
		t.Helper()
		resolver := newCountingStockResolver(files, durations)
		clips, err := newFinalJobStockPlanner(runID).sceneClips(context.Background(), resolver, "folder-100", 100_000_000)
		if err != nil {
			t.Fatalf("select stock for %s: %v", runID, err)
		}
		if len(clips) != len(files) {
			t.Fatalf("run %s selected %d clips, want %d", runID, len(clips), len(files))
		}
		selected := make([]string, len(clips))
		for i, clip := range clips {
			selected[i] = clip.Asset["drive_file_id"].(string)
		}
		return selected
	}
	first := selectIDs("run-one")
	if second := selectIDs("run-one"); !slices.Equal(first, second) {
		t.Fatal("same run produced a different stock order; retries must be stable")
	}
	if slices.Equal(first, inputOrder) {
		t.Fatal("stock selection retained Drive's sequential listing order")
	}
	if third := selectIDs("run-two"); slices.Equal(first, third) {
		t.Fatal("different runs produced the same stock order")
	}
}

// TestFinalJobStockPlannerRejectsAnUnfillableScene pins the fail-fast side: a
// folder whose whole files cannot sum to the scene duration must stop the
// handoff, not emit a partial tail for the Master to reject.
func TestFinalJobStockPlannerRejectsAnUnfillableScene(t *testing.T) {
	resolver := newCountingStockResolver(
		[]FinalJobStockFile{{ID: "stock-a", Name: "a.mp4"}, {ID: "stock-b", Name: "b.mp4"}, {ID: "stock-c", Name: "c.mp4"}},
		map[string]int64{"stock-a": 12_000, "stock-b": 4_000, "stock-c": 6_000},
	)
	_, err := newFinalJobStockPlanner("run-1").sceneClips(context.Background(), resolver, "folder-1", 25_000_000)
	if err == nil || !strings.Contains(err.Error(), "whole files") {
		t.Fatalf("sceneClips error = %v, want a whole-file composition failure", err)
	}
}

// TestFinalJobStockPlannerKeepsTheFormerGOPRejectedSource ensures a stock
// source that previously failed a GOP probe remains selectable as a WHOLE
// file: the remote Master downloads and packet-copies it itself. The Producer
// neither probes nor re-encodes it.
func TestFinalJobStockPlannerKeepsTheFormerGOPRejectedSource(t *testing.T) {
	const formerRejected = "1xvvjxin09xbwy7qqignzlmpjftckql_o"
	resolver := newCountingStockResolver(
		[]FinalJobStockFile{{ID: formerRejected, Name: "stock.mp4"}},
		map[string]int64{formerRejected: 5_000},
	)
	clips, err := newFinalJobStockPlanner("run-1").sceneClips(context.Background(), resolver, "folder-1", 5_000_000)
	if err != nil {
		t.Fatalf("whole-file stock source should remain selectable for the remote Master: %v", err)
	}
	if len(clips) != 1 || clips[0].Asset["drive_file_id"] != formerRejected || clips[0].ChunkMS != 5_000 {
		t.Fatalf("clips = %+v, want the former GOP-rejected whole stock source", clips)
	}
}
