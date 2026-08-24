package fallback

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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	artapp "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// pexelsVideoFile mirrors the inline JSON shape used both for
// decoding the response and for selecting the best progressive MP4.
// Declared as a named type so the function signature is readable.
type pexelsVideoFile struct {
	ID       int     `json:"id"`
	Quality  string  `json:"quality"`
	FileType string  `json:"file_type"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
	FPS      float64 `json:"fps"`
	Link     string  `json:"link"`
}

// Pexels is an HTTP-clamped implementation of artlist.Searcher
// backed by the Pexels /v1/videos/search endpoint.
type Pexels struct {
	client     *http.Client
	cfg        Config
	SourceName string
}

// NewPexels constructs a Pexels instance.
func NewPexels(cfg Config) *Pexels {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.pexels.com/v1"
	} else {
		cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.SourceName == "" {
		cfg.SourceName = "pexels"
	}
	return &Pexels{
		client:     &http.Client{Timeout: cfg.Timeout},
		cfg:        cfg,
		SourceName: cfg.SourceName,
	}
}

// Compile-time port assertion.
var _ artapp.Searcher = (*Pexels)(nil)

// Search queries the Pexels API. Per PR2.1 contract: returns only
// the centralised sentinels on transport failures; HTTP shape is
// never leaked to the caller.
func (p *Pexels) Search(ctx context.Context, req artapp.SearchRequest) ([]artapp.Candidate, error) {
	if strings.TrimSpace(p.cfg.APIKey) == "" {
		return nil, fmt.Errorf("%w: pexels api key not configured", artapp.ErrUnavailable)
	}
	term := strings.TrimSpace(req.Term)
	if term == "" {
		return nil, fmt.Errorf("%w: term required", artapp.ErrEmpty)
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}

	endpoint := p.cfg.BaseURL + "/videos/search"
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid pexels base url: %v", artapp.ErrInvalidResponse, err)
	}
	q := u.Query()
	q.Set("query", term)
	q.Set("per_page", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", artapp.ErrInvalidResponse, err)
	}
	httpReq.Header.Set("Authorization", p.cfg.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, mapTransportErr(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", artapp.ErrUnavailable, err)
	}

	if resp.StatusCode == http.StatusOK {
		return p.decode(body, term, limit)
	}
	return nil, mapStatusErr(resp.StatusCode, body)
}

func (p *Pexels) decode(body []byte, term string, limit int) ([]artapp.Candidate, error) {
	var payload struct {
		Videos []struct {
			ID         int               `json:"id"`
			URL        string            `json:"url"`
			Image      string            `json:"image"`
			Duration   int               `json:"duration"`
			VideoFiles []pexelsVideoFile `json:"video_files"`
			User       struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"user"`
		} `json:"videos"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: decode pexels: %v", artapp.ErrInvalidResponse, err)
	}

	out := make([]artapp.Candidate, 0, len(payload.Videos))
	for _, video := range payload.Videos {
		videoURL, rendition := bestPexelsVideoRendition(video.VideoFiles)
		if videoURL == "" {
			continue
		}

		// Bake user attribution into the title per PR2.4 design
		// decision. The canonical ProviderAsset keeps Creator as a
		// separate field, so we no longer need to stuff the author
		// into the title.
		title := term
		if video.User.Name != "" {
			title = fmt.Sprintf("%s by %s", term, video.User.Name)
		}

		pa := providerassets.ProviderAsset{
			Provider:     p.SourceName,
			ExternalID:   fmt.Sprintf("%d", video.ID),
			ID:           fmt.Sprintf("pexels-%d", video.ID),
			Title:        fmt.Sprintf("Pexels: %s", title),
			Creator:      video.User.Name,
			PageURL:      video.URL,
			PreviewURL:   video.URL,
			ThumbnailURL: video.Image,
			SourceRef:    videoURL,
			SourceName:   p.SourceName,
			MediaType:    asset.MediaTypeClip,
			DurationMs:   int64(video.Duration) * 1000,
		}
		if rendition.Width > 0 && rendition.Height > 0 {
			pa.Width = rendition.Width
			pa.Height = rendition.Height
			pa.FPSNumerator = rendition.FPSNumerator
			pa.FPSDenominator = rendition.FPSDenominator
			pa.Orientation = orientationFor(rendition.Width, rendition.Height)
		}
		if rendition.URL != "" {
			pa.Renditions = []providerassets.ProviderRendition{{
				Kind:      "master",
				Container: "mp4",
				Width:     rendition.Width,
				Height:    rendition.Height,
				URL:       rendition.URL,
			}}
		}
		out = append(out, pa)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no usable videos", artapp.ErrEmptyResult)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// bestPexelsVideoRendition picks the highest-resolution progressive MP4
// from Pexels' video_files list and returns both its URL and a full
// rendition descriptor.
//
// The legacy heuristic (provider_pexels.go::bestPexelsVideoURL)
// preferred HD first, then SD, then low, with progressive MP4 only.
// We keep that policy byte-for-byte so the new infra impl returns
// the same URLs the old code did.
func bestPexelsVideoRendition(files []pexelsVideoFile) (string, providerassets.ProviderRendition) {
	const (
		prefHDWidth, prefHDHeight = 1920, 1080
		prefSDWidth, prefSDHeight = 1280, 720
	)
	var (
		bestHD, bestSD, bestLow, fallback string
		bestHDArea                        int
	)
	for _, f := range files {
		if !strings.EqualFold(f.FileType, "video/mp4") {
			continue
		}
		px := f.Width * f.Height
		switch {
		case px >= prefHDWidth*prefHDHeight:
			if bestHD == "" || px > bestHDArea {
				bestHD = f.Link
				bestHDArea = px
			}
		case px >= prefSDWidth*prefSDHeight:
			if bestSD == "" {
				bestSD = f.Link
			}
		case bestLow == "":
			bestLow = f.Link
		case fallback == "":
			fallback = f.Link
		}
	}

	url := firstNonEmpty(bestHD, bestSD, bestLow, fallback)
	for _, f := range files {
		if f.Link == url {
			return url, providerassets.ProviderRendition{
				Kind:           "master",
				Container:      "mp4",
				Width:          f.Width,
				Height:         f.Height,
				FPSNumerator:   int(f.FPS * 1000),
				FPSDenominator: 1000,
				URL:            f.Link,
			}
		}
	}
	return url, providerassets.ProviderRendition{}
}

// orientationFor returns a canonical orientation label from pixel dimensions.
func orientationFor(width, height int) string {
	if width == 0 || height == 0 {
		return ""
	}
	if width == height {
		return "square"
	}
	if width > height {
		return "landscape"
	}
	return "portrait"
}
