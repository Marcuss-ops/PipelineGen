// Package diagnostics — system_prober_http.go: 4 real upstream
// HTTP-based probes (scraper 2-stage deep + browser + session +
// downloader via httpGetProbe) + their helpers + the Stage-2
// freshness constant (Step 4 follow-up, July 2026).
//
// godlike/06 SSOT: the 4 Commit-1 upstream probes (scraper / browser /
// session / downloader) all do real HTTP reachability checks against
// the Node scraper /health endpoint. They share the HTTP
// code path (httpGetProbe for the simpler 3 probes; probeScraperDeep
// for the deep 2-stage scraper probe with its helpers). Co-locating
// them in this file keeps the HTTP/scraper seam self-contained —
// a future addition of another HTTP-based probe lands here, not in
// canonical.
//
// godlike/07 NO-FAKE-AVAILABILITY §22: every stage of probeScraperDeep
// is gated on a REAL signal (parse the /health JSON, verify the
// browser_running boolean, parse the last_session_alive_at
// timestamp). No probe is ever reported as passing without exercising
// the dependency.
package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	artlist "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
)

// Stage2SessionFreshnessWindow is the canonical Stage-2 freshness
// window for last_session_alive_at. The Node scraper emits freshness
// every 30s (HB_INTERVAL_MS) and considers itself stale at 60s
// (HB_FRESH_WINDOW_MS). We probe at 90s to allow one heartbeat miss
// + transport roundtrip + clock skew between the Go probe and the
// Node scraper. Tightening to 60s would mirror the Node scraper's
// internal verdict; widening to 120s would let two consecutive
// heartbeats miss before we alarm — both are out of scope for this
// commit. Operators wanting tighter windows can override via
// AdminSystemProber.Stage2FreshnessWindow (forward-compat knob).
const Stage2SessionFreshnessWindow = 90 * time.Second

