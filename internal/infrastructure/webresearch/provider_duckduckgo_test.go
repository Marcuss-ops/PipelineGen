// Package webresearch — provider_duckduckgo_test.go covers the DDG HTML
// lite parser: result extraction, uddg redirect decoding, internal
// dedup, malformed-HTML resilience, and the empty-query guard. The
// network path is deliberately not exercised here (best-effort fallback,
// hermetic unit tests only).
package webresearch

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// roundTripFunc adapts a function to http.RoundTripper so tests can
// script DDG responses without a network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func ddgResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newDDGProviderScripted(t *testing.T, firstStatus int, laterStatus int) (*DuckDuckGoSearchProvider, *int) {
	t.Helper()
	p := NewDuckDuckGoSearchProvider(zap.NewNop())
	p.backoff = func(int) time.Duration { return 0 } // keep tests instant
	calls := 0
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return ddgResponse(firstStatus, "anti-bot challenge page"), nil
		}
		if laterStatus == http.StatusOK {
			return ddgResponse(http.StatusOK, ddgSampleHTML), nil
		}
		return ddgResponse(laterStatus, "still challenged"), nil
	})
	return p, &calls
}

// ddgSampleHTML mirrors the structure of html.duckduckgo.com/html/:
// result__a links (wrapped in a uddg redirect) plus result__snippet
// anchors with optional inline markup.
const ddgSampleHTML = `<html><body>
<div class="result results_links results_links_deep web-result ">
  <h2 class="result__title">
    <a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fen.wikipedia.org%2Fwiki%2FFloyd_Mayweather_Jr.&rut=abc123">Floyd Mayweather Jr. - Wikipedia</a>
  </h2>
  <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fen.wikipedia.org%2Fwiki%2FFloyd_Mayweather_Jr.&rut=abc123">Floyd Mayweather Jr. is an American <b>boxing</b> promoter and former professional boxer.</a>
</div>
<div class="result results_links results_links_deep web-result ">
  <h2 class="result__title">
    <a rel="nofollow" class="result__a" href="https://www.boxingscene.com/floyd-mayweather-net-worth">Floyd Mayweather Net Worth</a>
  </h2>
  <a class="result__snippet" href="https://www.boxingscene.com/floyd-mayweather-net-worth">Career earnings and endorsements of Floyd Mayweather.</a>
</div>
</body></html>`

func TestParseDDGHTML_ExtractsHits(t *testing.T) {
	hits := parseDDGHTML(ddgSampleHTML)
	if len(hits) != 2 {
		t.Fatalf("hit count = %d, want 2", len(hits))
	}

	first := hits[0]
	if first.Title != "Floyd Mayweather Jr. - Wikipedia" {
		t.Errorf("title = %q, want %q", first.Title, "Floyd Mayweather Jr. - Wikipedia")
	}
	if first.URL != "https://en.wikipedia.org/wiki/Floyd_Mayweather_Jr." {
		t.Errorf("url = %q, want decoded uddg target", first.URL)
	}
	if first.Content != "Floyd Mayweather Jr. is an American boxing promoter and former professional boxer." {
		t.Errorf("content = %q, want stripped snippet text", first.Content)
	}

	second := hits[1]
	if second.Title != "Floyd Mayweather Net Worth" {
		t.Errorf("title = %q, want %q", second.Title, "Floyd Mayweather Net Worth")
	}
	if second.URL != "https://www.boxingscene.com/floyd-mayweather-net-worth" {
		t.Errorf("url = %q, want plain URL kept as-is", second.URL)
	}
}

func TestParseDDGHTML_DecodesUddgRedirect(t *testing.T) {
	html := `<a rel="nofollow" class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage%3Futm_source%3Dddg&rut=xyz">Example Page</a>`
	hits := parseDDGHTML(html)
	if len(hits) != 1 {
		t.Fatalf("hit count = %d, want 1", len(hits))
	}
	if hits[0].URL != "https://example.com/page?utm_source=ddg" {
		t.Errorf("url = %q, want uddg query decoded", hits[0].URL)
	}
}

func TestParseDDGHTML_DedupsSameURL(t *testing.T) {
	html := `<a class="result__a" href="https://example.com/a">First</a>
<a class="result__a" href="https://example.com/a">Second</a>`
	hits := parseDDGHTML(html)
	if len(hits) != 1 {
		t.Fatalf("hit count = %d, want 1 (same URL deduped)", len(hits))
	}
	if hits[0].Title != "First" {
		t.Errorf("title = %q, want first occurrence kept", hits[0].Title)
	}
}

func TestParseDDGHTML_MalformedHTMLNoCrash(t *testing.T) {
	for _, input := range []string{"", "<html>garbage without results</html>", "<a class=\"result__a\" href=\"\"></a>", "<a class=\"result__a\" href=\"https://example.com\"></a>"} {
		hits := parseDDGHTML(input)
		if len(hits) != 0 {
			t.Errorf("input %q: hit count = %d, want 0", input, len(hits))
		}
	}
}

func TestParseDDGHTML_SnippetMissingStillKeepsHit(t *testing.T) {
	html := `<a class="result__a" href="https://example.com/no-snippet">No Snippet Title</a>`
	hits := parseDDGHTML(html)
	if len(hits) != 1 {
		t.Fatalf("hit count = %d, want 1", len(hits))
	}
	if hits[0].Content != "" {
		t.Errorf("content = %q, want empty when no snippet", hits[0].Content)
	}
}

