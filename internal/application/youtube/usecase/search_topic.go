// TopicSearch is the end-to-end "find me videos for this topic" entry point
// for the YouTube search capability. It reuses the existing SearchLive +
// GetVideoInfo L1/L2 caches owned by the same service so the candidate
// fetch, metadata enrichment, and scoring sit next to each other.
//
// Extracted from the root youtube package during CPR-CC-6 Phase 2
// (June 2026). Before Phase 2 the receiver lived on *youtube.Service
// as SearchByTopicWithFilter; the orchestrator now exposes a 1-line
// forwarder (Service.SearchByTopicWithFilter) that delegates here.
//
// Scoring constants live alongside the scorer routines; future refactors
// can swap the scorers without touching the orchestrator.
package usecase

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	concurrent "github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"

	"go.uber.org/zap"
)

// ── Public types (canonical shape at the search capability boundary) ───

// TopicSearchResponse is the ranked result of a YouTube topic search.
// Emitted by TopicSearch and returned through SearchByTopicWithFilter.
type TopicSearchResponse struct {
	OK      bool                `json:"ok"`
	Query   string              `json:"query"`
	Limit   int                 `json:"limit"`
	Count   int                 `json:"count"`
	Source  string              `json:"source"`
	Results []TopicSearchResult `json:"results"`
}

// TopicSearchResult is a single ranked row in a topic search response.
type TopicSearchResult struct {
	VideoID            string `json:"video_id"`
	Title              string `json:"title"`
	ChannelName        string `json:"channel_name"`
	ThumbnailURL       string `json:"thumbnail_url"`
	ViewCount          int64  `json:"view_count"`
	UploadDate         string `json:"upload_date"`
	Duration           int    `json:"duration"`
	SimilarityScore    int    `json:"similarity_score"`
	FormatMatchPercent int    `json:"format_match_percent"`
	DirectLink         string `json:"direct_link"`
}

// ── Scoring constants ───────────────────────────────────────────────────

// formatKeywords maps each canonical "format" term to its surface aliases.
// Used by scoreFormatMatch and detectFormatTerms so callers can write
// "interview" and find "interviews", "qa", "podcast", etc.
var formatKeywords = map[string][]string{
	"interview":   {"interview", "interviews", "qa", "q&a", "conversation", "talk", "podcast", "discussion"},
	"podcast":     {"podcast", "podcasts", "conversation", "discussion"},
	"clip":        {"clip", "clips", "excerpt", "highlights", "highlight"},
	"short":       {"short", "shorts", "snippet", "excerpt", "clip"},
	"live":        {"live", "livestream", "stream", "broadcast"},
	"reaction":    {"reaction", "reactions", "response"},
	"documentary": {"documentary", "doc", "feature"},
	"panel":       {"panel", "discussion", "roundtable"},
	"lecture":     {"lecture", "talk", "presentation", "seminar"},
}

// ── Service.TopicSearch — single canonical entry point ──────────────────

