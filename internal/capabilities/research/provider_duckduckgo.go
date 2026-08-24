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
package research

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

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
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
// ddgMaxAttempts bounds the total requests per query (1 initial attempt +
// 2 retries). Retries are reserved for transient failures (timeout, network,
// 5xx, anomaly challenge); a persistent failure stops early and lets the
// coordinator fall through to the next provider without burning the whole
// resolve budget.
const ddgMaxAttempts = 3

type DuckDuckGoSearchProvider struct {
	client      *http.Client
	log         *zap.Logger
	backoff     func(attempt int) time.Duration // delay before a transient-failure retry (injectable for tests)
	maxAttempts int
}

// NewDuckDuckGoSearchProvider creates a DDG text search provider with a
// 10-second timeout, up to 2 retries on transient failures, and jittered
// exponential backoff.
func NewDuckDuckGoSearchProvider(log *zap.Logger) *DuckDuckGoSearchProvider {
	if log == nil {
		log = zap.NewNop()
	}
	return &DuckDuckGoSearchProvider{
		client:      &http.Client{Timeout: 10 * time.Second},
		log:         log,
		backoff:     defaultDDGBackoff,
		maxAttempts: ddgMaxAttempts,
	}
}

// defaultDDGBackoff returns a jittered exponential delay for a retry.
// attempt is the 0-based retry index (0 = first retry): ~500ms, ~1s, ~2s.
func defaultDDGBackoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	base := 500 * time.Millisecond
	for i := 0; i < attempt; i++ {
		base *= 2
		if base > 2*time.Second {
			base = 2 * time.Second
			break
		}
	}
	return base + time.Duration(rand.Float64()*float64(base))
}

// isDDGAnomalyChallenge reports whether the status code is DDG's
// anti-bot anomaly challenge (202 challenge page or 403 access denied).
func isDDGAnomalyChallenge(status int) bool {
	return status == http.StatusAccepted || status == http.StatusForbidden
}

// isDDGTransient reports whether a failed attempt is worth retrying:
//   - status 0 with an error → network error / timeout before a response;
//   - 202/403 → DDG anti-bot anomaly challenge (transient per-IP);
//   - 5xx → DDG server error.
//
// A definitive 4xx (e.g. 400/404/410) is not transient and fails fast.
func isDDGTransient(status int, err error) bool {
	if err == nil {
		return false
	}
	if status == 0 {
		return true
	}
	return isDDGAnomalyChallenge(status) || status >= http.StatusInternalServerError
}

func (d *DuckDuckGoSearchProvider) Name() string { return "duckduckgo" }

func (d *DuckDuckGoSearchProvider) Search(ctx context.Context, query string, limit int) ([]scriptports.WebSearchHit, error) {
	if d == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	maxAttempts := d.maxAttempts
	if maxAttempts <= 0 {
		maxAttempts = ddgMaxAttempts
	}

	var lastErr error
	var lastStatus int
	for attempt := 0; attempt < maxAttempts; attempt++ {
		hits, status, err := d.searchOnce(ctx, query, limit)
		if err == nil {
			if attempt > 0 {
				d.log.Info("ddg: recovered after transient-failure retry",
					zap.Int("attempts", attempt+1),
					zap.Int("initial_status", lastStatus),
				)
			}
			return hits, nil
		}
		lastErr, lastStatus = err, status

		// Transient failures (timeout, network, 5xx, anomaly challenge) are
		// retried with backoff. A definitive 4xx or the final attempt fails
		// fast and lets the coordinator fall through to the next provider.
		if !isDDGTransient(status, err) || attempt == maxAttempts-1 {
			break
		}

		d.log.Warn("ddg: transient failure, backing off before retry",
			zap.Int("attempt", attempt+1),
			zap.Int("status", status),
			zap.Error(err),
		)
		delay := d.backoff(attempt)
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, fmt.Errorf("ddg search: %w", ctx.Err())
			}
		}
	}

	if isDDGAnomalyChallenge(lastStatus) {
		d.log.Warn("ddg: anomaly challenge persists after retries, provider down", zap.Int("status", lastStatus))
	}
	return nil, lastErr
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
