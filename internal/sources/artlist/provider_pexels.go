package artlist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PexelsProvider searches Pexels' free video API as a fallback source.
type PexelsProvider struct {
	apiKey  string
	baseURL string
}

// NewPexelsProvider creates a new PexelsProvider.
// If baseURL is empty, defaults to "https://api.pexels.com/v1".
func NewPexelsProvider(apiKey, baseURL string) *PexelsProvider {
	if baseURL == "" {
		baseURL = "https://api.pexels.com/v1"
	}
	return &PexelsProvider{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

func (p *PexelsProvider) Name() string { return "pexels" }

func (p *PexelsProvider) Search(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("pexels api key not configured")
	}

	endpoint := p.baseURL + "/videos/search"
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
	req.Header.Set("Authorization", p.apiKey)
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
