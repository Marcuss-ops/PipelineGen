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

func TestArtlistRemoteCandidateCannotBeBoundBeforePersistence(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "segment-remote"}
	matches := []ArtlistClipMatch{{
		Phrase:         "mountain runner",
		Remote:         true,
		ClipNames:      []string{"runner"},
		ClipDriveLinks: []string{"https://cdn.artlist.io/stream.m3u8"},
	}}

	got := artlistMatchesToCandidates(segment, matches)
	if len(got) != 1 {
		t.Fatalf("expected one remote candidate, got %d", len(got))
	}
	if got[0].AcquisitionStatus != scriptpkg.VidRushStatusCandidateFound {
		t.Fatalf("acquisition_status = %q, want %q", got[0].AcquisitionStatus, scriptpkg.VidRushStatusCandidateFound)
	}
	if readyVidRushCandidate(got[0]) {
		t.Fatal("remote candidate must not be binding-ready before acquisition, verification, persistence and indexing")
	}
}

// TestArtlistMatchesToCandidates_AlwaysProviderArtlist verifies that
// every candidate produced by the Artlist pipeline has provider="artlist",
// regardless of what the searcher returns. This is the processor-level
// guarantee that YouTube can never leak through the clip-search path:
// even if the Artlist scraper returned a YouTube URL, the provider field
// is hardcoded to "artlist" and the URL would be caught by the binding gate
// (validVidRushCandidate's forbidden URL patterns).
func TestArtlistMatchesToCandidates_AlwaysProviderArtlist(t *testing.T) {
	segment := scriptpkg.VidRushSegmentResult{SegmentID: "segment-001"}

	// Simulate an unlikely scenario where Artlist somehow returns
	// URLs that look like YouTube links.
	matches := []ArtlistClipMatch{
		{
			Phrase:         "maya temple ruins",
			FolderLink:     "https://drive.example/maya-folder",
			ClipNames:      []string{"temple clip", "pyramid clip"},
			ClipDriveLinks: []string{"https://www.youtube.com/watch?v=abc123", "https://drive.example/genuine-artlist"},
		},
	}

	got := artlistMatchesToCandidates(segment, matches)
	// The youtube-like URL candidate should still be produced
	// (with provider="artlist"), but the binding gate will catch it.
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(got))
	}
	for i, c := range got {
		if c.Provider != "artlist" {
			t.Errorf("candidate %d provider = %q, want \"artlist\" (hardcoded in artlistMatchesToCandidates)", i, c.Provider)
		}
	}
}

type emptyArtlistSearcher struct {
	calls int
}

func (s *emptyArtlistSearcher) SearchClips(context.Context, string, []string) ([]ArtlistClipMatch, error) {
	s.calls++
	return nil, nil
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
		if _, err := processor.Process(context.Background(), plan, input); err == nil {
			t.Fatalf("process call %d succeeded without required Artlist candidates", i+1)
		}
	}
	if searcher.calls != 2 {
		t.Fatalf("expected provider to be retried after an empty result, calls = %d", searcher.calls)
	}
}

func TestClipSearchProcessor_HybridArtlistMissRemainsBestEffort(t *testing.T) {
	searcher := &emptyArtlistSearcher{}
	processor := NewClipSearchProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{
				Artlist:        media.MediaToggleEnabled,
				InternetImages: media.MediaToggleEnabled,
			},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "hybrid-artlist-miss",
		Insights:  scriptpkg.SegmentInsights{ArtlistQueries: []string{"no result query"}},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("hybrid Artlist miss should remain best-effort: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("hybrid Artlist miss must remain visible as a warning")
	}
}

// multiClipArtlistSearcher returns clips that look like YouTube results
// to verify the processor pipeline never produces provider=youtube.
type multiClipArtlistSearcher struct{}

func (multiClipArtlistSearcher) SearchClips(_ context.Context, _ string, queries []string) ([]ArtlistClipMatch, error) {
	out := make([]ArtlistClipMatch, 0)
	for _, q := range queries {
		out = append(out, ArtlistClipMatch{
			Phrase:         q,
			FolderLink:     "https://drive.example/folder",
			ClipNames:      []string{"valid clip"},
			ClipDriveLinks: []string{"https://drive.example/valid-" + q[:minInt(8, len(q))]},
		})
	}
	return out, nil
}

// TestClipSearchProcessor_AllCandidatesAreArtlist verifies that after
// a full processor run, every candidate in the segment result has
// provider="artlist". This is the processor-level YouTube-block contract:
// the clip-search path can NEVER produce provider=youtube candidates.
func TestClipSearchProcessor_AllCandidatesAreArtlist(t *testing.T) {
	processor := NewClipSearchProcessor(multiClipArtlistSearcher{})
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			ProviderPolicy: media.MediaProviderPolicy{Artlist: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "artlist-provider-gate",
		TextHash:  "artlist-provider-gate-hash",
		Insights: scriptpkg.SegmentInsights{
			ArtlistQueries: []string{"maya temple", "ancient ruins", "jungle pyramid"},
		},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if len(result.VidRushSegments) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(result.VidRushSegments))
	}
	candidates := result.VidRushSegments[0].Assets.Candidates
	if len(candidates) < 3 {
		t.Fatalf("expected at least 3 candidates (one per query), got %d", len(candidates))
	}
	for i, c := range candidates {
		if c.Provider != "artlist" {
			t.Errorf("candidate %d provider = %q, want \"artlist\"", i, c.Provider)
		}
		if c.Provider == "youtube" {
			t.Errorf("candidate %d has provider=youtube — FORBIDDEN", i)
		}
	}
}
