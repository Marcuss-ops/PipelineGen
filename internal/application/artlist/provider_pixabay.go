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

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// PixabayProvider searches Pixabay's free video API as a fallback source.
type PixabayProvider struct {
	apiKey  string
	baseURL string
}

// NewPixabayProvider creates a new PixabayProvider.
// If baseURL is empty, defaults to "https://pixabay.com/api".
func NewPixabayProvider(apiKey, baseURL string) *PixabayProvider {
	if baseURL == "" {
		baseURL = "https://pixabay.com/api"
	}
	return &PixabayProvider{apiKey: apiKey, baseURL: strings.TrimRight(baseURL, "/")}
}

func (p *PixabayProvider) Name() string { return "pixabay" }

func (p *PixabayProvider) Search(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("pixabay api key not configured")
	}

	endpoint := p.baseURL + "/videos/"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid pixabay base url: %w", err)
	}

	q := u.Query()
	q.Set("key", p.apiKey)
	q.Set("q", term)
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
		videoURL := textutil.FirstNonEmpty(hit.Videos.Medium.URL, hit.Videos.Large.URL, hit.Videos.Small.URL)
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