// ── Real scraper 2-stage probe (Fase 7 / Commit B, July 2026) ──
//
// godlike/07 NO-FAKE-AVAILABILITY §22: the shallow HTTP GET probe
// (which only verified "the server is up and returned some response")
// was identified as the §22 anti-pattern — a scraper container can be
// alive (HTTP 200 from /) yet completely non-functional (browser
// crashed, no Artlist session).
//
// probeScraperDeep replaces that shallow probe with a 2-stage deep
// probe that verifies the canonical artlist.SetupCheck contract
// (Chromium started + session valid):
//
//	stage_1_chromium_started  GET <scraperURL>/health
//	                           parse JSON, verify browser_running == true
//	stage_2_session_valid      GET <scraperURL>/health
//	                           parse JSON, verify last_launch_error == null
//	                           AND last_session_alive_at is recent
//	                           (parsed + within 90s of now)
//
// The real query capability is intentionally NOT exercised here.
// Live Artlist searches are slow and are verified by the dedicated
// /api/artlist/search/live endpoint. Keeping diagnostics synchronous
// and fast prevents false-positive timeouts while /search/live
// remains the honest, end-to-end probe.
//
// Aggregate semantics:
//
//	ProbeResult.OK = (Stages[0].OK && Stages[1].OK)
//	ProbeResult.Error = the verbatim Error of the FIRST failing stage
//	                    (operators walk Stages in declaration order
//	                    to find the failing stage; first fails wins)
//	ProbeResult.Stages = the full 2-stage slice (always 2 entries,
//	                    even on parse failure or transport error)
//	ProbeResult.ElapsedMs = wall-clock sum of stage measurements
//
// godlike/06 SSOT: the ProbeStage shape lives in
// internal/application/assets/providers/artlist/types.go (the
// canonical wire-by-wire site). probeScraperDeep here is the SOLE
// producer of ProbeStage values for the Scraper probe — adding
// parallel per-stage checks elsewhere would be a godlike/06
// violation. godlike/07 fail-closed: every stage is gated on a
// REAL signal (parse the JSON, verify the boolean, parse the
// timestamp); no probe is ever reported as passing without exercising
// the dependency.
func (p *AdminSystemProber) probeScraperDeep(ctx context.Context, client *http.Client, serverURL string) artlist.ProbeResult {
	start := time.Now()
	if serverURL == "" {
		// Empty URL: ALL 2 stages fail with the same operator-config error.
		// godlike/07 distinguishes "operator did not configure" from
		// "configured but broken" — every stage surfaces the same error
		// so the aggregate AND is false and the failing stage surfaces
		// the operator-actionable advice.
		err := "scraper_url_not_configured"
		detail := "operator did not configure the scraper endpoint URL (cfg.External.ArtlistScraperServerURL is empty)"
		return artlist.ProbeResult{
			OK:        false,
			Error:     err,
			Detail:    detail,
			ElapsedMs: time.Since(start).Milliseconds(),
			Stages: []artlist.ProbeStage{
				{Name: "stage_1_chromium_started", OK: false, Error: err, Detail: detail},
				{Name: "stage_2_session_valid", OK: false, Error: err, Detail: detail},
			},
		}
	}
	if client == nil {
		err := "scraper_http_client_nil"
		detail := "composition root forgot to inject http.Client (godlike/07 fail-closed)"
		return artlist.ProbeResult{
			OK:        false,
			Error:     err,
			Detail:    detail,
			ElapsedMs: time.Since(start).Milliseconds(),
			Stages: []artlist.ProbeStage{
				{Name: "stage_1_chromium_started", OK: false, Error: err, Detail: detail},
				{Name: "stage_2_session_valid", OK: false, Error: err, Detail: detail},
			},
		}
	}

	baseURL := strings.TrimRight(serverURL, "/")
	healthURL := baseURL + "/health"

	stages := make([]artlist.ProbeStage, 2)

	// ── Stage 1 + 2 share the /health response (parse once, evaluate
	// both predicates from the JSON). A single HTTP roundtrip covers
	// both stages. The real query capability is intentionally not
	// exercised here; it belongs to the dedicated /api/artlist/search/live
	// endpoint.
	stage1Start := time.Now()
	resp1Body, stage1Err := p.httpFetch(ctx, client, healthURL)
	stages[0].ElapsedMs = time.Since(stage1Start).Milliseconds()
	if stage1Err != nil {
		stages[0].Name = "stage_1_chromium_started"
		stages[0].OK = false
		stages[0].Error = stage1Err.Error()
		stages[0].Detail = "GET " + healthURL + " failed: transport error — chromium actual-launch state cannot be observed (godlike/07 fail-closed)"
		// Short-circuit: stage 2 depends on /health, mark it as
		// unavailable with the cascading error so operators see the
		// causal chain.
		stages[1].Name = "stage_2_session_valid"
		stages[1].OK = false
		stages[1].Error = "skipped: stage_1_chromium_started transport failure — cannot evaluate session state without /health response"
		return aggregateScraperStages(stages, time.Since(start).Milliseconds())
	}

	// Parse /health JSON (Node scraper emits: ok, healthy, browser_running,
	// browser_pid, last_launch_error, last_search_at, last_session_alive_at,
	// uptime_seconds, requests_served, started_at, port).
	var health struct {
		OK                 bool   `json:"ok"`
		Healthy            bool   `json:"healthy"`
		BrowserRunning     bool   `json:"browser_running"`
		BrowserPid         *int   `json:"browser_pid"`
		LastLaunchError    string `json:"last_launch_error"`
		LastSessionAliveAt string `json:"last_session_alive_at"`
		LastSearchAt       string `json:"last_search_at"`
	}
	if err := json.Unmarshal(resp1Body, &health); err != nil {
		stages[0].Name = "stage_1_chromium_started"
		stages[0].OK = false
		stages[0].Error = "stage_1_chromium_started: /health response unparseable: " + err.Error()
		stages[0].Detail = "GET " + healthURL + " returned non-JSON or malformed payload (node scraper may be a different version)"
		stages[1].Name = "stage_2_session_valid"
		stages[1].OK = false
		stages[1].Error = "skipped: stage_1_chromium_started JSON parse failure"
		return aggregateScraperStages(stages, time.Since(start).Milliseconds())
	}

	// Stage 1: chromium started = browser_running == true.
	stages[0].Name = "stage_1_chromium_started"
	stages[0].OK = health.BrowserRunning
	if stages[0].OK {
		stages[0].Detail = "browser_running=true (chromium process up; " + formatHealthDetail(health.BrowserPid, health.LastSearchAt) + ")"
	} else {
		stages[0].Error = "stage_1_chromium_started: browser_running=false (chromium not started or crashed)"
		stages[0].Detail = formatHealthDetail(health.BrowserPid, health.LastSearchAt)
	}

	// Stage 2: session valid = last_launch_error empty AND last_session_alive_at recent.
	// Recent window: 90s — wider than the Node scraper's 60s freshness
	// window to allow for roundtrip latency + clock skew between the
	// Go probe and the Node scraper.
	stages[1].Name = "stage_2_session_valid"
	if health.LastLaunchError != "" {
		stages[1].OK = false
		stages[1].Error = "stage_2_session_valid: last_launch_error=\"" + health.LastLaunchError + "\" (artlist session may be invalid; operator should re-login)"
		stages[1].Detail = "artlist launch recorded an error; chromium up but session may be stale"
	} else if health.LastSessionAliveAt == "" {
		stages[1].OK = false
		stages[1].Error = "stage_2_session_valid: last_session_alive_at is empty (heartbeat never ran; session liveness unobservable)"
		stages[1].Detail = "node scraper has not yet emitted a heartbeat; cannot confirm session liveness within 90s window"
	} else {
		aliveAt, parseErr := time.Parse(time.RFC3339Nano, health.LastSessionAliveAt)
		if parseErr != nil {
			stages[1].OK = false
			stages[1].Error = "stage_2_session_valid: last_session_alive_at unparseable: " + parseErr.Error()
			stages[1].Detail = "value=\"" + health.LastSessionAliveAt + "\""
		} else if aliveAt.After(time.Now()) {
			// godlike/07 fail-closed: a future timestamp signals clock
			// skew between the Go probe host and the Node scraper
			// container, NOT a healthy session. Reject explicitly
			// rather than letting `time.Since(aliveAt) < 0` silently
			// pass the staleness check.
			stages[1].OK = false
			stages[1].Error = "stage_2_session_valid: last_session_alive_at is in the future (" + aliveAt.Format(time.RFC3339Nano) + " > now=" + time.Now().Format(time.RFC3339Nano) + ") — clock skew between probe host and node scraper? (godlike/07 fail-closed: a future timestamp is NOT proof of session liveness)"
			stages[1].Detail = "value=\"" + health.LastSessionAliveAt + "\" rejected as future-dated"
		} else if time.Since(aliveAt) > Stage2SessionFreshnessWindow {
			stages[1].OK = false
			stages[1].Error = "stage_2_session_valid: last_session_alive_at is stale (" + time.Since(aliveAt).Round(time.Second).String() + " ago, window=" + Stage2SessionFreshnessWindow.String() + ")"
			stages[1].Detail = "heartbeat has not refreshed within the freshness window; session liveness uncertain"
		} else {
			stages[1].OK = true
			stages[1].Detail = "last_launch_error empty; last_session_alive_at=" + health.LastSessionAliveAt + " (" + time.Since(aliveAt).Round(time.Second).String() + " ago, window=" + Stage2SessionFreshnessWindow.String() + ")"
		}
	}

	return aggregateScraperStages(stages, time.Since(start).Milliseconds())
}

