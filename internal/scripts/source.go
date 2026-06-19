package scripts

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama/client"

	urlutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure"

	"go.uber.org/zap"
)

// SourceResult holds the resolved source text and metadata for a single topic.
type SourceResult struct {
	Topic        string
	SourceText   string
	SourceOrigin string // "inline_text", "youtube_url", "web_search", "empty"
	WebContext   string
	RawSources   []ResolvedResearchSource
	SearchStart  time.Time
	SearchEnd    time.Time
}

// ResolvedResearchSource is a single research item captured during source resolution.
type ResolvedResearchSource struct {
	Query          string
	URL            string
	Title          string
	Snippet        string
	SourceType     string // "web", "youtube", "transcript"
	UsedInSections string // JSON array of section indices
	RelevanceScore float64
}

// ToDB converts a ResolvedResearchSource to the repository type.
func (r ResolvedResearchSource) ToDB() ScriptResearchSource {
	return ScriptResearchSource{
		Query:          r.Query,
		URL:            r.URL,
		Title:          r.Title,
		Snippet:        r.Snippet,
		SourceType:     r.SourceType,
		UsedInSections: r.UsedInSections,
		RelevanceScore: r.RelevanceScore,
	}
}

// SourceTextResolver resolves raw source text (e.g. YouTube URL → metadata+transcript bundle).
// Returns (resolvedText, origin, error).
type SourceTextResolver func(ctx context.Context, raw string) (string, string, error)

// SourceResolver resolves source text and performs web search for topics.
// It also collects raw search results for research source tracking.
type SourceResolver struct {
	log     *zap.Logger
	search  *client.WebSearcher
	resolve SourceTextResolver
}

// NewSourceResolver creates a resolver. If search is nil, web search is skipped.
// resolveFn is called for each topic's sourceText to handle YouTube URLs etc.
func NewSourceResolver(resolveFn SourceTextResolver, search *client.WebSearcher, log *zap.Logger) *SourceResolver {
	if resolveFn == nil {
		resolveFn = defaultSourceResolver
	}
	return &SourceResolver{
		log:     log,
		search:  search,
		resolve: resolveFn,
	}
}

// ResolveSource resolves a single topic's source text and performs web search.
func (r *SourceResolver) ResolveSource(ctx context.Context, topic, sourceText string, searchTimeout time.Duration) (*SourceResult, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, fmt.Errorf("empty topic")
	}

	result := &SourceResult{
		Topic:       topic,
		SearchStart: time.Now(),
	}

	// 1. Resolve the source text (YouTube URL → bundle, inline text → as-is)
	resolved, origin, err := r.resolve(ctx, sourceText)
	if err != nil {
		r.log.Warn("source resolution failed, falling back", zap.String("topic", topic), zap.Error(err))
		origin = "inline_text_fallback"
		resolved = strings.TrimSpace(sourceText)
	}
	result.SourceText = resolved
	result.SourceOrigin = origin

	// 2. Run web search
	if r.search != nil {
		webCtx, rawResults := r.runSearch(ctx, topic, searchTimeout)
		result.WebContext = webCtx
		for _, raw := range rawResults {
			result.RawSources = append(result.RawSources, ResolvedResearchSource{
				Query:      topic,
				URL:        raw.URL,
				Title:      raw.Title,
				Snippet:    raw.Content,
				SourceType: "web",
			})
		}
	}

	result.SearchEnd = time.Now()
	return result, nil
}

// runSearch performs a web search and returns formatted context + raw results.
func (r *SourceResolver) runSearch(ctx context.Context, query string, timeout time.Duration) (string, []client.SearchResult) {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	searchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	results, err := r.search.Search(searchCtx, query)
	if err != nil || len(results) == 0 {
		return "", nil
	}

	return client.FormatContext(results), results
}

// SearchTopics performs parallel web search for multiple topics and returns
// formatted context per topic plus all raw research sources.
func (r *SourceResolver) SearchTopics(ctx context.Context, topics []string, timeout time.Duration, concurrency int) (map[string]string, []ResolvedResearchSource) {
	if r.search == nil || len(topics) == 0 {
		return nil, nil
	}
	if concurrency <= 0 {
		concurrency = 4
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	type topicResult struct {
		topic   string
		context string
		sources []ResolvedResearchSource
	}

	results := make(chan topicResult, len(topics))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			continue
		}
		wg.Add(1)
		go func(q string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			searchCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			res, err := r.search.Search(searchCtx, q)
			if err != nil || len(res) == 0 {
				results <- topicResult{topic: q}
				return
			}

			var sources []ResolvedResearchSource
			for _, sr := range res {
				sources = append(sources, ResolvedResearchSource{
					Query:      q,
					URL:        sr.URL,
					Title:      sr.Title,
					Snippet:    sr.Content,
					SourceType: "web",
				})
			}
			results <- topicResult{
				topic:   q,
				context: client.FormatContext(res),
				sources: sources,
			}
		}(topic)
	}

	// Wait for all goroutines to finish, then close results channel
	go func() {
		wg.Wait()
		close(results)
	}()

	contextMap := make(map[string]string)
	var allSources []ResolvedResearchSource
	for tr := range results {
		if tr.context != "" {
			contextMap[tr.topic] = tr.context
		}
		allSources = append(allSources, tr.sources...)
	}
	return contextMap, allSources
}

// IsYouTubeURL checks if a string looks like a YouTube URL.
func IsYouTubeURL(raw string) bool {
	_, err := urlutil.ExtractVideoID(raw)
	return err == nil
}

// defaultSourceResolver is the fallback when no resolver is provided.
// It passes through inline text and rejects YouTube URLs (they need yt-dlp).
func defaultSourceResolver(_ context.Context, raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "empty", nil
	}
	if IsYouTubeURL(raw) {
		return raw, "youtube_url_unresolved", fmt.Errorf("youtube URL requires yt-dlp resolver")
	}
	return raw, "inline_text", nil
}
