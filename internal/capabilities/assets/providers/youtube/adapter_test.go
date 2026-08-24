package youtube

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	// DTOs (VideoCutRequest/Result, DownloaderMetadata) live in ports/.
	// TopicSearchResponse/Result stay top-level.
	youtubesrc "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	youtubesrcports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
)

// ── Test double ─────────────────────────────────────────────────────

// fakeSearcher implements the private searcher interface so adapter
// tests can run without spinning up a full *youtubesrc.Service (heavy
// scraper + repository chain).
type fakeSearcher struct {
	lastCall struct {
		Query          string
		Limit          int
		SortMode       string
		PublishedAfter string
	}
	resp *youtubesrc.TopicSearchResponse
	err  error
}

func (f *fakeSearcher) SearchByTopicWithFilter(
	_ context.Context,
	query string,
	limit int,
	sortMode, publishedAfter string,
) (*youtubesrc.TopicSearchResponse, error) {
	f.lastCall.Query = query
	f.lastCall.Limit = limit
	f.lastCall.SortMode = sortMode
	f.lastCall.PublishedAfter = publishedAfter
	return f.resp, f.err
}

func newAdapterWith(s searcher) *Adapter { return &Adapter{src: s, fetcher: nil} }

func newFetchAdapterWith(s searcher, f fetcher) *Adapter { return &Adapter{src: s, fetcher: f} }

// ── Fetch test double ───────────────────────────────────────────────

type fakeFetcher struct {
	lastReq youtubesrcports.VideoCutRequest
	result  *youtubesrcports.VideoCutResult
	err     error
}

func (f *fakeFetcher) DownloadAndCut(_ context.Context, req youtubesrcports.VideoCutRequest) (*youtubesrcports.VideoCutResult, error) {
	f.lastReq = req
	return f.result, f.err
}

func TestFetch_NilFetcher_ReturnsError(t *testing.T) {
	a := &Adapter{src: &fakeSearcher{}, fetcher: nil}
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://www.youtube.com/watch?v=abc",
	})
	if err == nil {
		t.Fatal("expected error on nil fetcher")
	}
}

func TestFetch_EmptySourceRef_ReturnsError(t *testing.T) {
	a := newFetchAdapterWith(&fakeSearcher{}, &fakeFetcher{})
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "",
	})
	if err == nil {
		t.Fatal("expected error on empty SourceRef")
	}
}

