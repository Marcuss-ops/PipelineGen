package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SearchResult represents a single web search result from SearXNG.
type SearchResult struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	URL     string `json:"url"`
}

// searxngResponse represents the SearXNG JSON API response.
type searxngResponse struct {
	Results             []SearchResult   `json:"results"`
	Infoboxes           []searxngInfobox `json:"infoboxes"`
	UnresponsiveEngines [][]string       `json:"unresponsive_engines"`
}

type SearchErrorCode string

const (
	SearchErrorNotConfigured       SearchErrorCode = "SEARXNG_NOT_CONFIGURED"
	SearchErrorUnreachable         SearchErrorCode = "SEARXNG_UNREACHABLE"
	SearchErrorJSONDisabled        SearchErrorCode = "SEARXNG_JSON_FORMAT_DISABLED"
	SearchErrorRateLimited         SearchErrorCode = "SEARXNG_RATE_LIMITED"
	SearchErrorEmptyResults        SearchErrorCode = "SEARXNG_EMPTY_RESULTS"
	SearchErrorEnginesUnresponsive SearchErrorCode = "SEARXNG_ENGINES_UNRESPONSIVE"
	SearchErrorInvalidResponse     SearchErrorCode = "SEARXNG_INVALID_RESPONSE"
)

// SearchError preserves the operator-facing cause of a SearXNG failure while
// keeping the original error available through errors.Is/As.
type SearchError struct {
	Code                SearchErrorCode
	Query               string
	StatusCode          int
	ResultsCount        int
	UnresponsiveEngines [][]string
	Err                 error
}

func (e *SearchError) Error() string {
	if e == nil {
		return ""
	}
	message := string(e.Code)
	if e.Query != "" {
		message += fmt.Sprintf(" query=%q", e.Query)
	}
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" status=%d", e.StatusCode)
	}
	if e.ResultsCount == 0 {
		message += " results=0"
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *SearchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type WebSearcherConfig struct {
	BaseURL    string
	MaxResults int
	Timeout    time.Duration
	Language   string
	Categories string
	SafeSearch int
	UserAgent  string
	Engines    []string
}

type searxngInfobox struct {
	Infobox string `json:"infobox"`
	ID      string `json:"id"`
	Content string `json:"content"`
	URLs    []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
	} `json:"urls"`
}

// WebSearcher performs web searches via SearXNG and returns formatted context
// that can be injected into LLM prompts for RAG augmentation.
type WebSearcher struct {
	baseURL    string
	httpClient *http.Client
	maxResults int
	language   string
	categories string
	safeSearch int
	userAgent  string
	engines    []string
}

// NewWebSearcher creates a WebSearcher targeting a SearXNG instance.
func NewWebSearcher(searxngURL string, maxResults int) *WebSearcher {
	return NewWebSearcherWithConfig(WebSearcherConfig{BaseURL: searxngURL, MaxResults: maxResults, Timeout: 10 * time.Second})
}

func NewWebSearcherWithConfig(cfg WebSearcherConfig) *WebSearcher {
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = 5
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if strings.TrimSpace(cfg.Language) == "" {
		cfg.Language = "en"
	}
	if strings.TrimSpace(cfg.Categories) == "" {
		cfg.Categories = "general"
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "PipelineGen/1.0 (SearXNG research client)"
	}
	if cfg.SafeSearch < 0 || cfg.SafeSearch > 2 {
		cfg.SafeSearch = 0
	}
	return &WebSearcher{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		httpClient: &http.Client{Timeout: cfg.Timeout},
		maxResults: cfg.MaxResults,
		language:   cfg.Language,
		categories: cfg.Categories,
		safeSearch: cfg.SafeSearch,
		userAgent:  cfg.UserAgent,
		engines:    normalizeEngines(cfg.Engines),
	}
}

