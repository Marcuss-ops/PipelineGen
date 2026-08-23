package adapters

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/stretchr/testify/require"
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

type recordingArtlistSearcher struct {
	queries []string
}

type countingArtlistSearcher struct{ calls int }

func (s *countingArtlistSearcher) SearchClips(_ context.Context, _ string, queries []string) ([]ArtlistClipMatch, error) {
	s.calls++
	return []ArtlistClipMatch{{Phrase: queries[0], ClipNames: []string{"cached-clip"}, ClipDriveLinks: []string{"https://cdn.artlist.io/cached.m3u8"}, Remote: true}}, nil
}

func TestClipSearchProcessorReusesWarmArtlistSegmentCache(t *testing.T) {
	vidrushArtlistCache = sync.Map{}
	searcher := &countingArtlistSearcher{}
	processor := NewClipSearchProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "Top 10 foods", Language: "en", MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{Artlist: media.MediaToggleEnabled},
	}}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "food-1", TextHash: "food-hash", Insights: scriptpkg.SegmentInsights{ArtlistQueries: []string{"bread"}},
	}}}
	first, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := processor.Process(context.Background(), plan, input)
	if err != nil {
		t.Fatal(err)
	}
	if searcher.calls != 1 || first.VidRushSegments[0].Cache.Artlist != "MISS" || second.VidRushSegments[0].Cache.Artlist != "HIT_EXACT" {
		t.Fatalf("calls=%d first=%q second=%q, want 1/MISS/HIT_EXACT", searcher.calls, first.VidRushSegments[0].Cache.Artlist, second.VidRushSegments[0].Cache.Artlist)
	}
}

func TestClipSearchProcessorColdWarmAndIntentInvalidation(t *testing.T) {
	vidrushArtlistCache = sync.Map{}
	searcher := &countingArtlistSearcher{}
	processor := NewClipSearchProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "Top 10 foods", Language: "en", MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{Artlist: media.MediaToggleEnabled},
	}}
	makeInput := func(changed bool) ProcessInput {
		keywords := []string{"bread", "wine", "olive oil", "cheese", "fish"}
		segments := make([]scriptpkg.VidRushSegmentResult, 0, len(keywords))
		for i, keyword := range keywords {
			if changed && i == 0 {
				keyword = "sourdough bread"
			}
			segments = append(segments, scriptpkg.VidRushSegmentResult{
				SegmentID: fmt.Sprintf("food-%d", i),
				TextHash:  fmt.Sprintf("text-%d", i),
				Insights:  scriptpkg.SegmentInsights{ArtlistQueries: []string{keyword}, ArtlistIntentHash: scriptpkg.ArtlistSearchIntentHash([]string{keyword})},
			})
		}
		return ProcessInput{VidRushSegments: segments}
	}
	coldStart := time.Now()
	cold, err := processor.Process(context.Background(), plan, makeInput(false))
	coldWall := time.Since(coldStart)
	if err != nil || len(cold.VidRushSegments) != 5 || searcher.calls != 5 {
		t.Fatalf("cold: err=%v segments=%d calls=%d", err, len(cold.VidRushSegments), searcher.calls)
	}
	warmStart := time.Now()
	warm, err := processor.Process(context.Background(), plan, makeInput(false))
	warmWall := time.Since(warmStart)
	if err != nil || searcher.calls != 5 {
		t.Fatalf("warm: err=%v calls=%d, want zero additional provider calls", err, searcher.calls)
	}
	for _, segment := range warm.VidRushSegments {
		if segment.Cache.Artlist != "HIT_EXACT" || len(segment.Assets.Candidates) != 1 {
			t.Fatalf("warm segment = %+v", segment)
		}
	}
	_, err = processor.Process(context.Background(), plan, makeInput(true))
	if err != nil || searcher.calls != 6 {
		t.Fatalf("intent invalidation: err=%v calls=%d, want exactly one new lookup", err, searcher.calls)
	}
	t.Logf("artlist_prefetch cold_ms=%d warm_ms=%d provider_calls_cold=5 provider_calls_warm=0", coldWall.Milliseconds(), warmWall.Milliseconds())
}

func (s *recordingArtlistSearcher) SearchClips(_ context.Context, _ string, queries []string) ([]ArtlistClipMatch, error) {
	s.queries = append([]string(nil), queries...)
	return []ArtlistClipMatch{{
		Phrase:         queries[0],
		ClipNames:      []string{"Maya temple aerial"},
		ClipDriveLinks: []string{"https://drive.google.com/file/d/test"},
		FolderID:       "maya-folder",
		FolderLink:     "https://drive.google.com/drive/folders/maya-folder",
	}}, nil
}

func TestClipSearchProcessor_UsesManualMayaQuery(t *testing.T) {
	searcher := &recordingArtlistSearcher{}
	processor := NewClipSearchProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{Title: "La civiltà Maya", Language: "it", MediaPlan: media.MediaPlanSpec{
		ProviderPolicy: media.MediaProviderPolicy{Artlist: media.MediaToggleEnabled},
	}}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "main", Text: "Testo Maya", TextHash: "hash",
		Insights: scriptpkg.SegmentInsights{SegmentID: "main", ArtlistQueries: []string{"ancient Maya temples jungle aerial cinematic"}},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.Equal(t, []string{"ancient Maya temples jungle aerial cinematic"}, searcher.queries)
	require.Len(t, result.VidRushSegments, 1)
	require.NotEmpty(t, result.VidRushSegments[0].Assets.Candidates)
	require.Equal(t, "artlist", result.VidRushSegments[0].Assets.Candidates[0].Provider)
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

func TestClipSearchProcessorCacheOnlyNeverCallsProvider(t *testing.T) {
	searcher := &emptyArtlistSearcher{}
	processor := NewClipSearchProcessor(searcher)
	plan := &scriptpkg.ResolvedGenerationPlan{
		MediaPlan: media.MediaPlanSpec{
			Mode:               media.MediaPlanModeCacheOnly,
			ForceRefreshAssets: true, // cache_only must override this flag
			ProviderPolicy:     media.MediaProviderPolicy{Artlist: media.MediaToggleEnabled},
		},
	}
	input := ProcessInput{VidRushSegments: []scriptpkg.VidRushSegmentResult{{
		SegmentID: "cache-only-artlist-miss",
		TextHash:  "cache-only-artlist-hash",
		Insights:  scriptpkg.SegmentInsights{ArtlistQueries: []string{"boxing gym"}},
	}}}

	result, err := processor.Process(context.Background(), plan, input)
	require.NoError(t, err)
	require.Len(t, result.VidRushSegments, 1)
	require.Equal(t, "CACHE_MISS", result.VidRushSegments[0].Cache.Artlist)
	require.Empty(t, result.VidRushSegments[0].Assets.Candidates)
	require.Equal(t, 0, searcher.calls, "cache_only must never call Artlist, even on a miss")
	require.NotEmpty(t, result.Warnings)
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