func TestFetch_FullVideo_UsesSentinelDuration(t *testing.T) {
	ff := &fakeFetcher{
		result: &youtubesrcports.VideoCutResult{
			LocalPath: "/tmp/yt_full.mp4",
			Metadata: &youtubesrcports.DownloaderMetadata{
				Title:    "Full Video Title",
				Uploader: "Test Channel",
				Duration: 300.0,
			},
		},
	}
	a := newFetchAdapterWith(&fakeSearcher{}, ff)
	res, err := a.Fetch(context.Background(), providers.FetchRequest{
		AssetID:   "test-full",
		SourceRef: "https://www.youtube.com/watch?v=abc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LocalPath != "/tmp/yt_full.mp4" {
		t.Errorf("LocalPath=%q", res.LocalPath)
	}
	if res.Asset.Name != "Full Video Title" {
		t.Errorf("Asset.Name=%q", res.Asset.Name)
	}
	if ff.lastReq.Duration != 86400 {
		t.Errorf("expected sentinel Duration=86400 for full video, got %v", ff.lastReq.Duration)
	}
	if ff.lastReq.Start != 0 {
		t.Errorf("expected Start=0 for full video, got %v", ff.lastReq.Start)
	}
}

func TestFetch_SegmentExtraction_SetsBounds(t *testing.T) {
	ff := &fakeFetcher{
		result: &youtubesrcports.VideoCutResult{
			LocalPath: "/tmp/yt_segment.mp4",
			Metadata: &youtubesrcports.DownloaderMetadata{
				Title:    "Segment Title",
				Duration: 60.0,
			},
		},
	}
	a := newFetchAdapterWith(&fakeSearcher{}, ff)
	res, err := a.Fetch(context.Background(), providers.FetchRequest{
		AssetID:      "test-segment",
		SourceRef:    "https://www.youtube.com/watch?v=abc",
		SegmentStart: 10 * time.Second,
		SegmentEnd:   70 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LocalPath != "/tmp/yt_segment.mp4" {
		t.Errorf("LocalPath=%q", res.LocalPath)
	}
	if ff.lastReq.Start != 10 {
		t.Errorf("expected Start=10, got %v", ff.lastReq.Start)
	}
	if ff.lastReq.Duration != 60 {
		t.Errorf("expected Duration=60, got %v", ff.lastReq.Duration)
	}
}

func TestFetch_NilMetadata_FallsBackToAssetID(t *testing.T) {
	// Cache hits in the pipeline return Metadata: nil.
	ff := &fakeFetcher{
		result: &youtubesrcports.VideoCutResult{
			LocalPath: "/tmp/yt_cached.mp4",
			Metadata:  nil,
		},
	}
	a := newFetchAdapterWith(&fakeSearcher{}, ff)
	res, err := a.Fetch(context.Background(), providers.FetchRequest{
		AssetID:   "my-asset-id",
		SourceRef: "https://www.youtube.com/watch?v=abc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Asset.Name != "my-asset-id" {
		t.Errorf("expected Asset.Name to fall back to AssetID, got %q", res.Asset.Name)
	}
	if res.Asset.Source != "youtube" {
		t.Errorf("expected Source=youtube, got %q", res.Asset.Source)
	}
}

func TestFetch_PropagatesUpstreamError(t *testing.T) {
	sentinel := errors.New("yt-dlp download failed")
	ff := &fakeFetcher{err: sentinel}
	a := newFetchAdapterWith(&fakeSearcher{}, ff)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		SourceRef: "https://www.youtube.com/watch?v=abc",
	})
	if err == nil {
		t.Fatal("expected error propagating upstream failure")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) true; got %v", err)
	}
}

// ── Tests ──────────────────────────────────────────────────────────

func TestAdapter_Name(t *testing.T) {
	a := newAdapterWith(&fakeSearcher{})
	if got := a.Name(); got != "youtube" {
		t.Fatalf("Name()=%q, want \"youtube\"", got)
	}
}

func TestAdapter_Capabilities(t *testing.T) {
	a := newAdapterWith(&fakeSearcher{})
	caps := a.Capabilities()
	if !slices.Contains(caps, providers.CapabilitySearch) {
		t.Errorf("Capabilities() missing CapabilitySearch: %v", caps)
	}
	if !slices.Contains(caps, providers.CapabilityVideo) {
		t.Errorf("Capabilities() missing CapabilityVideo: %v", caps)
	}
	if !slices.Contains(caps, providers.CapabilityFetch) {
		t.Errorf("Capabilities() missing CapabilityFetch (added Punto 6): %v", caps)
	}
}

// Adapter MUST implement FetchProvider — verified at compile time
// and tested here as a runtime assertion for the adapter instance.
func TestAdapter_ImplementsFetchProvider(t *testing.T) {
	a := &Adapter{src: &fakeSearcher{}, fetcher: nil}
	var sp providers.SearchProvider = a
	if _, ok := sp.(providers.FetchProvider); !ok {
		t.Fatal("youtube Adapter must satisfy FetchProvider (Punto 6)")
	}
}

func TestSearch_NilInterfaceSource(t *testing.T) {
	a := &Adapter{} // src == nil interface value
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if !errors.Is(err, ErrSourceNotWired) {
		t.Fatalf("expected ErrSourceNotWired, got %v", err)
	}
	if res.Candidates != nil {
		t.Errorf("expected nil candidates, got %v", res.Candidates)
	}
	if res.NextPageToken != "" {
		t.Errorf("expected empty NextPageToken, got %q", res.NextPageToken)
	}
}

func TestSearch_TypedNilSource(t *testing.T) {
	// Mirror the registry's typed-nil convention: a non-nil
	// interface holding a nil concrete pointer must also be
	// rejected.
	a := &Adapter{src: (*fakeSearcher)(nil)}
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if !errors.Is(err, ErrSourceNotWired) {
		t.Fatalf("expected ErrSourceNotWired on typed-nil, got %v", err)
	}
	if res.Candidates != nil {
		t.Errorf("expected nil candidates, got %v", res.Candidates)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	a := newAdapterWith(&fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}})
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: ""})
	if err == nil {
		t.Fatalf("expected error on empty query, got nil (results=%v)", res)
	}
	if res.Candidates != nil {
		t.Errorf("expected nil candidates on empty query, got %v", res.Candidates)
	}
}

