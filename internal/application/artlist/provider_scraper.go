package artlist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ScraperProvider performs live searches via the persistent Node.js scraper
// server (preferred) or falls back to exec.Command (cold start).
type ScraperProvider struct {
	cfg scraperConfig
	log *zap.Logger
}

type scraperConfig struct {
	ServerURL  string
	ScraperDir string
}

// NewScraperProvider creates a new ScraperProvider.
func NewScraperProvider(serverURL, scraperDir string, log *zap.Logger) *ScraperProvider {
	return &ScraperProvider{
		cfg: scraperConfig{
			ServerURL:  strings.TrimSpace(serverURL),
			ScraperDir: scraperDir,
		},
		log: log,
	}
}

func (p *ScraperProvider) Name() string { return "scraper" }

func (p *ScraperProvider) Search(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	if p.cfg.ServerURL != "" {
		return p.searchViaServer(ctx, term, limit)
	}
	return p.searchViaExec(ctx, term, limit)
}

func (p *ScraperProvider) searchViaServer(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	type searchRequest struct {
		Term  string `json:"term"`
		Limit int    `json:"limit"`
	}
	body, err := json.Marshal(searchRequest{Term: term, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("artlist server: marshal request: %w", err)
	}

	reqURL := strings.TrimRight(p.cfg.ServerURL, "/") + "/search"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("artlist server: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		p.log.Warn("artlist scraper server unreachable, falling back to exec", zap.String("url", reqURL), zap.Error(err))
		return p.searchViaExec(ctx, term, limit)
	}
	defer resp.Body.Close()

	var response ScraperResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("artlist server: decode response: %w", err)
	}

	p.log.Info("artlist server search completed", zap.String("term", term), zap.Int("clips", len(response.Clips)))
	return response.Clips, nil
}

func (p *ScraperProvider) searchViaExec(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	scraperDir := p.cfg.ScraperDir
	if scraperDir == "" {
		scraperDir = "node-scraper"
	}
	if absDir, err := filepath.Abs(scraperDir); err == nil {
		scraperDir = absDir
	}
	scriptPath := filepath.Join(scraperDir, "artlist_search.js")

	if _, err := exec.LookPath("node"); err != nil {
		return nil, fmt.Errorf("node not found in PATH: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	args := []string{scriptPath, "--term", term, "--limit", strconv.Itoa(limit)}
	cmd := exec.CommandContext(ctx, "node", args...)
	cmd.Dir = scraperDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	p.log.Info("Running live Artlist search (exec)", zap.String("term", term), zap.Int("limit", limit), zap.String("script_path", scriptPath))

	if err := cmd.Run(); err != nil {
		p.log.Error("Artlist scraper failed", zap.Error(err), zap.String("stderr", stderr.String()))
		return nil, fmt.Errorf("scraper failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	var response ScraperResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		p.log.Error("failed to decode scraper response", zap.Error(err), zap.String("output", stdout.String()))
		return nil, fmt.Errorf("failed to decode scraper response: %w", err)
	}

	p.log.Info("Live Artlist search completed (exec)", zap.String("term", term), zap.Int("clips_found", len(response.Clips)))
	return response.Clips, nil
}
