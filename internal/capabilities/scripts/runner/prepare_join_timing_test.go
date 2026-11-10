package runner

import (
	"testing"
	"time"
)

func TestBuildPrepareJoinBreakdownAttributesLastBranch(t *testing.T) {
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	joinStart := start.Add(2 * time.Second)
	semanticDone := start.Add(5 * time.Second)
	assetsDone := start.Add(8 * time.Second)
	joinedAt := start.Add(8*time.Second + 250*time.Millisecond)

	got := BuildPrepareJoinBreakdown(start, joinStart, joinedAt, semanticDone, assetsDone)
	if got == nil {
		t.Fatal("expected a breakdown")
	}
	if got.WallMS != 6250 {
		t.Fatalf("WallMS = %d, want 6250", got.WallMS)
	}
	if got.SemanticMS != 5000 || got.AssetsMS != 8000 {
		t.Fatalf("branch totals = semantic %d, assets %d; want 5000/8000", got.SemanticMS, got.AssetsMS)
	}
	if got.SemanticWaitMS != 3000 || got.AssetsWaitMS != 6000 {
		t.Fatalf("waits = semantic %d, assets %d; want 3000/6000", got.SemanticWaitMS, got.AssetsWaitMS)
	}
	if got.LastBranch != "assets" {
		t.Fatalf("LastBranch = %q, want assets", got.LastBranch)
	}
}

func TestBuildPrepareJoinBreakdownClampsAlreadyFinishedBranches(t *testing.T) {
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	semanticDone := start.Add(5 * time.Second)
	assetsDone := start.Add(6 * time.Second)
	joinStart := start.Add(8 * time.Second)
	joinedAt := start.Add(8*time.Second + 100*time.Millisecond)

	got := BuildPrepareJoinBreakdown(start, joinStart, joinedAt, semanticDone, assetsDone)
	if got == nil {
		t.Fatal("expected a breakdown")
	}
	if got.WallMS != 100 {
		t.Fatalf("WallMS = %d, want 100", got.WallMS)
	}
	if got.SemanticWaitMS != 0 || got.AssetsWaitMS != 0 {
		t.Fatalf("waits = %d/%d, want 0/0", got.SemanticWaitMS, got.AssetsWaitMS)
	}
	if got.LastBranch != "assets" {
		t.Fatalf("LastBranch = %q, want assets", got.LastBranch)
	}
}

func TestBuildPrepareJoinBreakdownRequiresAllTimestamps(t *testing.T) {
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	if got := BuildPrepareJoinBreakdown(start, start, start, time.Time{}, start); got != nil {
		t.Fatalf("expected nil with a missing timestamp, got %+v", got)
	}
}