func TestSearch_LimitClamping(t *testing.T) {
	cases := []struct {
		name    string
		inLimit int
		want    int
	}{
		{"zero clamped to default 10", 0, 10},
		{"negative clamped to default 10", -3, 10},
		{"over 50 clamped to 50", 100, 50},
		{"exactly 50 preserved", 50, 50},
		{"positive in-range preserved", 7, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
			a := newAdapterWith(stub)
			res, err := a.Search(context.Background(), providers.SearchRequest{
				Query: "x",
				Limit: tc.inLimit,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Candidates == nil {
				t.Fatalf("expected non-nil candidates, got %v", res)
			}
			if stub.lastCall.Limit != tc.want {
				t.Errorf("forwarded Limit=%d, want %d", stub.lastCall.Limit, tc.want)
			}
		})
	}
}

func TestSearch_SortModeMapping(t *testing.T) {
	cases := []struct {
		name     string
		inSort   providers.SortMode
		wantSort string
	}{
		{"empty maps to native default", "", ""},
		{"SortByRelevance maps to native default", providers.SortByRelevance, ""},
		{"SortByNewest passes through", providers.SortByNewest, "newest"},
		{"SortByOldest passes through", providers.SortByOldest, "oldest"},
		{"SortByLongest passes through", providers.SortByLongest, "longest"},
		{"SortByShortest passes through", providers.SortByShortest, "shortest"},
		{"unknown value passes through verbatim", providers.SortMode("custom"), "custom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
			a := newAdapterWith(stub)
			res, err := a.Search(context.Background(), providers.SearchRequest{
				Query:   "x",
				Limit:   5,
				Filters: providers.SearchFilters{Sort: tc.inSort},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Candidates == nil {
				t.Fatalf("expected non-nil candidates, got %v", res)
			}
			if stub.lastCall.SortMode != tc.wantSort {
				t.Errorf("forwarded SortMode=%q, want %q", stub.lastCall.SortMode, tc.wantSort)
			}
		})
	}
}

func TestSearch_PublishedAfterForwarding(t *testing.T) {
	t.Run("nil filters publishedAfter forwarded as empty string", func(t *testing.T) {
		stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
		a := newAdapterWith(stub)
		res, err := a.Search(context.Background(), providers.SearchRequest{
			Query: "x",
			Limit: 5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Candidates == nil {
			t.Fatalf("expected non-nil candidates, got %v", res)
		}
		if stub.lastCall.PublishedAfter != "" {
			t.Errorf("expected empty PublishedAfter, got %q", stub.lastCall.PublishedAfter)
		}
	})

	t.Run("non-nil filters publishedAfter forwarded as RFC3339 UTC", func(t *testing.T) {
		want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
		stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
		a := newAdapterWith(stub)
		res, err := a.Search(context.Background(), providers.SearchRequest{
			Query: "x",
			Limit: 5,
			Filters: providers.SearchFilters{
				PublishedAfter: &want,
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Candidates == nil {
			t.Fatalf("expected non-nil candidates, got %v", res)
		}
		if stub.lastCall.PublishedAfter == "" {
			t.Fatalf("expected non-empty PublishedAfter, got empty")
		}
		parsed, err := time.Parse(time.RFC3339, stub.lastCall.PublishedAfter)
		if err != nil {
			t.Fatalf("forwarded PublishedAfter %q is not RFC3339: %v", stub.lastCall.PublishedAfter, err)
		}
		if !parsed.Equal(want) {
			t.Errorf("forwarded PublishedAfter=%v want %v", parsed, want)
		}
	})

	t.Run("non-UTC time is normalised to UTC before encoding", func(t *testing.T) {
		// 03:04:05 in a +02:00 zone == 01:04:05 UTC.
		loc := time.FixedZone("plus02", 2*60*60)
		in := time.Date(2025, 6, 7, 3, 4, 5, 0, loc)
		want := in.UTC()
		stub := &fakeSearcher{resp: &youtubesrc.TopicSearchResponse{}}
		a := newAdapterWith(stub)
		_, _ = a.Search(context.Background(), providers.SearchRequest{
			Query: "x", Limit: 5,
			Filters: providers.SearchFilters{PublishedAfter: &in},
		})
		parsed, err := time.Parse(time.RFC3339, stub.lastCall.PublishedAfter)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if !parsed.Equal(want) {
			t.Errorf("forwarded=%v want %v", parsed, want)
		}
	})
}

func TestSearch_CandidateTranslation(t *testing.T) {
	stub := &fakeSearcher{
		resp: &youtubesrc.TopicSearchResponse{
			Results: []youtubesrc.TopicSearchResult{
				{
					VideoID:            "v123abc",
					Title:              "An interesting video",
					ChannelName:        "Channel X",
					ThumbnailURL:       "https://example.com/thumb.jpg",
					ViewCount:          9000,
					UploadDate:         "20250101",
					Duration:           123, // seconds
					SimilarityScore:    80,
					FormatMatchPercent: 60,
					DirectLink:         "https://www.youtube.com/watch?v=v123abc",
				},
			},
		},
	}
	a := newAdapterWith(stub)
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x", Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(res.Candidates))
	}
	c := res.Candidates[0]

	if c.SourceName != "youtube" {
		t.Errorf("SourceName=%q want \"youtube\"", c.SourceName)
	}
	if c.SourceRef != "v123abc" {
		t.Errorf("SourceRef=%q want \"v123abc\"", c.SourceRef)
	}
	if c.Title != "An interesting video" {
		t.Errorf("Title=%q", c.Title)
	}
	if c.PreviewURL != "https://www.youtube.com/watch?v=v123abc" {
		t.Errorf("PreviewURL=%q", c.PreviewURL)
	}
	if c.ThumbnailURL != "https://example.com/thumb.jpg" {
		t.Errorf("ThumbnailURL=%q", c.ThumbnailURL)
	}
	if c.MediaType != youtubeMediaType {
		t.Errorf("MediaType=%q want %q", c.MediaType, youtubeMediaType)
	}
	wantDur := time.Duration(123 * time.Second)
	if c.Duration != wantDur {
		t.Errorf("Duration=%v want %v", c.Duration, wantDur)
	}
	if c.PublishedAt == nil {
		t.Errorf("expected non-nil PublishedAt (parsed from YYYYMMDD)")
	}
	// combinedScore: 80*70 + 60*30 = 5600 + 1800 = 7400 → 0.74.
	if c.Score != 0.74 {
		t.Errorf("Score=%v want 0.74", c.Score)
	}
	if res.NextPageToken != "" {
		t.Errorf("NextPageToken=%q want \"\"", res.NextPageToken)
	}
}

func TestSearch_CandidateTranslation_UnparseableUploadDateIsNil(t *testing.T) {
	stub := &fakeSearcher{
		resp: &youtubesrc.TopicSearchResponse{
			Results: []youtubesrc.TopicSearchResult{
				{
					VideoID:    "v1",
					Duration:   10,
					UploadDate: "not-a-date",
				},
			},
		},
	}
	a := newAdapterWith(stub)
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(res.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(res.Candidates))
	}
	if res.Candidates[0].PublishedAt != nil {
		t.Errorf("expected nil PublishedAt on unparseable upload_date, got %v", res.Candidates[0].PublishedAt)
	}
}

func TestSearch_NilResponseReturnsNilCandidates(t *testing.T) {
	stub := &fakeSearcher{resp: nil}
	a := newAdapterWith(stub)
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if res.Candidates != nil {
		t.Errorf("expected nil candidates on nil response, got %v", res.Candidates)
	}
	if res.NextPageToken != "" {
		t.Errorf("expected empty NextPageToken on nil response, got %q", res.NextPageToken)
	}
}

func TestSearch_PropagatesUpstreamError(t *testing.T) {
	sentinel := errors.New("upstream failure")
	stub := &fakeSearcher{err: sentinel}
	a := newAdapterWith(stub)
	res, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"})
	if err == nil {
		t.Fatalf("expected error propagating upstream failure")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) true; got %v", err)
	}
	if res.Candidates != nil {
		t.Errorf("expected nil candidates on error, got %v", res.Candidates)
	}
}

func TestNewAdapter_NilService_SearchReturnsErrSourceNotWired(t *testing.T) {
	a := NewAdapter(nil)
	if a.Name() != "youtube" {
		t.Errorf("Name mismatch")
	}
	if _, err := a.Search(context.Background(), providers.SearchRequest{Query: "x"}); !errors.Is(err, ErrSourceNotWired) {
		t.Errorf("expected ErrSourceNotWired, got %v", err)
	}
}

// ── NoAudio propagation ─────────────────────────────────────────────

// TestFetch_NoAudio_True_MapsToKeepAudioFalse locks the critical
// adapter-layer mapping: when providers.FetchRequest.NoAudio is true,
// the underlying VideoCutRequest.KeepAudio MUST be false (inverted)
// so ffmpeg's -an flag strips the audio stream.
//
// Regression guard for PR-YT-NO-AUDIO-THREAD (July 2026): pre-fix,
// KeepAudio was hardcoded to true regardless of NoAudio, so the audio
// track was never stripped even when the API caller set no_audio=true.
func TestFetch_NoAudio_True_MapsToKeepAudioFalse(t *testing.T) {
	ff := &fakeFetcher{
		result: &youtubesrcports.VideoCutResult{
			LocalPath: "/tmp/yt_noaudio.mp4",
			Metadata: &youtubesrcports.DownloaderMetadata{
				Title: "No Audio Clip",
			},
		},
	}
	a := newFetchAdapterWith(&fakeSearcher{}, ff)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		AssetID:   "test-noaudio",
		SourceRef: "https://www.youtube.com/watch?v=abc",
		NoAudio:   true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ff.lastReq.KeepAudio {
		t.Errorf("expected KeepAudio=false when NoAudio=true, got KeepAudio=true")
	}
}

// TestFetch_NoAudio_False_MapsToKeepAudioTrue locks the backward-
// compatible default: when NoAudio is false (zero-value), KeepAudio
// MUST be true (audio preserved).
func TestFetch_NoAudio_False_MapsToKeepAudioTrue(t *testing.T) {
	ff := &fakeFetcher{
		result: &youtubesrcports.VideoCutResult{
			LocalPath: "/tmp/yt_withaudio.mp4",
			Metadata: &youtubesrcports.DownloaderMetadata{
				Title: "With Audio Clip",
			},
		},
	}
	a := newFetchAdapterWith(&fakeSearcher{}, ff)
	_, err := a.Fetch(context.Background(), providers.FetchRequest{
		AssetID:   "test-withaudio",
		SourceRef: "https://www.youtube.com/watch?v=abc",
		NoAudio:   false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ff.lastReq.KeepAudio {
		t.Errorf("expected KeepAudio=true when NoAudio=false (default), got KeepAudio=false")
	}
}
