package scriptgeneration

import (
	"context"
	"fmt"
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
// whole-file rule: a scene is filled by whole sources that sum exactly to its
// canonical duration, and the cursor continues through the folder. The old
// walk took a partial tail from the last file; the remote Master rejects that
// (r56), so the planner must never emit it.
func TestFinalJobStockPlannerConsumesWholeFilesAndRotatesAcrossScenes(t *testing.T) {
	resolver := newCountingStockResolver(
		[]FinalJobStockFile{
			{ID: "stock-a", Name: "a.mp4"},
			{ID: "stock-b", Name: "b.mp4"},
			{ID: "stock-c", Name: "c.mp4"},
			{ID: "stock-d", Name: "d.mp4"},
			{ID: "stock-e", Name: "e.mp4"},
		},
		map[string]int64{"stock-a": 12_000, "stock-b": 4_000, "stock-c": 6_000, "stock-d": 3_000, "stock-e": 5_000},
	)
	planner := newFinalJobStockPlanner()
	scene0, err := planner.sceneClips(context.Background(), resolver, "folder-1", 25_000_000)
	if err != nil {
		t.Fatalf("scene 0: %v", err)
	}
	want0 := []struct {
		id      string
		chunkMS int64
	}{{"stock-a", 12_000}, {"stock-b", 4_000}, {"stock-c", 6_000}, {"stock-d", 3_000}}
	if len(scene0) != len(want0) {
		t.Fatalf("scene 0 chunks = %+v, want %d", scene0, len(want0))
	}
	for i, want := range want0 {
		if scene0[i].Asset["drive_file_id"] != want.id || scene0[i].ChunkMS != want.chunkMS {
			t.Fatalf("scene 0 chunk %d = %v/%d, want %s/%d", i, scene0[i].Asset["drive_file_id"], scene0[i].ChunkMS, want.id, want.chunkMS)
		}
	}
	// The cursor records the file AFTER the last claimed chunk, so the next
	// scene starts at stock-e and takes its exact 5s whole file.
	scene1, err := planner.sceneClips(context.Background(), resolver, "folder-1", 5_000_000)
	if err != nil {
		t.Fatalf("scene 1: %v", err)
	}
	if len(scene1) != 1 || scene1[0].Asset["drive_file_id"] != "stock-e" || scene1[0].ChunkMS != 5_000 {
		t.Fatalf("scene 1 chunks = %+v, want stock-e/5000", scene1)
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

// TestFinalJobStockPlannerRejectsAnUnfillableScene pins the fail-fast side: a
// folder whose whole files cannot sum to the scene duration must stop the
// handoff, not emit a partial tail for the Master to reject.
func TestFinalJobStockPlannerRejectsAnUnfillableScene(t *testing.T) {
	resolver := newCountingStockResolver(
		[]FinalJobStockFile{{ID: "stock-a", Name: "a.mp4"}, {ID: "stock-b", Name: "b.mp4"}, {ID: "stock-c", Name: "c.mp4"}},
		map[string]int64{"stock-a": 12_000, "stock-b": 4_000, "stock-c": 6_000},
	)
	_, err := newFinalJobStockPlanner().sceneClips(context.Background(), resolver, "folder-1", 25_000_000)
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
	clips, err := newFinalJobStockPlanner().sceneClips(context.Background(), resolver, "folder-1", 5_000_000)
	if err != nil {
		t.Fatalf("whole-file stock source should remain selectable for the remote Master: %v", err)
	}
	if len(clips) != 1 || clips[0].Asset["drive_file_id"] != formerRejected || clips[0].ChunkMS != 5_000 {
		t.Fatalf("clips = %+v, want the former GOP-rejected whole stock source", clips)
	}
}
