// Package webresearch — provider_duckduckgo.go provides a DuckDuckGo
// text search adapter as a fallback web search provider. It scrapes
// DuckDuckGo's HTML lite endpoint — no API key required.
//
// This is a best-effort fallback, not a strategic dependency. HTML
// scraping may break without notice; the architecture allows easy
// replacement with Bing, Brave, Serper, or any other WebSearchProvider.
//
// New adapter (August 2026): registered in package_hotspots.json under
// the infrastructure adapter migration owner for multi-provider research
// fallback.
package webresearch

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	"go.uber.org/zap"
)

// DuckDuckGoSearchProvider implements WebSearchProvider via DuckDuckGo's
// HTML lite endpoint. It carries no API key and is best-effort.
//
// Anomaly-challenge resilience: DDG answers 202 (challenge page) or 403
// when it suspects a bot on the IP. Those statuses are transient — the
// same IP is often challenged for a few seconds and then served normally
// — so the provider backs off briefly and retries once before declaring
// itself down.
type DuckDuckGoSearchProvider struct {
	client  *http.Client
	log     *zap.Logger
	backoff func() time.Duration // delay before the anomaly-challenge retry (injectable for tests)
}

// NewDuckDuckGoSearchProvider creates a DDG text search provider with
// a 10-second timeout and a randomized 2-5s anomaly-challenge backoff.
func NewDuckDuckGoSearchProvider(log *zap.Logger) *DuckDuckGoSearchProvider {
	if log == nil {
		log = zap.NewNop()
	}
	return &DuckDuckGoSearchProvider{
		client:  &http.Client{Timeout: 10 * time.Second},
		log:     log,
		backoff: defaultDDGBackoff,
	}
}

// defaultDDGBackoff returns a randomized delay in [2s, 5s).
func defaultDDGBackoff() time.Duration {
	return 2*time.Second + time.Duration(rand.Float64()*3*float64(time.Second))
}

// isDDGAnomalyChallenge reports whether the status code is DDG's
// anti-bot anomaly challenge (202 challenge page or 403 access denied).
func isDDGAnomalyChallenge(status int) bool {
	return status == http.StatusAccepted || status == http.StatusForbidden
}

func (d *DuckDuckGoSearchProvider) Name() string { return "duckduckgo" }

func (d *DuckDuckGoSearchProvider) Search(ctx context.Context, query string, limit int) ([]scriptports.WebSearchHit, error) {
	if d == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	hits, status, err := d.searchOnce(ctx, query, limit)
	if err == nil {
		return hits, nil
	}

	// Selective retry: only the anomaly-challenge statuses (202/403) are
	// retried — they are often a transient per-IP challenge, not a real
	// outage. Every other failure (5xx, network, timeout) fails fast and
	// lets the coordinator fall through to the next provider.
	if !isDDGAnomalyChallenge(status) {
		return nil, err
	}

	d.log.Warn("ddg: anomaly challenge, backing off before one retry", zap.Int("status", status))
	delay := d.backoff()
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("ddg search: %w", ctx.Err())
		}
	}

	hits, retryStatus, retryErr := d.searchOnce(ctx, query, limit)
	if retryErr != nil {
		if isDDGAnomalyChallenge(retryStatus) {
			d.log.Warn("ddg: anomaly challenge persists after retry, provider down", zap.Int("status", retryStatus))
		}
		return nil, retryErr
	}
	d.log.Info("ddg: recovered after anomaly-challenge retry", zap.Int("initial_status", status))
	return hits, nil
}

// searchOnce performs a single POST to DDG's HTML lite endpoint. It
// returns the parsed hits, the HTTP status (even on error, so callers
// can decide whether to retry), and the error.
func (d *DuckDuckGoSearchProvider) searchOnce(ctx context.Context, query string, limit int) ([]scriptports.WebSearchHit, int, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("kl", "us-en")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://html.duckduckgo.com/html/", strings.NewReader(params.Encode()))
	if err != nil {
		return nil, 0, fmt.Errorf("ddg search: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "PipelineGen-WebResearch/1.0 (fallback search)")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ddg search: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// The 202 challenge page is large; keep only the first 4KB in the
		// error so logs are not flooded with anti-bot HTML.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, fmt.Errorf("ddg search: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("ddg search: read body: %w", err)
	}

	hits := parseDDGHTML(string(body))
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, resp.StatusCode, nil
}

// parseDDGHTML extracts search results from DuckDuckGo's HTML lite page.
// Each result has a link with class "result__a" and a snippet with
// class "result__snippet".
func parseDDGHTML(html string) []scriptports.WebSearchHit {
	var hits []scriptports.WebSearchHit
	seen := make(map[string]struct{})

	// Extract result blocks: each result has a link and a snippet.
	linkRe := regexp.MustCompile(`<a[^>]+class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	snippetRe := regexp.MustCompile(`<a[^>]+class="result__snippet"[^>]*>(.*?)</a>`)

	links := linkRe.FindAllStringSubmatch(html, -1)
	snippets := snippetRe.FindAllStringSubmatch(html, -1)

	for i, match := range links {
		if len(match) < 3 {
			continue
		}
		rawURL := strings.TrimSpace(match[1])
		title := stripHTMLTags(match[2])

		// DDG wraps result URLs in a redirect; extract the actual URL.
		if u, err := url.Parse(rawURL); err == nil {
			if uddg := u.Query().Get("uddg"); uddg != "" {
				rawURL = uddg
			}
		}

		if rawURL == "" || title == "" {
			continue
		}
		if _, ok := seen[rawURL]; ok {
			continue
		}
		seen[rawURL] = struct{}{}

		content := ""
		if i < len(snippets) && len(snippets[i]) >= 2 {
			content = stripHTMLTags(snippets[i][1])
		}

		hits = append(hits, scriptports.WebSearchHit{
			Title:   title,
			URL:     rawURL,
			Content: content,
		})
	}
	return hits
}

func stripHTMLTags(s string) string {
	s = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}
