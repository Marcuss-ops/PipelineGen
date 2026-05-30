package artlist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

func (ss *SearchService) searchLiveWithFallbacks(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	s := ss.service
	term = normalizeSearchTerm(term)
	if term == "" {
		return nil, fmt.Errorf("term is required")
	}
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	// ─── LEVEL 1: In-memory cache check ─────────────────────────────────────
	ttlHours := 24
	if s.cfg != nil && s.cfg.External.ArtlistLiveSearchCacheTTLHours > 0 {
		ttlHours = s.cfg.External.ArtlistLiveSearchCacheTTLHours
	}
	ttl := time.Duration(ttlHours) * time.Hour

	if s.liveCache.isFresh(term, ttl) {
		cached, _ := s.liveCache.get(term)
		s.log.Info("artlist live search: cache HIT", zap.String("term", term), zap.Int("clips", len(cached)))

		// ─── LEVEL 2: Background refresh if cache is > 75% of TTL ────────────
		if s.liveCache.isGettingStale(term, ttl) {
			s.log.Info("artlist live search: cache getting stale, scheduling background refresh", zap.String("term", term))
			go func() {
				bgCtx := context.WithoutCancel(ctx)
				if freshClips, err := ss.searchArtlistLive(bgCtx, term, limit); err == nil && len(freshClips) > 0 {
					s.liveCache.set(term, freshClips)
					s.log.Info("artlist background refresh: cache updated", zap.String("term", term), zap.Int("clips", len(freshClips)))
				} else if err != nil {
					s.log.Warn("artlist background refresh: live search failed", zap.String("term", term), zap.Error(err))
				}
			}()
		}

		if len(cached) > limit {
			cached = cached[:limit]
		}
		return cached, nil
	}

	// ─── CACHE MISS: Run live search ─────────────────────────────────────────
	if clips, err := ss.searchArtlistLive(ctx, term, limit); err == nil && len(clips) > 0 {
		s.liveCache.set(term, clips) // populate cache for next call
		return clips, nil
	} else if err != nil {
		s.log.Warn("artlist live search failed, trying fallbacks", zap.String("term", term), zap.Error(err))
	}

	var fallbackErrors []string

	if clips, err := ss.searchPixabayVideos(ctx, term, limit); err == nil && len(clips) > 0 {
		s.log.Info("pixabay fallback search succeeded", zap.String("term", term), zap.Int("clips_found", len(clips)))
		return clips, nil
	} else if err != nil {
		fallbackErrors = append(fallbackErrors, "pixabay: "+err.Error())
	}

	if clips, err := ss.searchPexelsVideos(ctx, term, limit); err == nil && len(clips) > 0 {
		s.log.Info("pexels fallback search succeeded", zap.String("term", term), zap.Int("clips_found", len(clips)))
		return clips, nil
	} else if err != nil {
		fallbackErrors = append(fallbackErrors, "pexels: "+err.Error())
	}

	if len(fallbackErrors) == 0 {
		return nil, fmt.Errorf("no live Artlist fallback provider is configured or returned results")
	}
	return nil, fmt.Errorf("%s", strings.Join(fallbackErrors, "; "))
}

func (ss *SearchService) searchArtlistLive(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	s := ss.service

	// ─── LEVEL 3: Use persistent Node.js HTTP server if configured ───────────
	// This avoids cold-starting Chromium on every request (saves 20-40s).
	// To enable: set external.artlist_scraper_server_url = "http://localhost:9123"
	// and run: node node-scraper/artlist_search.js --server --port 9123
	if s.cfg != nil && strings.TrimSpace(s.cfg.External.ArtlistScraperServerURL) != "" {
		return ss.searchArtlistViaServer(ctx, term, limit, s.cfg.External.ArtlistScraperServerURL)
	}

	// ─── Fallback: Legacy exec.Command (cold start, slower) ──────────────────
	return ss.searchArtlistViaExec(ctx, term, limit)
}

// searchArtlistViaServer calls the persistent Node.js scraper HTTP server.
// The server keeps Chromium alive between requests, reducing latency from 30-50s to 5-10s.
func (ss *SearchService) searchArtlistViaServer(ctx context.Context, term string, limit int, serverURL string) ([]ScraperClip, error) {
	s := ss.service

	type searchRequest struct {
		Term  string `json:"term"`
		Limit int    `json:"limit"`
	}
	body, err := json.Marshal(searchRequest{Term: term, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("artlist server: marshal request: %w", err)
	}

	reqURL := strings.TrimRight(serverURL, "/") + "/search"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("artlist server: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		s.log.Warn("artlist scraper server unreachable, falling back to exec", zap.String("url", reqURL), zap.Error(err))
		return ss.searchArtlistViaExec(ctx, term, limit) // graceful fallback
	}
	defer resp.Body.Close()

	var response ScraperResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("artlist server: decode response: %w", err)
	}

	s.log.Info("artlist server search completed", zap.String("term", term), zap.Int("clips", len(response.Clips)))
	return response.Clips, nil
}

