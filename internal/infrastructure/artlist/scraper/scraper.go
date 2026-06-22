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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"go.uber.org/zap"
)

// Response mirrors the JSON shape written by artlist_search.js /
// artlist_server.js. The application never sees this raw shape; the
// port adapter translates it into Candidate.
type Response struct {
	OK        bool     `json:"ok"`
	Term      string   `json:"term"`
	Clips     []Clip   `json:"clips"`
	SearchURL string   `json:"search_url"`
	Saved     int      `json:"saved"`
}

// Clip is the JSON shape returned by the Node scraper.
type Clip struct {
	ClipID      string   `json:"clip_id"`
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Name        string   `json:"name"`
	PrimaryURL  string   `json:"primary_url"`
	StreamURLs  []string `json:"stream_urls"`
	ClipPageURL string   `json:"clip_page_url"`
}

// Config carries the wiring values the application owns.
type Config struct {
	ServerURL    string // empty → use exec fallback
	ScraperDir   string // directory containing artlist_search.js
	ScriptName   string // defaults to "artlist_search.js"
	ExecTimeout  time.Duration
	HTTPTimeout  time.Duration
}

// Provider implements artlist.Searcher using the persistent Node server
// first and the canonical process runner as a fallback.
type Provider struct {
	cfg Config
	log *zap.Logger
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
var _ artlist.Searcher = (*Provider)(nil)

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
func (p *Provider) Search(ctx context.Context, req artlist.SearchRequest) ([]artlist.Candidate, error) {
	term := strings.TrimSpace(req.Term)
	if term == "" {
		return nil, artlist.ErrEmpty
	}
	if req.Limit <= 0 {
		req.Limit = 8
	}
	if req.Limit > 50 {
		req.Limit = 50
	}

	if p.cfg.ServerURL != "" {
		resp, err := p.searchViaServer(ctx, term, req.Limit)
		if err != nil {
			if errors.Is(err, artlist.ErrTransportFallback) {
				if p.log != nil {
					p.log.Warn("artlist scraper server unreachable, falling back to exec",
						zap.String("url", p.cfg.ServerURL), zap.Error(err))
				}
				return p.searchViaExec(ctx, term, req.Limit)
			}
			return nil, err
		}
		if resp == nil || len(resp.Clips) == 0 {
			return nil, artlist.ErrEmptyResult
		}
		return toCandidates(resp.Clips), nil
	}
	return p.searchViaExec(ctx, term, req.Limit)
}



func (p *Provider) searchViaServer(ctx context.Context, term string, limit int) (*Response, error) {
	type searchReq struct {
		Term  string `json:"term"`
		Limit int    `json:"limit"`
	}
	body, err := json.Marshal(searchReq{Term: term, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("artlist server: marshal request: %w", err)
	}
	reqURL := strings.TrimRight(p.cfg.ServerURL, "/") + "/search"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("artlist server: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: p.cfg.HTTPTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", artlist.ErrTransportFallback, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: status %d", artlist.ErrTransportFallback, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", artlist.ErrInvalidResponse, resp.StatusCode)
	}

	var response Response
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("%w: %v", artlist.ErrInvalidResponse, err)
	}
	if !response.OK {
		// The server reports ok=false with no error string → empty result.
		return &response, nil
	}
	return &response, nil
}

func (p *Provider) searchViaExec(ctx context.Context, term string, limit int) ([]artlist.Candidate, error) {
	scraperDir := p.cfg.ScraperDir
	if abs, err := filepath.Abs(scraperDir); err == nil {
		scraperDir = abs
	}
	scriptPath := filepath.Join(scraperDir, p.cfg.ScriptName)

	if !process.CommandExists("node") {
		return nil, fmt.Errorf("%w: node not found in PATH", artlist.ErrUnavailable)
	}

	args := []string{scriptPath, "--term", term, "--limit", strconv.Itoa(limit)}
	if p.log != nil {
		p.log.Info("Running live Artlist search (exec)", zap.String("term", term), zap.Int("limit", limit), zap.String("script_path", scriptPath))
	}

	result, err := process.Run(ctx, "node", args, process.Options{
		WorkDir:       scraperDir,
		Timeout:       p.cfg.ExecTimeout,
		CombinedOutput: true,
	})
	if err != nil {
		mapped := artlist.ErrUnavailable
		if result != nil && result.TimedOut {
			mapped = artlist.ErrTimeout
		}
		return nil, fmt.Errorf("%w: scraper failed: %v", mapped, err)
	}

	var response Response
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		return nil, fmt.Errorf("%w: %v", artlist.ErrInvalidResponse, err)
	}
	if len(response.Clips) == 0 {
		return nil, artlist.ErrEmptyResult
	}
	return toCandidates(response.Clips), nil
}

func toCandidates(clips []Clip) []artlist.Candidate {
	out := make([]artlist.Candidate, 0, len(clips))
	for _, c := range clips {
		id := firstNonEmpty(c.ClipID, c.ID)
		title := firstNonEmpty(c.Title, c.Name, id)
		out = append(out, artlist.Candidate{
			ID:         id,
			Title:      title,
			SourceRef:  c.PrimaryURL,
			PageURL:    c.ClipPageURL,
			SourceName: "artlist",
		})
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