// Search queries SearXNG and returns raw results.
func (w *WebSearcher) Search(ctx context.Context, query string) ([]SearchResult, error) {
	if w == nil || w.baseURL == "" {
		return nil, &SearchError{Code: SearchErrorNotConfigured, Query: query}
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", w.categories)
	params.Set("language", w.language)
	params.Set("safesearch", fmt.Sprintf("%d", w.safeSearch))
	if len(w.engines) > 0 {
		params.Set("engines", strings.Join(w.engines, ","))
	}

	reqURL := fmt.Sprintf("%s/search?%s", w.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, &SearchError{Code: SearchErrorInvalidResponse, Query: query, Err: err}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", w.userAgent)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, &SearchError{Code: SearchErrorUnreachable, Query: query, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		code := SearchErrorInvalidResponse
		if resp.StatusCode == http.StatusForbidden {
			code = SearchErrorJSONDisabled
		} else if resp.StatusCode == http.StatusTooManyRequests {
			code = SearchErrorRateLimited
		}
		return nil, &SearchError{Code: code, Query: query, StatusCode: resp.StatusCode, Err: fmt.Errorf("%s", strings.TrimSpace(string(body)))}
	}

	var data searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, &SearchError{Code: SearchErrorInvalidResponse, Query: query, StatusCode: resp.StatusCode, Err: err}
	}
	if len(data.Results) == 0 {
		for _, box := range data.Infoboxes {
			rawURL := strings.TrimSpace(box.ID)
			title := strings.TrimSpace(box.Infobox)
			if len(box.URLs) > 0 {
				if strings.TrimSpace(box.URLs[0].URL) != "" {
					rawURL = strings.TrimSpace(box.URLs[0].URL)
				}
				if title == "" {
					title = strings.TrimSpace(box.URLs[0].Title)
				}
			}
			if rawURL == "" || strings.TrimSpace(box.Content) == "" {
				continue
			}
			data.Results = append(data.Results, SearchResult{Title: title, Content: box.Content, URL: rawURL})
		}
	}
	data.Results = deduplicateSearchResults(data.Results)

	if len(data.Results) > w.maxResults {
		data.Results = data.Results[:w.maxResults]
	}
	if len(data.Results) == 0 {
		code := SearchErrorEmptyResults
		if len(data.UnresponsiveEngines) > 0 {
			code = SearchErrorEnginesUnresponsive
		}
		return nil, &SearchError{Code: code, Query: query, StatusCode: http.StatusOK, ResultsCount: 0, UnresponsiveEngines: data.UnresponsiveEngines}
	}

	return data.Results, nil
}

func normalizeEngines(engines []string) []string {
	seen := make(map[string]struct{}, len(engines))
	result := make([]string, 0, len(engines))
	for _, engine := range engines {
		engine = strings.TrimSpace(engine)
		if engine == "" {
			continue
		}
		if _, ok := seen[engine]; ok {
			continue
		}
		seen[engine] = struct{}{}
		result = append(result, engine)
	}
	sort.Strings(result)
	return result
}

func deduplicateSearchResults(results []SearchResult) []SearchResult {
	seen := make(map[string]struct{}, len(results))
	result := make([]SearchResult, 0, len(results))
	for _, item := range results {
		item.URL = strings.TrimSpace(item.URL)
		item.Title = strings.TrimSpace(item.Title)
		if item.URL == "" || item.Title == "" {
			continue
		}
		u, err := url.Parse(item.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		key := item.URL
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, item)
	}
	return result
}

// FormatContext takes search results and returns a formatted string suitable
// for injection into an LLM prompt as RAG context.
func FormatContext(results []SearchResult) string {
	if len(results) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<web_context>\n")
	b.WriteString("Use the following web search results as factual reference when answering. Do not mention that you searched the web.\n\n")

	for i, r := range results {
		title := strings.TrimSpace(r.Title)
		snippet := strings.TrimSpace(r.Content)
		if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}
		b.WriteString(fmt.Sprintf("%d. %s\n   %s\n   Source: %s\n\n", i+1, title, snippet, r.URL))
	}

	b.WriteString("</web_context>\n\n")
	return b.String()
}

// SearchQueryFromTopic extracts the most relevant search query from a topic/title.
// It strips documentary-style prefixes and returns a clean query string.
func SearchQueryFromTopic(topic string) string {
	q := strings.TrimSpace(topic)

	// Remove common prefixes that don't help web search
	for _, prefix := range []string{"La storia di", "La storia del", "La storia della", "Il storia di"} {
		if strings.HasPrefix(q, prefix) {
			q = strings.TrimPrefix(q, prefix)
			break
		}
	}

	q = strings.TrimSpace(q)
	if q == "" {
		q = topic
	}
	return q
}
