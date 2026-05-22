package youtube

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"go.uber.org/zap"
	"velox/go-master/internal/media/models"
)

// TopicSearchResult represents a ranked YouTube result for a topic query.
type TopicSearchResult struct {
	VideoID            string  `json:"video_id"`
	Title              string  `json:"title"`
	ChannelName        string  `json:"channel_name"`
	ThumbnailURL       string  `json:"thumbnail_url"`
	ViewCount          int64   `json:"view_count"`
	UploadDate         string  `json:"upload_date,omitempty"`
	Duration           float64 `json:"duration"`
	SimilarityScore    int     `json:"similarity_score"`
	FormatMatchPercent int     `json:"format_match_percent"`
	DirectLink         string  `json:"direct_link"`
}

// TopicSearchResponse is the API response for topic-based YouTube search.
type TopicSearchResponse struct {
	OK      bool                `json:"ok"`
	Query   string              `json:"query"`
	Limit   int                 `json:"limit"`
	Count   int                 `json:"count"`
	Source  string              `json:"source"`
	Results []TopicSearchResult `json:"results"`
	Error   string              `json:"error,omitempty"`
}

var formatKeywords = map[string][]string{
	"interview":   {"interview", "interviews", "qa", "q&a", "conversation", "talk", "podcast", "discussion"},
	"podcast":     {"podcast", "podcasts", "conversation", "discussion"},
	"clip":        {"clip", "clips", "excerpt", "highlights", "highlight"},
	"short":       {"short", "shorts", "snippet", "excerpt", "clip"},
	"live":        {"live", "livestream", "stream", "broadcast"},
	"reaction":    {"reaction", "reactions", "response"},
	"documentary": {"documentary", "doc", "feature"},
	"news":        {"news", "report", "reporting"},
	"panel":       {"panel", "discussion", "roundtable"},
	"lecture":     {"lecture", "talk", "presentation", "seminar"},
}

// SearchByTopic ranks YouTube search results for a topic and enriches them with metadata.
func (s *Service) SearchByTopic(ctx context.Context, query string, limit int, sortMode string) (*TopicSearchResponse, error) {
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

	baseResults, err := s.SearchLive(ctx, query, limit, sortMode)
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
		wg.Add(1)
		go func(idx int, clip models.MediaAsset) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			item, err := s.enrichTopicResult(ctx, query, clip)
			resultsCh <- enriched{idx: idx, item: item, err: err}
		}(i, base)
	}

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

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

// SearchTopicVideos is a convenience wrapper used by API handlers.
func (s *Service) SearchTopicVideos(ctx context.Context, query string, limit int, sortMode string) (*TopicSearchResponse, error) {
	return s.SearchByTopic(ctx, query, limit, sortMode)
}

func (s *Service) enrichTopicResult(ctx context.Context, query string, clip models.MediaAsset) (TopicSearchResult, error) {
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
		Duration:           metadata.Duration,
		SimilarityScore:    similarity,
		FormatMatchPercent: formatMatch,
		DirectLink:         metadata.URL,
	}, nil
}

func directYouTubeLink(clip models.MediaAsset) string {
	if strings.TrimSpace(clip.ExternalURL) != "" {
		return clip.ExternalURL
	}
	id := strings.TrimPrefix(strings.TrimSpace(clip.ID), "youtube_")
	if id == "" {
		return ""
	}
	return "https://www.youtube.com/watch?v=" + id
}

func scoreTopicSimilarity(query string, metadata *VideoMetadata) int {
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
	if strings.Contains(strings.ToLower(metaText), strings.ToLower(query)) {
		score = 100
	}
	if score > 100 {
		score = 100
	}
	return score
}

func scoreFormatMatch(query string, metadata *VideoMetadata) int {
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

func tokenSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, token := range meaningfulTokens(text) {
		out[token] = true
	}
	return out
}

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

func isStopWord(s string) bool {
	switch s {
	case "the", "a", "an", "and", "or", "of", "to", "for", "in", "on", "with", "by", "from", "at", "about", "video", "clip":
		return true
	default:
		return false
	}
}
