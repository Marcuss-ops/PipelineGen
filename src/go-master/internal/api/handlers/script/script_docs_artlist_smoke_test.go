package script

import (
	"path/filepath"
	"runtime"
	"os"
	"testing"
)

func TestMatchArtlistForSegment_ReturnsSingleLinkFromLocalDb(t *testing.T) {
	nodeScraperDir := findNodeScraperDir(t)
	seg := TimelineSegment{
		OpeningSentence: "The world of boxing was brutal and fast.",
		ClosingSentence: "The crowd watched an unforgettable fight.",
		Keywords:        []string{"boxing", "fight"},
		Entities:        []string{"Mike Tyson"},
	}

	matches := matchArtlistForSegment(nil, nil, seg, nil, nodeScraperDir)
	if len(matches) != 1 {
		t.Fatalf("expected exactly 1 artlist match, got %d", len(matches))
	}
	if matches[0].Link == "" {
		t.Fatalf("expected non-empty artlist link")
	}
}

func TestMatchArtlistForSegment_Elephant(t *testing.T) {
	nodeScraperDir := findNodeScraperDir(t)
	seg := TimelineSegment{
		OpeningSentence: "An elephant walks through the savannah.",
		ClosingSentence: "The animal stands beside the river.",
		Keywords:        []string{"elephant", "animal", "wildlife"},
		Entities:        []string{"elephant"},
	}

	matches := matchArtlistForSegment(nil, nil, seg, nil, nodeScraperDir)
	t.Logf("artlist matches for elephant: %+v", matches)
	if len(matches) > 1 {
		t.Fatalf("expected at most 1 artlist match, got %d", len(matches))
	}
}

func findNodeScraperDir(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}

	dir := filepath.Dir(file)
	for i := 0; i < 5; i++ {
		dir = filepath.Dir(dir)
	}

	nodeScraperDir := filepath.Join(dir, "node-scraper")
	if _, err := os.Stat(nodeScraperDir); err != nil {
		t.Fatalf("node scraper dir not available: %v", err)
	}
	return nodeScraperDir
}
