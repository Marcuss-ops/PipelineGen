package adapters

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
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

type emptyArtlistSearcher struct {
	calls int
}

func (s *emptyArtlistSearcher) SearchClips(context.Context, string, []string) []ArtlistClipMatch {
	s.calls++
	return nil
}

func TestClipSearchProcessorDoesNotCacheProviderMisses(t *testing.T) {
	searcher := &emptyArtlistSearcher{}
	processor := NewClipSearchProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{Artlist: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "negative-cache-artlist-test",
		TextHash:  "negative-cache-artlist-hash",
		Insights: scriptpkg.SegmentInsights{
			ArtlistQueries: []string{"no result query"},
		},
	}}}

	for i := 0; i < 2; i++ {
		if _, err := processor.Process(context.Background(), plan, input); err != nil {
			t.Fatalf("process call %d failed: %v", i+1, err)
		}
	}
	if searcher.calls != 2 {
		t.Fatalf("expected provider to be retried after an empty result, calls = %d", searcher.calls)
	}
}
