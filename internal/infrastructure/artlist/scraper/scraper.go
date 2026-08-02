// Package scraper owns the Node.js / Playwright scraper integration for
// Artlist live search. It exposes two Searcher implementations:
//
//   - ServerSearcher:  hits the persistent Node.js server (preferred)
//   - ExecSearcher:    falls back to spawning the Node script directly
//     via the canonical process runner (cold start, server down).
//
// Per PR2.2: the application layer is forbidden from importing os/exec
// or managing process lifecycle. Everything in this file stays here.
package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// Response mirrors the stable JSON shape written by the Artlist
// browser gateway. The application never sees this raw shape; the
// port adapter translates it into Candidate.
type Response struct {
	OK        bool   `json:"ok"`
	Term      string `json:"term"`
	Clips     []Clip `json:"clips"`
	Results   []Clip `json:"results"`
	SearchURL string `json:"search_url"`
	Saved     int    `json:"saved"`
	Provider  string `json:"provider"`
	Query     string `json:"query"`
	CacheHit  bool   `json:"cache_hit"`
	Source    string `json:"source"`
}

// Clip is the JSON shape returned by the Node scraper. The detail endpoint
// returns additional rich fields (description, creator, tags, etc.).
type Clip struct {
	ClipID       string         `json:"clip_id"`
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Creator      string         `json:"creator"`
	Country      string         `json:"country"`
	Location     string         `json:"location"`
	Tags         []string       `json:"tags"`
	Categories   []string       `json:"categories"`
	PrimaryURL   string         `json:"primary_url"`
	StreamURLs   []string       `json:"stream_urls"`
	ClipPageURL  string         `json:"clip_page_url"`
	ThumbnailURL string         `json:"thumbnail_url"`
	PreviewURL   string         `json:"preview_url"`
	RawMetadata  map[string]any `json:"raw_metadata"`
	// Optional rich metadata that the detail page may provide.
	DurationMs   int64   `json:"duration_ms"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	FPS          float64 `json:"fps"`
	LicenseClass string  `json:"license_class"`
}

// Config carries the wiring values the application owns.
type Config struct {
	ServerURL   string // empty → use exec fallback
	ScraperDir  string // directory containing artlist_search.js
	ScriptName  string // defaults to "artlist_search.js"
	ExecTimeout time.Duration
	HTTPTimeout time.Duration
}

// Provider implements artlist.Searcher using the persistent Node server
// first and the canonical process runner as a fallback.
type Provider struct {
	cfg Config
	log *zap.Logger

	searchGateMu sync.Mutex
	searchGate   chan struct{}
}

// New returns a Provider wired with the canonical process runner as the
// exec fallback. Either Source or Config may be nil; defaults are applied.
func New(cfg Config, log *zap.Logger) *Provider {
	if cfg.ScriptName == "" {
		cfg.ScriptName = "artlist_search.js"
	}
	if cfg.ExecTimeout <= 0 {
		cfg.ExecTimeout = 4 * time.Minute
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = 2 * time.Minute
	}
	if cfg.ScraperDir == "" {
		cfg.ScraperDir = "node-scraper"
	}
	return &Provider{cfg: cfg, log: log}
}

// Compile-time interface assertion so the scraper is forced to satisfy
// the application port.
var _ artapp.Searcher = (*Provider)(nil)
var _ artapp.DetailFetcher = (*Provider)(nil)

// Search tries the configured server first, falls back to exec if the
// server returns a transport-level failure. Empty results from the server
// propagate as ErrEmptyResult so the application fallback chain can try
// the next Searcher. Decisions:
//
//   - 5xx or network error     → ErrTransportFallback  → call exec and return
//   - 4xx (non-200)            → ErrInvalidResponse    → do NOT fall back
//   - 200 with ok=false or []  → ErrEmptyResult        → do NOT fall back
//
// Trim only — the 4-word cap is the application's policy
// (run_helpers.go::normalizeSearchTerm).
func (p *Provider) Search(ctx context.Context, req artapp.SearchRequest) ([]artapp.Candidate, error) {
	release, err := p.acquireSearchSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	term := strings.TrimSpace(req.Term)
	if term == "" {
		return nil, artapp.ErrEmpty
	}
	if req.Limit <= 0 {
		req.Limit = 8
	}
	if req.Limit > 50 {
		req.Limit = 50
	}

	if p.cfg.ServerURL != "" {
		resp, err := p.searchViaServer(ctx, term, req.Limit, req.ForceRefresh)
		if err != nil {
			if errors.Is(err, artapp.ErrTransportFallback) {
				if p.log != nil {
					p.log.Warn("artlist scraper server unreachable, falling back to exec",
						zap.String("url", p.cfg.ServerURL), zap.Error(err))
				}
				return p.searchViaExec(ctx, term, req.Limit)
			}
			return nil, err
		}
		if resp == nil || len(resp.Clips) == 0 {
			return nil, artapp.ErrEmptyResult
		}
		return toCandidates(resp.Clips), nil
	}
	return p.searchViaExec(ctx, term, req.Limit)
}

// acquireSearchSlot protects the browser-backed Artlist searcher from a
// fan-out of simultaneous navigations. Artlist's anti-bot layer can return a
// challenge page when several scene queries open at once; serializing this
// provider call keeps the application-level workers bounded without changing
// the shared VidRush worker policy. The wait remains context-aware.
func (p *Provider) acquireSearchSlot(ctx context.Context) (func(), error) {
	p.searchGateMu.Lock()
	if p.searchGate == nil {
		p.searchGate = make(chan struct{}, 1)
	}
	gate := p.searchGate
	p.searchGateMu.Unlock()

	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *Provider) searchViaServer(ctx context.Context, term string, limit int, forceRefresh bool) (*Response, error) {
	type searchReq struct {
		Query        string         `json:"query"`
		Term         string         `json:"term"`
		Page         int            `json:"page"`
		Limit        int            `json:"limit"`
		Filters      map[string]any `json:"filters"`
		ForceRefresh bool           `json:"force_refresh"`
	}
	body, err := json.Marshal(searchReq{
		Query:        term,
		Term:         term,
		Page:         1,
		Limit:        limit,
		Filters:      map[string]any{},
		ForceRefresh: forceRefresh,
	})
	if err != nil {
		return nil, fmt.Errorf("artlist server: marshal request: %w", err)
	}
	reqURL := strings.TrimRight(p.cfg.ServerURL, "/") + "/v1/clips/search"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("artlist server: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: p.cfg.HTTPTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		// Context cancellation / deadline must propagate to callers
		// instead of being converted into a transport fallback.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", artapp.ErrTransportFallback, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w: status %d", artapp.ErrRateLimited, resp.StatusCode)
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: status %d", artapp.ErrTransportFallback, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", artapp.ErrInvalidResponse, resp.StatusCode)
	}

	var response Response
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: %v", artapp.ErrInvalidResponse, err)
	}
	if err := normalizeGatewayResponse(&response, term); err != nil {
		return nil, err
	}
	if !response.OK {
		// The server reports ok=false with no error string → empty result.
		return &response, nil
	}
	return &response, nil
}

func (p *Provider) searchViaExec(ctx context.Context, term string, limit int) ([]artapp.Candidate, error) {
	scraperDir := p.cfg.ScraperDir
	if abs, err := filepath.Abs(scraperDir); err == nil {
		scraperDir = abs
	}
	scriptPath := filepath.Join(scraperDir, p.cfg.ScriptName)

	if !process.CommandExists("node") {
		return nil, fmt.Errorf("%w: node not found in PATH", artapp.ErrUnavailable)
	}

	args := []string{scriptPath, "--term", term, "--limit", strconv.Itoa(limit)}
	if p.log != nil {
		p.log.Info("Running live Artlist search (exec)", zap.String("term", term), zap.Int("limit", limit), zap.String("script_path", scriptPath))
	}

	result, err := process.Run(ctx, "node", args, process.Options{
		WorkDir:        scraperDir,
		Timeout:        p.cfg.ExecTimeout,
		CombinedOutput: true,
	})
	if err != nil {
		mapped := artapp.ErrUnavailable
		if result != nil && result.TimedOut {
			mapped = artapp.ErrTimeout
		}
		return nil, fmt.Errorf("%w: scraper failed: %v", mapped, err)
	}

	var response Response
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		return nil, fmt.Errorf("%w: %v", artapp.ErrInvalidResponse, err)
	}
	if len(response.Clips) == 0 {
		return nil, artapp.ErrEmptyResult
	}
	return toCandidates(response.Clips), nil
}

func toCandidates(clips []Clip) []artapp.Candidate {
	out := make([]artapp.Candidate, 0, len(clips))
	for _, c := range clips {
		out = append(out, clipToCandidate(c))
	}
	return out
}

func clipToCandidate(c Clip) artapp.Candidate {
	id := firstNonEmpty(c.ClipID, c.ID)
	title := firstNonEmpty(c.Title, c.Name, id)
	raw := make(map[string]any, len(c.RawMetadata)+2)
	for k, v := range c.RawMetadata {
		raw[k] = v
	}
	if c.Country != "" {
		raw["country"] = c.Country
	}
	if c.Location != "" {
		raw["location"] = c.Location
	}
	if len(c.StreamURLs) > 0 {
		raw["stream_urls"] = c.StreamURLs
	}

	candidate := artapp.Candidate{
		Provider:     "artlist",
		ExternalID:   id,
		ID:           id,
		Title:        title,
		Description:  c.Description,
		Creator:      c.Creator,
		SourceRef:    firstNonEmpty(c.PrimaryURL, c.PreviewURL, c.ClipPageURL),
		PageURL:      c.ClipPageURL,
		ThumbnailURL: c.ThumbnailURL,
		PreviewURL:   firstNonEmpty(c.PreviewURL, c.PrimaryURL, c.ClipPageURL),
		Keywords:     c.Tags,
		Categories:   c.Categories,
		RawMetadata:  raw,
		SourceName:   "artlist",
		MediaType:    asset.MediaType("video"),
	}
	if c.DurationMs > 0 {
		candidate.DurationMs = c.DurationMs
		candidate.Duration = time.Duration(c.DurationMs) * time.Millisecond
	}
	candidate.Width = c.Width
	candidate.Height = c.Height
	candidate.FPS = c.FPS
	candidate.FPSNumerator = int(c.FPS)
	if c.FPS > 0 {
		candidate.FPSDenominator = 1
	}
	candidate.LicenseClass = c.LicenseClass
	return candidate
}

func normalizeGatewayResponse(response *Response, fallbackTerm string) error {
	if response == nil {
		return nil
	}
	if response.Provider != "" && response.Provider != "artlist" {
		return fmt.Errorf("%w: unexpected provider %q", artapp.ErrInvalidResponse, response.Provider)
	}
	if response.Query == "" {
		response.Query = fallbackTerm
	}
	if response.Term == "" {
		response.Term = response.Query
	}
	if response.SearchURL == "" && response.Query != "" {
		response.SearchURL = "https://artlist.io/stock-footage/search?terms=" + url.QueryEscape(response.Query)
	}
	if len(response.Clips) == 0 && len(response.Results) > 0 {
		response.Clips = response.Results
	}
	if response.Source == "" {
		if response.CacheHit {
			response.Source = "sqlite"
		} else if len(response.Clips) > 0 {
			response.Source = "browser_api"
		}
	}
	return nil
}

// DetailResponse mirrors the Node scraper POST /detail JSON envelope.
type DetailResponse struct {
	OK   bool `json:"ok"`
	Clip Clip `json:"clip"`
}

// FetchDetails calls the Node scraper /detail endpoint to hydrate a single
// Artlist clip page. It satisfies artlist.DetailFetcher.
func (p *Provider) FetchDetails(ctx context.Context, clipPageURL string) (*artapp.Candidate, error) {
	clipPageURL = strings.TrimSpace(clipPageURL)
	if clipPageURL == "" {
		return nil, artapp.ErrEmpty
	}

	if p.cfg.ServerURL == "" {
		return nil, fmt.Errorf("%w: detail fetch requires a configured scraper server URL", artapp.ErrUnavailable)
	}

	type detailReq struct {
		ClipPageURL string `json:"clip_page_url"`
	}
	body, err := json.Marshal(detailReq{ClipPageURL: clipPageURL})
	if err != nil {
		return nil, fmt.Errorf("artlist server: marshal detail request: %w", err)
	}

	reqURL := strings.TrimRight(p.cfg.ServerURL, "/") + "/detail"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("artlist server: build detail request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: p.cfg.HTTPTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", artapp.ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, artapp.ErrNotFound
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: scraper server returned %d", artapp.ErrUnavailable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", artapp.ErrInvalidResponse, resp.StatusCode)
	}

	var detail DetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("%w: %v", artapp.ErrInvalidResponse, err)
	}
	if !detail.OK || detail.Clip.ClipPageURL == "" {
		return nil, artapp.ErrEmptyResult
	}

	c := clipToCandidate(detail.Clip)
	return &c, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