// searchArtlistViaExec is the legacy cold-start approach (spawns a new Node process per call).
func (ss *SearchService) searchArtlistViaExec(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	s := ss.service

	scraperDir := os.Getenv("VELOX_NODE_SCRAPER_DIR")
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

	s.log.Info("Running live Artlist search (exec)", zap.String("term", term), zap.Int("limit", limit), zap.String("script_path", scriptPath))

	if err := cmd.Run(); err != nil {
		s.log.Error("Artlist scraper failed", zap.Error(err), zap.String("stderr", stderr.String()))
		return nil, fmt.Errorf("scraper failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	stdoutStr := stdout.String()
	s.log.Info("Scraper raw output received", zap.Int("bytes", len(stdoutStr)))

	var response ScraperResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		s.log.Error("failed to decode scraper response", zap.Error(err), zap.String("output", stdoutStr))
		return nil, fmt.Errorf("failed to decode scraper response: %w", err)
	}

	s.log.Info("Live Artlist search completed (exec)", zap.String("term", term), zap.Int("clips_found", len(response.Clips)))
	return response.Clips, nil
}


func (ss *SearchService) searchPixabayVideos(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	cfg := ss.service.cfg
	if cfg == nil || strings.TrimSpace(cfg.External.PixabayAPIKey) == "" {
		return nil, fmt.Errorf("pixabay api key not configured")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.External.PixabayBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://pixabay.com/api"
	}

	endpoint := baseURL + "/videos/"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid pixabay base url: %w", err)
	}

	q := u.Query()
	q.Set("key", cfg.External.PixabayAPIKey)
	q.Set("q", term)
	q.Set("lang", "it")
	q.Set("video_type", "all")
	q.Set("per_page", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pixabay search failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Hits []struct {
			ID      int    `json:"id"`
			PageURL string `json:"pageURL"`
			Tags    string `json:"tags"`
			Videos  struct {
				Medium struct {
					URL string `json:"url"`
				} `json:"medium"`
				Large struct {
					URL string `json:"url"`
				} `json:"large"`
				Small struct {
					URL string `json:"url"`
				} `json:"small"`
			} `json:"videos"`
		} `json:"hits"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to decode pixabay response: %w", err)
	}

	clips := make([]ScraperClip, 0, len(payload.Hits))
	for _, hit := range payload.Hits {
		videoURL := firstNonEmpty(hit.Videos.Medium.URL, hit.Videos.Large.URL, hit.Videos.Small.URL)
		if videoURL == "" {
			continue
		}

		title := strings.TrimSpace(hit.Tags)
		if title == "" {
			title = term
		}

		clips = append(clips, ScraperClip{
			ClipID:      fmt.Sprintf("pixabay-%d", hit.ID),
			ID:          fmt.Sprintf("pixabay-%d", hit.ID),
			Title:       fmt.Sprintf("Pixabay: %s", title),
			Name:        fmt.Sprintf("Pixabay: %s", title),
			PrimaryURL:  videoURL,
			ClipPageURL: hit.PageURL,
			StreamURLs:  []string{videoURL},
		})
	}

	if len(clips) == 0 {
		return nil, fmt.Errorf("pixabay returned no usable videos")
	}
	if len(clips) > limit {
		clips = clips[:limit]
	}
	return clips, nil
}

func (ss *SearchService) searchPexelsVideos(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	cfg := ss.service.cfg
	if cfg == nil || strings.TrimSpace(cfg.External.PexelsAPIKey) == "" {
		return nil, fmt.Errorf("pexels api key not configured")
	}

	baseURL := strings.TrimRight(strings.TrimSpace(cfg.External.PexelsBaseURL), "/")
	if baseURL == "" {
		baseURL = "https://api.pexels.com/v1"
	}

	endpoint := baseURL + "/videos/search"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid pexels base url: %w", err)
	}

	q := u.Query()
	q.Set("query", term)
	q.Set("per_page", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", cfg.External.PexelsAPIKey)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pexels search failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		Videos []struct {
			ID         int    `json:"id"`
			URL        string `json:"url"`
			Image      string `json:"image"`
			Duration   int    `json:"duration"`
			VideoFiles []struct {
				ID       int     `json:"id"`
				Quality  string  `json:"quality"`
				FileType string  `json:"file_type"`
				Width    int     `json:"width"`
				Height   int     `json:"height"`
				FPS      float64 `json:"fps"`
				Link     string  `json:"link"`
			} `json:"video_files"`
			User struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"user"`
		} `json:"videos"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to decode pexels response: %w", err)
	}

	clips := make([]ScraperClip, 0, len(payload.Videos))
	for _, video := range payload.Videos {
		videoURL := bestPexelsVideoURL(video.VideoFiles)
		if videoURL == "" {
			continue
		}

		title := term
		if video.User.Name != "" {
			title = fmt.Sprintf("%s by %s", term, video.User.Name)
		}

		clips = append(clips, ScraperClip{
			ClipID:      fmt.Sprintf("pexels-%d", video.ID),
			ID:          fmt.Sprintf("pexels-%d", video.ID),
			Title:       fmt.Sprintf("Pexels: %s", title),
			Name:        fmt.Sprintf("Pexels: %s", title),
			PrimaryURL:  videoURL,
			ClipPageURL: video.URL,
			StreamURLs:  []string{videoURL},
		})
	}

	if len(clips) == 0 {
		return nil, fmt.Errorf("pexels returned no usable videos")
	}
	if len(clips) > limit {
		clips = clips[:limit]
	}
	return clips, nil
}

func bestPexelsVideoURL(files []struct {
	ID       int     `json:"id"`
	Quality  string  `json:"quality"`
	FileType string  `json:"file_type"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Link     string  `json:"link"`
}) string {
	var bestURL string
	bestScore := -1
	for _, f := range files {
		if strings.TrimSpace(f.Link) == "" {
			continue
		}
		score := f.Width * f.Height
		if strings.EqualFold(f.Quality, "hd") {
			score += 1_000_000
		}
		if score > bestScore {
			bestScore = score
			bestURL = f.Link
		}
	}
	return bestURL
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
