package adapters

import (
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

func TestArtlistMatchesToCandidatesExpandsEveryClip(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "segment-001"}
	matches := []ArtlistClipMatch{{
		Phrase:         "roman ruins excavation",
		FolderLink:     "https://drive.example/folder",
		ClipNames:      []string{"clip one", "clip two", "clip three"},
		ClipDriveLinks: []string{"https://drive.example/1", "https://drive.example/2", "https://drive.example/3"},
	}}

	got := artlistMatchesToCandidates(segment, matches)
	if len(got) != 3 {
		t.Fatalf("expected every Artlist clip to become a candidate, got %d", len(got))
	}
	for i, candidate := range got {
		if candidate.Provider != "artlist" {
			t.Fatalf("candidate %d provider = %q", i, candidate.Provider)
		}
		if candidate.DriveLink != matches[0].ClipDriveLinks[i] {
			t.Fatalf("candidate %d drive link = %q, want %q", i, candidate.DriveLink, matches[0].ClipDriveLinks[i])
		}
		if candidate.Entity != matches[0].ClipNames[i] {
			t.Fatalf("candidate %d entity = %q, want %q", i, candidate.Entity, matches[0].ClipNames[i])
		}
	}
}

func TestArtlistMatchesToCandidatesDeduplicatesDriveLinks(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "segment-001"}
	matches := []ArtlistClipMatch{
		{Phrase: "query one", ClipNames: []string{"same"}, ClipDriveLinks: []string{"https://drive.example/1"}},
		{Phrase: "query two", ClipNames: []string{"same duplicate"}, ClipDriveLinks: []string{"https://drive.example/1"}},
	}

	got := artlistMatchesToCandidates(segment, matches)
	if len(got) != 1 {
		t.Fatalf("expected duplicate Drive links to collapse, got %d", len(got))
	}
}
