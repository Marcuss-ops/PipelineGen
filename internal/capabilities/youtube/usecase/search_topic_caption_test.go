package usecase

import (
	"context"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// TestTopicSearchPropagatesCaptionProbe pins the T1.2 contract: the
// has_captions probe surfaced by the metadata layer (yt-dlp dump keys
// subtitles/automatic_captions) MUST reach the ranked TopicSearchResult
// rows, because the autonomous agent filters its shortlist on this flag
// AFTER scoring — without it the selection would be blind and every
// candidate without captions would only be discovered post-extraction
// (or after paying a Whisper run).
//
// The enrichment runs the SAME GetVideoInfo call the scorers already
// make, so carrying the flag must cost zero extra metadata requests:
// the stub counts calls to prove it.
func TestTopicSearchPropagatesCaptionProbe(t *testing.T) {
	corpus := []youtubeports.SearchLiveResult{
		{ID: "vid-with-subs", Title: "Denzel Washington Interview", URL: "https://www.youtube.com/watch?v=vid-with-subs"},
		{ID: "vid-no-subs", Title: "Denzel Washington Interview", URL: "https://www.youtube.com/watch?v=vid-no-subs"},
	}

	meta := &captionMetaFetcher{byID: map[string]metaCaps{
		"vid-with-subs": {hasCaptions: true, langs: []string{"en", "it"}},
		"vid-no-subs":   {hasCaptions: false},
	}}
	runner := &stubTopicSearchRunner{corpus: corpus}
	svc := &Service{
		log:         zap.NewNop(),
		search:      NewSearchService(SearchDeps{SearchRunner: runner, Log: zap.NewNop()}),
		metaFetcher: meta,
	}

	resp, err := svc.TopicSearch(context.Background(), "Denzel Washington Interview", 10, "", "")
	if err != nil {
		t.Fatalf("TopicSearch: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 ranked results, got %d", len(resp.Results))
	}

	byID := map[string]struct {
		hasCaptions bool
		langs       []string
	}{}
	for _, r := range resp.Results {
		byID[r.VideoID] = struct {
			hasCaptions bool
			langs       []string
		}{r.HasCaptions, r.CaptionLanguages}
	}

	got := byID["vid-with-subs"]
	if !got.hasCaptions {
		t.Error("has_captions=false for a candidate whose metadata reports captions: the probe was dropped during enrichment")
	}
	if len(got.langs) != 2 || got.langs[0] != "en" || got.langs[1] != "it" {
		t.Errorf("caption_languages=%v, want [en it]", got.langs)
	}
	if byID["vid-no-subs"].hasCaptions {
		t.Error("has_captions=true for a candidate without any caption dictionary")
	}
	if got := int(meta.calls.Load()); got != 2 {
		t.Errorf("metadata probe made %d calls, want exactly 2 (one per candidate, same call the scorers use)", got)
	}
}

// metaCaps is the caption-probe answer for one video id.
type metaCaps struct {
	hasCaptions bool
	langs       []string
}

// captionMetaFetcher returns per-video caption flags and counts calls so
// the test can assert the probe rides the existing enrichment call.
// TopicSearch enriches candidates concurrently (semaphore 4), so the counter
// must be race-safe.
type captionMetaFetcher struct {
	byID  map[string]metaCaps
	calls atomic.Int64
}

func (f *captionMetaFetcher) GetVideoMetadata(_ context.Context, videoURL string) (*youtubeports.DownloaderMetadata, error) {
	f.calls.Add(1)
	id := topicVideoID(videoURL)
	caps := f.byID[id]
	return &youtubeports.DownloaderMetadata{
		ID:               id,
		URL:              videoURL,
		Title:            "Denzel Washington Interview",
		Uploader:         "Graham Bensinger",
		HasCaptions:      caps.hasCaptions,
		CaptionLanguages: caps.langs,
	}, nil
}