// TopicSearch ranks YouTube search results with an optional publishedAfter
// date filter.
//
// query: non-empty trimmed search string.
// limit: clamps to [1, 50]; defaults to 10 when <= 0.
// sortMode: forwarded verbatim to SearchLive; "" means "no preference".
// publishedAfter: RFC3339 date string (e.g. "2025-01-01T00:00:00Z") or ""
// for no filter. When set, only videos uploaded after this date remain in
// the response.
//
// Pipeline (1) calls SearchLive with limit*2 to over-fetch for the
// publishedAfter filter, (2) enriches each candidate in parallel with
// GetVideoInfo (re-using the existing L1/L2 metadata cache), (3) scores
// each row by topic-similarity (70%) + format-match (30%) with view count
// + duration as tiebreakers, (4) returns the ranked list.
func (s *Service) TopicSearch(ctx context.Context, query string, limit int, sortMode string, publishedAfter string) (*TopicSearchResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	baseResults, err := s.SearchLive(ctx, query, limit*2, sortMode) // fetch more than needed for filtering
	if err != nil {
		return nil, err
	}

	ranked := make([]TopicSearchResult, 0, len(baseResults))
	type enriched struct {
		idx  int
		item TopicSearchResult
		err  error
	}

	resultsCh := make(chan enriched, len(baseResults))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)

	for i, base := range baseResults {
		i, base := i, base // capture per-iteration
		wg.Add(1)
		concurrent.SafeGoFunc("youtube-topic-enrich", struct {
			Idx  int
			Clip asset.Asset
			Sem  chan struct{}
		}{Idx: i, Clip: base, Sem: sem}, func(arg struct {
			Idx  int
			Clip asset.Asset
			Sem  chan struct{}
		}) {
			defer wg.Done()
			arg.Sem <- struct{}{}
			defer func() { <-arg.Sem }()

			item, err := s.enrichTopicResult(ctx, query, arg.Clip)
			resultsCh <- enriched{idx: arg.Idx, item: item, err: err}
		})
	}

	concurrent.SafeGo("youtube-topic-results-closer", func() {
		wg.Wait()
		close(resultsCh)
	})

	tmp := make([]TopicSearchResult, len(baseResults))
	filled := make([]bool, len(baseResults))
	for res := range resultsCh {
		if res.err != nil {
			s.log.Warn("failed to enrich youtube topic result", zap.Error(res.err))
			continue
		}
		tmp[res.idx] = res.item
		filled[res.idx] = true
	}

	for i := range tmp {
		if filled[i] {
			// Apply publishedAfter filter if set
			if publishedAfter != "" && tmp[i].UploadDate != "" {
				pubDate, err := timeutil.ParseYouTubeUploadDate(tmp[i].UploadDate)
				if err == nil {
					filterAfter := timeutil.ParseRFC3339(publishedAfter)
					if !filterAfter.IsZero() && pubDate.Before(filterAfter) {
						continue // skip videos published before the filter date
					}
				}
			}
			ranked = append(ranked, tmp[i])
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		scoreI := ranked[i].SimilarityScore*70 + ranked[i].FormatMatchPercent*30
		scoreJ := ranked[j].SimilarityScore*70 + ranked[j].FormatMatchPercent*30
		if scoreI != scoreJ {
			return scoreI > scoreJ
		}
		if ranked[i].ViewCount != ranked[j].ViewCount {
			return ranked[i].ViewCount > ranked[j].ViewCount
		}
		return ranked[i].Duration > ranked[j].Duration
	})

	return &TopicSearchResponse{
		OK:      true,
		Query:   query,
		Limit:   limit,
		Count:   len(ranked),
		Source:  "youtube_live",
		Results: ranked,
	}, nil
}

// enrichTopicResult fetches the metadata for a single topic search
// candidate (re-using the L1/L2 cache) and packs it into a YouTube-shaped
// result row. Returns an empty result + error when the candidate lacks a
// resolvable URL.
func (s *Service) enrichTopicResult(ctx context.Context, query string, clip asset.Asset) (TopicSearchResult, error) {
	videoURL := directYouTubeLink(clip)
	if videoURL == "" {
		return TopicSearchResult{}, fmt.Errorf("missing youtube url for clip %s", clip.ID)
	}

	metadata, err := s.GetVideoInfo(ctx, videoURL)
	if err != nil {
		return TopicSearchResult{}, err
	}

	similarity := scoreTopicSimilarity(query, metadata)
	formatMatch := scoreFormatMatch(query, metadata)

	return TopicSearchResult{
		VideoID:            metadata.ID,
		Title:              metadata.Title,
		ChannelName:        metadata.Uploader,
		ThumbnailURL:       metadata.ThumbnailURL,
		ViewCount:          metadata.ViewCount,
		UploadDate:         metadata.UploadDate,
		Duration:           int(metadata.Duration),
		SimilarityScore:    similarity,
		FormatMatchPercent: formatMatch,
		DirectLink:         metadata.URL,
	}, nil
}