// aggregateScraperStages consolidates the 2-stage ProbeStage slice
// into the aggregate ProbeResult. godlike/07 contract:
//   - OK = strict AND of all Stages[i].OK
//   - Error = the verbatim Error of the FIRST failing stage
//   - Detail = per-stage summary on success; first-failing-stage
//     detail on failure
//   - ElapsedMs = wall-clock (sum of stages)
//   - Stages = the verbatim 2-stage slice (always len == 2)
//
// godlike/06 SSOT: this is the SINGLE canonical aggregation rule for
// the Scraper probe stages. Adding parallel aggregation elsewhere
// would be a godlike/06 violation.
func aggregateScraperStages(stages []artlist.ProbeStage, elapsedMs int64) artlist.ProbeResult {
	out := artlist.ProbeResult{
		ElapsedMs: elapsedMs,
		Stages:    stages,
	}
	allOK := true
	var firstFailErr string
	var summaryDetail []string
	for _, s := range stages {
		summaryDetail = append(summaryDetail, s.Name+"="+boolToOK(s.OK))
		if !s.OK {
			allOK = false
			if firstFailErr == "" {
				firstFailErr = s.Error
				out.Detail = s.Name + ": " + s.Detail
			}
		}
	}
	out.OK = allOK
	out.Error = firstFailErr
	if allOK {
		out.Detail = "all 2 stages passed: " + strings.Join(summaryDetail, ", ")
	}
	return out
}

// boolToOK is a tiny helper to keep aggregateScraperStages readable.
func boolToOK(b bool) string {
	if b {
		return "ok"
	}
	return "fail"
}

// formatHealthDetail is the per-stage-1 / per-failure-detail helper
// that surfaces the Node scraper's pid + last_search_at so operators
// can ps(1) the running Chromium from a probe failure alone.
func formatHealthDetail(pid *int, lastSearchAt string) string {
	pidStr := "nil"
	if pid != nil {
		pidStr = strconv.Itoa(*pid)
	}
	return "browser_pid=" + pidStr + ", last_search_at=" + lastSearchAt
}

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
