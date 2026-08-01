package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	Results   []SearchResult   `json:"results"`
	Infoboxes []searxngInfobox `json:"infoboxes"`
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
}

// NewWebSearcher creates a WebSearcher targeting a SearXNG instance.
func NewWebSearcher(searxngURL string, maxResults int) *WebSearcher {
	if maxResults <= 0 {
		maxResults = 5
	}
	return &WebSearcher{
		baseURL:    strings.TrimRight(searxngURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
		maxResults: maxResults,
	}
}

// Search queries SearXNG and returns raw results.
func (w *WebSearcher) Search(ctx context.Context, query string) ([]SearchResult, error) {
	if w == nil || w.baseURL == "" {
		return nil, fmt.Errorf("web searcher not configured")
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")

	reqURL := fmt.Sprintf("%s/search?%s", w.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create search request: %w", err)
	}

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SearXNG request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SearXNG returned status %d: %s", resp.StatusCode, string(body))
	}

	var data searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode SearXNG response: %w", err)
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

	if len(data.Results) > w.maxResults {
		data.Results = data.Results[:w.maxResults]
	}

	return data.Results, nil
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