// directYouTubeLink returns the canonical watch URL for a clip.
func directYouTubeLink(clip asset.Asset) string {
	if strings.TrimSpace(clip.ExternalURL()) != "" {
		return clip.ExternalURL()
	}
	id := strings.TrimPrefix(strings.TrimSpace(clip.ID), "youtube_")
	if id == "" {
		return ""
	}
	return "https://www.youtube.com/watch?v=" + id
}

// ── Scoring helpers (package-private, used by TopicSearch + tests) ──────

// scoreTopicSimilarity returns a [0,100] integer score for how well the
// query matches the video metadata (title / uploader / description /
// tags / categories). Exact substring matches in metadata yield 100.
func scoreTopicSimilarity(query string, metadata *ports.DownloaderMetadata) int {
	queryTokens := meaningfulTokens(query)
	if len(queryTokens) == 0 || metadata == nil {
		return 0
	}

	metaText := strings.Join([]string{
		metadata.Title,
		metadata.Uploader,
		metadata.Description,
		strings.Join(metadata.Tags, " "),
		strings.Join(metadata.Categories, " "),
	}, " ")
	metaTokens := tokenSet(metaText)

	matches := 0
	for _, token := range queryTokens {
		if metaTokens[token] {
			matches++
		}
	}

	score := int(math.Round((float64(matches) / float64(len(queryTokens))) * 100))
	if textutil.ContainsCI(metaText, query) {
		score = 100
	}
	if score > 100 {
		score = 100
	}
	return score
}

// scoreFormatMatch returns a [0,100] integer score for how well the
// query's format terms (interview, podcast, clip, ...) match the
// metadata. Returns 45 as a neutral floor when neither side mentions a
// recognised format, 60 when only the metadata mentions a format.
func scoreFormatMatch(query string, metadata *ports.DownloaderMetadata) int {
	if metadata == nil {
		return 0
	}

	queryFormat := detectFormatTerms(query)
	metaFormat := detectFormatTerms(strings.Join([]string{
		metadata.Title,
		metadata.Uploader,
		metadata.Description,
		strings.Join(metadata.Tags, " "),
		strings.Join(metadata.Categories, " "),
	}, " "))

	if len(queryFormat) == 0 {
		if len(metaFormat) > 0 {
			return 60
		}
		return 45
	}

	matches := 0
	for term := range queryFormat {
		if metaFormat[term] {
			matches++
			continue
		}
		for _, alias := range formatKeywords[term] {
			if metaFormat[alias] {
				matches++
				break
			}
		}
	}

	score := int(math.Round((float64(matches) / float64(len(queryFormat))) * 100))
	if score > 100 {
		score = 100
	}
	return score
}

// detectFormatTerms returns the set of canonical format terms present in
// text (matched either by the canonical token or any of its aliases).
func detectFormatTerms(text string) map[string]bool {
	out := map[string]bool{}
	tokens := meaningfulTokens(text)
	for term, aliases := range formatKeywords {
		for _, token := range tokens {
			if token == term {
				out[term] = true
				break
			}
			for _, alias := range aliases {
				if token == alias {
					out[term] = true
					break
				}
			}
			if out[term] {
				break
			}
		}
	}
	return out
}

// tokenSet returns the unique tokens of text as a set.
func tokenSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, token := range meaningfulTokens(text) {
		out[token] = true
	}
	return out
}

// meaningfulTokens lower-cases text, splits on non-word runes, drops stop
// words, and returns the remaining tokens (preserving order).
func meaningfulTokens(text string) []string {
	text = strings.ToLower(text)
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	out := make([]string, 0, len(words))
	for _, word := range words {
		word = strings.TrimSpace(word)
		if word == "" || isStopWord(word) {
			continue
		}
		out = append(out, word)
	}
	return out
}

// isStopWord returns true when s is a noise token that shouldn't affect
// the similarity score.
func isStopWord(s string) bool {
	if linguistics.IsStopWord(s) {
		return true
	}
	// Also check known YouTube noise tokens that are not in the standard
	// stop-word list but should be ignored for topic similarity.
	switch s {
	case "video", "clip":
		return true
	}
	return false
}