func TestDuckDuckGoSearchProvider_EmptyQueryNoNetwork(t *testing.T) {
	p := NewDuckDuckGoSearchProvider(zap.NewNop())
	hits, err := p.Search(context.Background(), "   ", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("hits = %v, want nil for empty query", hits)
	}
}

func TestDuckDuckGoSearchProvider_NilReceiver(t *testing.T) {
	var p *DuckDuckGoSearchProvider
	hits, err := p.Search(context.Background(), "query", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil {
		t.Errorf("hits = %v, want nil from nil receiver", hits)
	}
}

func TestDuckDuckGoSearchProvider_RetriesOnceOn202Challenge(t *testing.T) {
	p, calls := newDDGProviderScripted(t, http.StatusAccepted, http.StatusOK)
	hits, err := p.Search(context.Background(), "floyd mayweather", 5)
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if *calls != 2 {
		t.Errorf("round trips = %d, want 2 (initial 202 + one retry)", *calls)
	}
	if len(hits) != 2 {
		t.Errorf("hit count = %d, want 2 parsed from retried response", len(hits))
	}
}

func TestDuckDuckGoSearchProvider_RetriesOnceOn403Challenge(t *testing.T) {
	p, calls := newDDGProviderScripted(t, http.StatusForbidden, http.StatusOK)
	hits, err := p.Search(context.Background(), "canelo alvarez", 5)
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if *calls != 2 {
		t.Errorf("round trips = %d, want 2 (initial 403 + one retry)", *calls)
	}
	if len(hits) != 2 {
		t.Errorf("hit count = %d, want 2 parsed from retried response", len(hits))
	}
}

func TestDuckDuckGoSearchProvider_AnomalyChallengePersists_ReturnsError(t *testing.T) {
	p, calls := newDDGProviderScripted(t, http.StatusAccepted, http.StatusForbidden)
	hits, err := p.Search(context.Background(), "roberto duran", 5)
	if err == nil {
		t.Fatalf("expected error when the challenge persists, got hits=%d", len(hits))
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error = %q, want the retried 403 status", err.Error())
	}
	if *calls != 3 {
		t.Errorf("round trips = %d, want exactly 3 (two retries, then give up)", *calls)
	}
	if hits != nil {
		t.Errorf("hits = %v, want nil on persistent challenge", hits)
	}
}

func TestDuckDuckGoSearchProvider_NoRetryOnDefinitiveClientError(t *testing.T) {
	p, calls := newDDGProviderScripted(t, http.StatusNotFound, http.StatusOK)
	hits, err := p.Search(context.Background(), "joe frazier", 5)
	if err == nil {
		t.Fatalf("expected error on 404, got hits=%d", len(hits))
	}
	if *calls != 1 {
		t.Errorf("round trips = %d, want 1 (404 is a definitive client error, not retried)", *calls)
	}
	if hits != nil {
		t.Errorf("hits = %v, want nil on 404", hits)
	}
}

func TestDuckDuckGoSearchProvider_RetriesOnServerError(t *testing.T) {
	p, calls := newDDGProviderScripted(t, http.StatusInternalServerError, http.StatusOK)
	hits, err := p.Search(context.Background(), "joe frazier", 5)
	if err != nil {
		t.Fatalf("unexpected error after 5xx retry: %v", err)
	}
	if *calls != 2 {
		t.Errorf("round trips = %d, want 2 (initial 500 + one retry)", *calls)
	}
	if len(hits) != 2 {
		t.Errorf("hit count = %d, want 2 parsed from retried response", len(hits))
	}
}

func TestDuckDuckGoSearchProvider_RetriesOnTimeout(t *testing.T) {
	p := NewDuckDuckGoSearchProvider(zap.NewNop())
	p.backoff = func(int) time.Duration { return 0 }
	calls := 0
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, context.DeadlineExceeded // simulate the reported per-query timeout
		}
		return ddgResponse(http.StatusOK, ddgSampleHTML), nil
	})

	hits, err := p.Search(context.Background(), "joe frazier", 5)
	if err != nil {
		t.Fatalf("unexpected error after timeout retry: %v", err)
	}
	if calls != 2 {
		t.Errorf("round trips = %d, want 2 (timeout + one retry)", calls)
	}
	if len(hits) != 2 {
		t.Errorf("hit count = %d, want 2 parsed from retried response", len(hits))
	}
}

func TestDuckDuckGoSearchProvider_RetryBackoffRespectsContextCancel(t *testing.T) {
	p := NewDuckDuckGoSearchProvider(zap.NewNop())
	p.backoff = func(int) time.Duration { return time.Hour } // would block forever if not cancelled
	calls := 0
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return ddgResponse(http.StatusAccepted, "challenge"), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the search starts
	hits, err := p.Search(ctx, "tyson fury", 5)
	if err == nil {
		t.Fatalf("expected context error, got hits=%d", len(hits))
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("error = %q, want context canceled", err.Error())
	}
	if calls != 1 {
		t.Errorf("round trips = %d, want 1 (backoff aborted before retry)", calls)
	}
}
