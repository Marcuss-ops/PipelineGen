package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
)

// httpFetch is a tiny HTTP GET helper used by probeScraperDeep. It
// returns (body, nil) on 2xx and (nil, wrappedErr) on transport
// failure. 4xx/5xx are returned as 2xx-shaped responses (the caller
// parses them for semantic state); the bytes are still useful.
func (p *AdminSystemProber) httpFetch(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpFetch NewRequestWithContext: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httpFetch client.Do %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("httpFetch %s: non-2xx status %s", url, http.StatusText(resp.StatusCode))
	}
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, fmt.Errorf("httpFetch %s: read body: %w", url, err)
	}
	return buf.Bytes(), nil
}

// httpPostJSON is a tiny HTTP POST-JSON helper. Mirrors httpFetch
// but accepts a JSON body and returns (bytes, status, err).
func (p *AdminSystemProber) httpPostJSON(ctx context.Context, client *http.Client, url string, body interface{}) ([]byte, int, error) {
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("httpPostJSON marshal body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("httpPostJSON NewRequestWithContext: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("httpPostJSON client.Do %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("httpPostJSON %s read body: %w", url, err)
	}
	return buf.Bytes(), resp.StatusCode, nil
}

// httpGetProbe performs an HTTP GET against the configured URL with
// the supplied client + per-probe ctx. Returns ProbeResult on 2xx,
// explicitly failed ProbeResult on 4xx/5xx, transport error on
// DNS/TCP/timeout/connection-refused.
//
// probeLabel is included in Error and Detail messages so operators
// reading the JSON output can pinpoint the failing probe URL without
// grepping the field names (e.g. "scraper: HTTP 503" rather than just
// "HTTP 503").
//
// Does NOT cache across calls (godlike/07 audit-pinning; the caller
// can wrap with an LRU if they want periodic sampling).
func (p *AdminSystemProber) httpGetProbe(ctx context.Context, client *http.Client, url, probeLabel string) artlist.ProbeResult {
	start := time.Now()
	if url == "" {
		return artlist.ProbeResult{
			OK:        false,
			Error:     probeLabel + "_url_not_configured",
			Detail:    "operator did not configure the " + probeLabel + " endpoint URL (cfg field empty)",
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	if client == nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     probeLabel + "_http_client_nil",
			Detail:    "composition root forgot to inject http.Client (godlike/07 fail-closed)",
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return artlist.ProbeResult{
			OK:        false,
			Error:     probeLabel + ": NewRequestWithContext: " + err.Error(),
			Detail:    "url=" + url + ", elapsed=" + time.Since(start).String(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		// Transport-level failure (DNS/TCP/timeout/connection-refused)
		return artlist.ProbeResult{
			OK:        false,
			Error:     probeLabel + ": client.Do " + url + ": " + err.Error(),
			Detail:    "transport failure, elapsed=" + time.Since(start).String(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return artlist.ProbeResult{
			OK:        true,
			Detail:    "url=" + url + ", status=" + http.StatusText(resp.StatusCode) + ", elapsed=" + time.Since(start).String(),
			ElapsedMs: time.Since(start).Milliseconds(),
		}
	}
	// 4xx/5xx — server reachable but unhealthy. Honest fail with
	// status code embedded for operator triage.
	return artlist.ProbeResult{
		OK:        false,
		Error:     probeLabel + ": non-2xx status " + http.StatusText(resp.StatusCode),
		Detail:    "url=" + url + ", elapsed=" + time.Since(start).String(),
		ElapsedMs: time.Since(start).Milliseconds(),
	}
}
